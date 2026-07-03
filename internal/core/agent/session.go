package agent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/ryuclub/ai-workflow/internal/core/store"
)

// session 是某租户的常驻 claude CLI 会话（stream-json 双向）。
type session struct {
	m        *Manager
	tenantID string

	cmd     *exec.Cmd
	stdin   io.WriteCloser
	writeMu sync.Mutex

	lastAt        atomic.Int64 // unix nano：最后活动时间（空闲回收判据）
	pendingNotice atomic.Int64 // 待转发 Slack 的唤醒回合数
	curTask       atomic.Value // string：当前话题任务 id（塔台回复继承该归属，供按任务过滤）
	stopping      atomic.Bool  // 主动回收标记（区分异常退出）
	dead          atomic.Bool
}

// currentTask 返回会话当前话题任务 id（可空）。
func (s *session) currentTask() string {
	v, _ := s.curTask.Load().(string)
	return v
}

// startSession 拉起 claude 子进程。brief 为空且有持久化会话 id 则 --resume 找回上下文；
// brief 非空表示交接班：放弃旧上下文新开会话，交接摘要并入系统提示（不占对话回合）。
func (m *Manager) startSession(tenantID, brief string) (*session, error) {
	cfg := m.env.Config(tenantID)
	dir := cfg.AgentDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("建 Agent 工作目录失败：%w", err)
	}

	args := []string{
		"-p",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--include-partial-messages",
		"--verbose",
		"--strict-mcp-config",
		"--mcp-config", m.mcpConfigJSON(tenantID),
		"--allowedTools", "mcp__wf",
		"--dangerously-skip-permissions",
		"--system-prompt", systemPrompt + briefSuffix(brief),
	}
	// 通用能力（默认开）：保留 claude 内建工具（Bash/Read/搜索/联网…），塔台≈本地终端会话，
	// 流水线操作仍走 wf MCP 工具（服务端白名单/确认流）。AGENT_GENERAL=false 回到纯 MCP 沙箱
	// （多租户共用宿主且互不信任时建议关闭：内建工具运行在控制面主机上）。
	if !cfg.AgentGeneral() {
		args = append(args, "--tools", "")
	}
	if mdl := cfg.AgentModel(); mdl != "" {
		args = append(args, "--model", mdl)
	}
	if sid, _, _ := m.st.GetAgentSession(tenantID); sid != "" && brief == "" {
		args = append(args, "--resume", sid)
	} else {
		args = append(args, "--session-id", uuid.NewString())
	}

	cmd := exec.Command(cfg.ClaudeBin(), args...)
	cmd.Dir = dir
	// 登录态令牌注入：与 runner 相同的两级隔离（剔宿主凭据 → 注租户令牌；无 vault 回落宿主登录态）。
	env := os.Environ()
	if tok := m.env.ClaudeToken(tenantID, ""); tok != "" {
		env = filterEnv(env, "CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY")
		env = append(env, "CLAUDE_CODE_OAUTH_TOKEN="+tok)
	}
	cmd.Env = env

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = nil // stderr 丢弃：诊断走 stream-json 的 result/error 事件
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("启动 claude 失败：%w", err)
	}

	s := &session{m: m, tenantID: tenantID, cmd: cmd, stdin: stdin}
	s.touch()
	go s.readLoop(stdout)
	return s, nil
}

// mcpConfigJSON 生成指向控制面 /internal/mcp 的 MCP 配置。租户头由控制面固化，子进程无法跨租户。
func (m *Manager) mcpConfigJSON(tenantID string) string {
	cfg := m.env.Config(tenantID)
	conf := map[string]any{
		"mcpServers": map[string]any{
			"wf": map[string]any{
				"type": "http",
				"url":  cfg.EventURLBase() + "/internal/mcp",
				"headers": map[string]string{
					"X-Internal-Token": cfg.InternalToken(),
					"X-WF-Tenant":      tenantID,
				},
			},
		},
	}
	b, _ := json.Marshal(conf)
	return string(b)
}

func (s *session) alive() bool           { return !s.dead.Load() }
func (s *session) lastActive() time.Time { return time.Unix(0, s.lastAt.Load()) }
func (s *session) touch()                { s.lastAt.Store(time.Now().UnixNano()) }

