// Package agent 是 M7 调度 Agent：骑在固定状态机之上的「塔台」（值班调度）。
// 每租户一个常驻 claude CLI 会话（stream-json 双向），经 MCP 工具观察/操作流水线；
// 事件唤醒规则命中时主动分析并发话。设计见 docs/agent-orchestrator-plan.md。
package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/ryuclub/ai-workflow/internal/core/config"
	"github.com/ryuclub/ai-workflow/internal/core/events"
	"github.com/ryuclub/ai-workflow/internal/core/github"
	"github.com/ryuclub/ai-workflow/internal/core/orchestrator"
	"github.com/ryuclub/ai-workflow/internal/core/source"
	"github.com/ryuclub/ai-workflow/internal/core/store"
)

// Store 是 agent 所需的持久化能力（任务/事件 + agent 三表），由 *store.SQLite 同时实现。
type Store interface {
	store.Store
	store.AgentStore
}

// Env 提供按租户解析的配置/票源/客户端/令牌（由 app.Runtime 实现）。
type Env interface {
	Config(tenantID string) *config.Config
	Provider(tenantID string) source.Provider
	Github(tenantID, userID string) *github.Client
	ClaudeToken(tenantID, userID string) string
}

// ChatSignal 是聊天订阅者收到的信号：Delta 非空为流式增量（不落库）；
// 空则表示「有新落库消息」，订阅者回 store 按游标拉齐。
type ChatSignal struct {
	Delta string
}

// Manager 管理各租户的 Agent 会话与聊天订阅。
type Manager struct {
	st   Store
	bus  *events.Bus
	env  Env
	orch *orchestrator.Orchestrator

	tools  *Tools
	health func(tenantID string) string // 健康面板快照钩子（api 层注入，可为 nil）

	mu       sync.Mutex
	sessions map[string]*session

	chatMu   sync.Mutex
	chatSubs map[string]map[int]chan ChatSignal
	nextSub  int

	reapOnce sync.Once
}

// NewManager 构造 Agent 管理器。
func NewManager(st Store, bus *events.Bus, env Env, orch *orchestrator.Orchestrator) *Manager {
	m := &Manager{
		st: st, bus: bus, env: env, orch: orch,
		sessions: map[string]*session{},
		chatSubs: map[string]map[int]chan ChatSignal{},
	}
	m.tools = newTools(m)
	return m
}

// SetHealthFunc 注入健康面板快照钩子（get_health 工具用；健康探测实现位于 api 层）。
func (m *Manager) SetHealthFunc(f func(tenantID string) string) { m.health = f }

// Tools 暴露工具执行器（MCP 端点与确认流用）。
func (m *Manager) Tools() *Tools { return m.tools }

// Enabled 返回该租户 Agent 是否启用。
func (m *Manager) Enabled(tenantID string) bool { return m.env.Config(tenantID).AgentEnabled() }

// Send 处理用户在聊天窗的发言：落库 → 确保会话 → 写入 stdin。
// taskID 非空表示该发言归属某任务（任务过滤视图下发送），塔台回复将继承该归属。
func (m *Manager) Send(tenantID, userID, taskID, content string) (*store.AgentMessage, error) {
	msg := &store.AgentMessage{TenantID: tenantID, Role: "user", Kind: "chat", Content: content, UserID: userID, TaskID: taskID}
	if err := m.st.AppendAgentMessage(msg); err != nil {
		return nil, err
	}
	m.notifyChat(tenantID, ChatSignal{})
	if err := m.deliver(tenantID, taskID, content, false); err != nil {
		m.appendSystem(tenantID, "chat", "Agent 会话启动失败："+err.Error())
		return msg, err
	}
	return msg, nil
}

// Wake 以系统事件唤醒 Agent（watcher / 动作结果注入用）。kind 记录消息来源类别；
// taskID 为关联任务（可空），供聊天窗按任务过滤。
func (m *Manager) Wake(tenantID, kind, taskID, content string) error {
	msg := &store.AgentMessage{TenantID: tenantID, Role: "system", Kind: kind, Content: content, TaskID: taskID}
	if err := m.st.AppendAgentMessage(msg); err != nil {
		return err
	}
	m.notifyChat(tenantID, ChatSignal{})
	return m.deliver(tenantID, taskID, content, kind == "wake")
}

