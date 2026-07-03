package agent

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/ryuclub/ai-workflow/internal/core/store"
)

// watchLoop 是事件唤醒 watcher：订阅全局事件流，规则命中即唤醒对应租户的 Agent；
// 另起定时扫描提醒「人审/PR 审查滞留」。Token 纪律：只注入摘要，不灌全量事件。
func (m *Manager) watchLoop(ctx context.Context) {
	lastID, err := m.st.MaxEventID() // 从当下开始，不回放历史
	if err != nil {
		return
	}
	subID, nudge := m.bus.SubscribeAll()
	defer m.bus.UnsubscribeAll(subID)

	remind := time.NewTicker(10 * time.Minute)
	defer remind.Stop()
	var reminded sync.Map // taskID+state → 已提醒（进程内去重；重启后至多重提醒一次）

	for {
		select {
		case <-ctx.Done():
			return
		case <-nudge:
			evs, err := m.st.ListEventsSince(lastID, 500)
			if err != nil {
				continue
			}
			for _, ev := range evs {
				lastID = ev.ID
				m.maybeWakeOnEvent(ev)
			}
		case <-remind.C:
			m.remindStale(&reminded)
		}
	}
}

// maybeWakeOnEvent 对单条事件应用唤醒规则。
func (m *Manager) maybeWakeOnEvent(ev *store.Event) {
	if ev.TenantID == "" || !m.Enabled(ev.TenantID) {
		return
	}
	// Agent 自身产生的事件不回喂，防自激。
	if ev.Type == "agent.notice" || ev.Type == "agent.action" {
		return
	}
	switch ev.Type {
	case "task.failed":
		tk, err := m.st.GetTask(ev.TaskID)
		if err != nil || tk == nil {
			return
		}
		text := fmt.Sprintf(
			"[系统事件] 任务 %s（%s，repo=%s）失败转待裁决：%s。请查明原因（read_task_log / list_task_events），给出简短结论与建议；恢复动作在白名单内可直接执行。",
			taskRef(tk), trim(tk.Title, 40), tk.Repo, trim(ev.Message, 200))
		_ = m.Wake(ev.TenantID, "wake", tk.ID, text)
	case "task.awaiting_review":
		// 塔台自动审核（租户设置门控）：Issue 产出即唤醒审核流程。
		if !m.env.Config(ev.TenantID).AgentAutoReview() {
			return
		}
		tk, err := m.st.GetTask(ev.TaskID)
		if err != nil || tk == nil || tk.State != store.StateAwaitingReview {
			return
		}
		text := fmt.Sprintf(
			"[系统事件] 任务 %s（%s，repo=%s）B 段完成，Issue #%d 待审核；本租户已启用塔台自动审核，请立即执行审核："+
				"1) get_ticket + get_issue 对照审查（需求覆盖是否完整、方案与仓库现状是否吻合、验收条件是否可执行、方案分歧点选项是否明确）；"+
				"2) 无阻碍且选项明确 → 需勾选的分歧点先用 update_issue 勾选（[x]），然后 approve_issue 通过并简短通报审核要点；"+
				"3) 有真阻碍或选项不明确 → 调 escalate_review 上报人工并说明原因，不要通过。",
			taskRef(tk), trim(tk.Title, 40), tk.Repo, tk.IssueNum)
		_ = m.Wake(ev.TenantID, "wake", tk.ID, text)
	}
}

// remindStale 扫描滞留在人审/PR 审查闸口超阈值的任务，提醒一次。
func (m *Manager) remindStale(reminded *sync.Map) {
	tasks, err := m.st.ListTasks()
	if err != nil {
		return
	}
	for _, tk := range tasks {
		if tk.TenantID == "" || !m.Enabled(tk.TenantID) {
			continue
		}
		h := m.env.Config(tk.TenantID).AgentReviewRemindH()
		if h <= 0 {
			continue
		}
		var gate string
		switch tk.State {
		case store.StateAwaitingReview:
			gate = "人审闸口"
		case store.StateAwaitingPRReview:
			gate = "PR 审查闸口"
			h *= 2 // PR 审查通常更慢，阈值放宽一倍
		default:
			continue
		}
		if time.Since(tk.UpdatedAt) < time.Duration(h)*time.Hour {
			continue
		}
		key := tk.ID + "/" + string(tk.State)
		if _, dup := reminded.LoadOrStore(key, true); dup {
			continue
		}
		text := fmt.Sprintf(
			"[系统事件] 任务 %s（%s）已在%s滞留超过 %d 小时。请简要通报并提醒相关人处理；如判断票已无效可建议打回。",
			taskRef(tk), trim(tk.Title, 40), gate, h)
		_ = m.Wake(tk.TenantID, "wake", tk.ID, text)
	}
}