// sendUser 写入一条用户消息（stream-json 输入协议；CLI 侧串行排队处理）。
func (s *session) sendUser(text string) error {
	line, _ := json.Marshal(map[string]any{
		"type":    "user",
		"message": map[string]any{"role": "user", "content": text},
	})
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.dead.Load() {
		return fmt.Errorf("会话已退出")
	}
	s.touch()
	_, err := s.stdin.Write(append(line, '\n'))
	return err
}

// stop 主动回收：关 stdin 让 CLI 正常收尾，超时兜底杀进程。
func (s *session) stop() {
	s.stopping.Store(true)
	s.writeMu.Lock()
	_ = s.stdin.Close()
	s.writeMu.Unlock()
	done := make(chan struct{})
	go func() { _ = s.cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		_ = s.cmd.Process.Kill()
	}
	s.dead.Store(true)
}

// streamMsg 是 claude stream-json 输出的一行（只解我们关心的字段）。
type streamMsg struct {
	Type      string          `json:"type"`
	Subtype   string          `json:"subtype"`
	SessionID string          `json:"session_id"`
	Message   json.RawMessage `json:"message"`
	Event     json.RawMessage `json:"event"`
	Result    string          `json:"result"`
	IsError   bool            `json:"is_error"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
	Name string `json:"name"` // tool_use
}

// readLoop 消费子进程 stdout：init 记会话 id；assistant 文本落库推流；
// stream_event 转打字机增量；result 结束回合（唤醒回合把结论转 Slack）。
func (s *session) readLoop(stdout io.Reader) {
	var lastText string
	r := bufio.NewReaderSize(stdout, 1<<20)
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 1 {
			s.touch()
			var msg streamMsg
			if json.Unmarshal(line, &msg) == nil {
				switch msg.Type {
				case "system":
					if msg.Subtype == "init" && msg.SessionID != "" {
						_ = s.m.st.PutAgentSession(s.tenantID, msg.SessionID)
					}
				case "assistant":
					if text := extractText(msg.Message); text != "" {
						lastText = text
						_ = s.m.st.AppendAgentMessage(&store.AgentMessage{
							TenantID: s.tenantID, Role: "assistant", Kind: "chat", Content: text,
							TaskID: s.currentTask(), // 回复继承当前话题任务的归属
						})
						s.m.notifyChat(s.tenantID, ChatSignal{})
					}
				case "stream_event":
					if delta := extractDelta(msg.Event); delta != "" {
						s.m.notifyChat(s.tenantID, ChatSignal{Delta: delta})
					}
				case "result":
					if msg.IsError {
						s.m.appendSystem(s.tenantID, "chat", "Agent 回合出错："+trim(msg.Result, 300))
					}
					// 唤醒回合结束：把最终结论转发 Slack（agent.notice）。
					if s.pendingNotice.Load() > 0 {
						s.pendingNotice.Add(-1)
						s.m.publishNotice(s.tenantID, lastText)
					}
				}
			}
		}
		if err != nil {
			break
		}
	}
	s.dead.Store(true)
	if !s.stopping.Load() {
		s.m.appendSystem(s.tenantID, "chat", "Agent 会话已退出（下次发言自动重启并恢复上下文）")
	}
}

// extractText 从 assistant message 中拼接全部 text 块。
func extractText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var m struct {
		Content []contentBlock `json:"content"`
	}
	if json.Unmarshal(raw, &m) != nil {
		return ""
	}
	var b strings.Builder
	for _, c := range m.Content {
		if c.Type == "text" {
			b.WriteString(c.Text)
		}
	}
	return strings.TrimSpace(b.String())
}

// extractDelta 从 stream_event 中取文本增量（content_block_delta / text_delta）。
func extractDelta(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var ev struct {
		Type  string `json:"type"`
		Delta struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"delta"`
	}
	if json.Unmarshal(raw, &ev) != nil {
		return ""
	}
	if ev.Type == "content_block_delta" && ev.Delta.Type == "text_delta" {
		return ev.Delta.Text
	}
	return ""
}

// filterEnv 返回剔除了指定 KEY 的环境副本（与 runner 同款，防跨租户串登录态）。
func filterEnv(env []string, drop ...string) []string {
	out := make([]string, 0, len(env))
	for _, e := range env {
		keep := true
		for _, k := range drop {
			if strings.HasPrefix(e, k+"=") {
				keep = false
				break
			}
		}
		if keep {
			out = append(out, e)
		}
	}
	return out
}

func trim(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// briefSuffix 把交接摘要拼进系统提示（空则不拼）。
func briefSuffix(brief string) string {
	if brief == "" {
		return ""
	}
	return "\n\n" + brief
}