// deliver 把内容写进该租户会话（懒启动）。notice=true 时该回合结束后把 Agent 结论转发 Slack；
// taskID 记为会话当前话题任务——塔台随后的回复按此归属（任务过滤视图能看到完整对话）。
func (m *Manager) deliver(tenantID, taskID, content string, notice bool) error {
	s, err := m.ensure(tenantID)
	if err != nil {
		return err
	}
	s.curTask.Store(taskID)
	if notice {
		s.pendingNotice.Add(1)
	}
	return s.sendUser(content)
}

// ensure 取（或懒启动）该租户会话；同时确保空闲回收器已启动。
// 交接班：班次（自上次交接起）超时长/消息量阈值时，不再 resume 旧上下文，
// 而是带「交接摘要」新开会话——控制 token 成本与旧记忆干扰（上下文越滚越大、
// 旧工具行为/旧结论会污染新判断）。
func (m *Manager) ensure(tenantID string) (*session, error) {
	m.reapOnce.Do(func() { go m.reapLoop() })
	m.mu.Lock()
	defer m.mu.Unlock()
	if s := m.sessions[tenantID]; s != nil && s.alive() && !m.shiftOver(tenantID) {
		return s, nil
	}
	brief := ""
	if m.shiftOver(tenantID) {
		if s := m.sessions[tenantID]; s != nil {
			s.stop()
			delete(m.sessions, tenantID)
		}
		brief = m.buildBriefing(tenantID)
		// 交接班标记消息：既是给团队看的分界线，也是下一班次的计时起点（epoch）。
		_ = m.st.AppendAgentMessage(&store.AgentMessage{
			TenantID: tenantID, Role: "system", Kind: "handover",
			Content: "🔄 交接班：本班次会话已达阈值，塔台带交接摘要换新会话上岗（历史见上文）。",
		})
		m.notifyChat(tenantID, ChatSignal{})
	}
	s, err := m.startSession(tenantID, brief)
	if err != nil {
		return nil, err
	}
	m.sessions[tenantID] = s
	return s, nil
}

// shiftOver 判断当前班次是否到点（自上次交接/首条消息起，超时长或超消息量）。
func (m *Manager) shiftOver(tenantID string) bool {
	epoch, err := m.st.AgentHandoverEpoch(tenantID)
	if err != nil || epoch.IsZero() {
		return false
	}
	cfg := m.env.Config(tenantID)
	if time.Since(epoch) > time.Duration(cfg.AgentHandoverH())*time.Hour {
		return true
	}
	n, _ := m.st.CountAgentMessagesSince(tenantID, epoch)
	return n > cfg.AgentHandoverMsgs()
}

// buildBriefing 从数据侧生成交接摘要（不依赖旧会话）：在办任务、待确认动作、近期对话摘录。
func (m *Manager) buildBriefing(tenantID string) string {
	var b strings.Builder
	b.WriteString("## 交接摘要（上一班次）\n")
	if tasks, err := m.st.ListTasksByTenant(tenantID); err == nil {
		b.WriteString("在办任务：\n")
		n := 0
		for _, t := range tasks {
			if t.State.Terminal() {
				continue
			}
			fmt.Fprintf(&b, "- %s %s [%s]\n", taskRef(t), t.Title, t.State)
			n++
		}
		if n == 0 {
			b.WriteString("- （无）\n")
		}
	}
	if acts, err := m.st.ListAgentActions(tenantID, store.ActionPending); err == nil && len(acts) > 0 {
		b.WriteString("待人确认的动作：\n")
		for _, a := range acts {
			fmt.Fprintf(&b, "- #%d %s：%s\n", a.ID, a.Tool, a.Summary)
		}
	}
	if msgs, err := m.st.ListAgentMessages(tenantID, 0, 8); err == nil && len(msgs) > 0 {
		b.WriteString("近期对话摘录（最新 8 条）：\n")
		for _, mm := range msgs {
			c := mm.Content
			if len(c) > 160 {
				c = c[:160] + "…"
			}
			fmt.Fprintf(&b, "- [%s] %s\n", mm.Role, strings.ReplaceAll(c, "\n", " "))
		}
	}
	return b.String()
}

// reapLoop 定期回收空闲会话（会话 id 已持久化，下次 resume 找回上下文）。
func (m *Manager) reapLoop() {
	tk := time.NewTicker(time.Minute)
	defer tk.Stop()
	for range tk.C {
		m.mu.Lock()
		for tid, s := range m.sessions {
			idle := time.Duration(m.env.Config(tid).AgentIdleMin()) * time.Minute
			if s.alive() && time.Since(s.lastActive()) > idle && s.pendingNotice.Load() == 0 {
				s.stop()
				delete(m.sessions, tid)
			}
		}
		m.mu.Unlock()
	}
}

// SubscribeChat 注册聊天信号通道（SSE 用）。
func (m *Manager) SubscribeChat(tenantID string) (int, <-chan ChatSignal) {
	m.chatMu.Lock()
	defer m.chatMu.Unlock()
	m.nextSub++
	id := m.nextSub
	if m.chatSubs[tenantID] == nil {
		m.chatSubs[tenantID] = map[int]chan ChatSignal{}
	}
	ch := make(chan ChatSignal, 16)
	m.chatSubs[tenantID][id] = ch
	return id, ch
}

// UnsubscribeChat 注销聊天订阅。
func (m *Manager) UnsubscribeChat(tenantID string, id int) {
	m.chatMu.Lock()
	defer m.chatMu.Unlock()
	if s := m.chatSubs[tenantID]; s != nil {
		delete(s, id)
		if len(s) == 0 {
			delete(m.chatSubs, tenantID)
		}
	}
}

func (m *Manager) notifyChat(tenantID string, sig ChatSignal) {
	m.chatMu.Lock()
	defer m.chatMu.Unlock()
	for _, ch := range m.chatSubs[tenantID] {
		select {
		case ch <- sig:
		default: // 订阅者滞后：丢增量无妨（落库消息靠游标拉齐）
		}
	}
}

// appendSystem 落一条系统消息并通知聊天订阅者（错误提示等，不进会话）。
func (m *Manager) appendSystem(tenantID, kind, content string) {
	_ = m.st.AppendAgentMessage(&store.AgentMessage{TenantID: tenantID, Role: "system", Kind: kind, Content: content})
	m.notifyChat(tenantID, ChatSignal{})
}

// publishNotice 把 Agent 的主动结论作为事件发布（level=warn → 走 SlackSink）。
func (m *Manager) publishNotice(tenantID, text string) {
	if text == "" {
		return
	}
	const maxLen = 800
	if len(text) > maxLen {
		text = text[:maxLen] + "…"
	}
	_ = m.bus.Publish(&store.Event{TenantID: tenantID, Type: "agent.notice", Level: "warn", Message: text})
}

// Recycle 主动回收某租户会话（设置变更后调用）：下次发言/唤醒按新配置重启，resume 保上下文。
func (m *Manager) Recycle(tenantID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s := m.sessions[tenantID]; s != nil {
		s.stop()
		delete(m.sessions, tenantID)
	}
}

// Shutdown 停掉所有会话（服务退出时调用；会话可 resume）。
func (m *Manager) Shutdown() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for tid, s := range m.sessions {
		s.stop()
		delete(m.sessions, tid)
	}
}

// StartWatcher 启动事件唤醒 watcher（main 调用）。
func (m *Manager) StartWatcher(ctx context.Context) { go m.watchLoop(ctx) }

// taskRef 返回任务的口头引用（#seq + 票号）。
func taskRef(t *store.Task) string { return fmt.Sprintf("#%d（%s）", t.Seq, t.SourceID) }
