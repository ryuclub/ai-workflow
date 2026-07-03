package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ryuclub/ai-workflow/internal/core/store"
)

// ToolDef 是一个暴露给 Agent 的 MCP 工具。
type ToolDef struct {
	Name        string
	Description string
	Schema      map[string]any // JSON Schema（inputSchema）
	Write       bool           // 写操作：过白名单策略
	HumanGate   bool           // 人审闸口语义：永远确认制，白名单不可放行
	Run         func(ctx context.Context, tenantID, actor string, args map[string]any) (string, error)
}

// Tools 是工具注册表 + 白名单策略执行器。
type Tools struct {
	m      *Manager
	defs   []*ToolDef
	byName map[string]*ToolDef
}

func newTools(m *Manager) *Tools {
	t := &Tools{m: m, byName: map[string]*ToolDef{}}
	for _, d := range t.buildDefs() {
		t.defs = append(t.defs, d)
		t.byName[d.Name] = d
	}
	return t
}

// List 返回工具定义（MCP tools/list 用）。
func (t *Tools) List() []*ToolDef { return t.defs }

// Call 是 MCP tools/call 入口：读工具直通；写工具过白名单，未命中落待确认动作。
func (t *Tools) Call(ctx context.Context, tenantID, name string, args map[string]any) (string, error) {
	def := t.byName[name]
	if def == nil {
		return "", fmt.Errorf("未知工具：%s", name)
	}
	if !def.Write {
		return def.Run(ctx, tenantID, "", args)
	}
	// 塔台自动审核（租户设置门控）：审核相关写工具对塔台直通，无需确认卡——
	// 这是用户显式开启的自动通道；approve_pr 不在此列，PR 审查恒需人工。
	autoReview := t.m.env.Config(tenantID).AgentAutoReview() && (name == "approve_issue" || name == "update_issue")
	if autoReview || (!def.HumanGate && t.whitelisted(tenantID, name)) {
		out, err := def.Run(ctx, tenantID, "", args)
		t.audit(tenantID, name, args, out, err, "白名单自动执行")
		if err != nil {
			return "", err
		}
		return out, nil
	}
	// 白名单外（或人审闸口）：落待确认动作，等人点头。
	argsJSON, _ := json.Marshal(args)
	taskID := "" // 关联任务（best-effort：多数写工具有 task 参数）
	if ref := argStr(args, "task"); ref != "" {
		if tk, err := t.findTask(tenantID, ref); err == nil {
			taskID = tk.ID
		}
	}
	act := &store.AgentAction{
		TenantID: tenantID, Tool: name, ArgsJSON: string(argsJSON),
		Summary: summarize(name, args), Status: store.ActionPending, TaskID: taskID,
	}
	if err := t.m.st.CreateAgentAction(act); err != nil {
		return "", err
	}
	_ = t.m.st.AppendAgentMessage(&store.AgentMessage{
		TenantID: tenantID, Role: "system", Kind: "action_request",
		Content: act.Summary, ActionID: act.ID, TaskID: taskID,
	})
	t.m.notifyChat(tenantID, ChatSignal{})
	return fmt.Sprintf("已创建待确认动作 #%d（%s），等待人工在聊天窗确认。结果会以 [动作结果] 消息通知你，本回合不要重复调用该工具。", act.ID, act.Summary), nil
}

// Confirm 人工确认待确认动作并执行；结果注入 Agent 会话。
func (t *Tools) Confirm(ctx context.Context, actionID int64, tenantID, userID string) (*store.AgentAction, error) {
	act, err := t.pending(actionID, tenantID)
	if err != nil {
		return nil, err
	}
	var args map[string]any
	_ = json.Unmarshal([]byte(act.ArgsJSON), &args)
	def := t.byName[act.Tool]
	if def == nil {
		return nil, fmt.Errorf("动作工具已不存在：%s", act.Tool)
	}
	out, runErr := def.Run(ctx, tenantID, userID, args)
	now := time.Now()
	act.DecidedBy, act.DecidedAt = userID, &now
	if runErr != nil {
		act.Status, act.Result = store.ActionFailed, runErr.Error()
	} else {
		act.Status, act.Result = store.ActionExecuted, out
	}
	_ = t.m.st.UpdateAgentAction(act)
	t.audit(tenantID, act.Tool, args, out, runErr, "人工确认执行")
	result := out
	if runErr != nil {
		result = "执行失败：" + runErr.Error()
	}
	_ = t.m.Wake(tenantID, "action_result", act.TaskID, fmt.Sprintf("[动作结果] 动作 #%d（%s）已确认并执行。结果：%s", act.ID, act.Summary, result))
	return act, nil
}

// Deny 人工拒绝待确认动作；理由注入 Agent 会话。
func (t *Tools) Deny(actionID int64, tenantID, userID, reason string) (*store.AgentAction, error) {
	act, err := t.pending(actionID, tenantID)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	act.Status, act.DecidedBy, act.DecidedAt, act.Result = store.ActionDenied, userID, &now, reason
	_ = t.m.st.UpdateAgentAction(act)
	if reason == "" {
		reason = "（未给理由）"
	}
	_ = t.m.Wake(tenantID, "action_result", act.TaskID, fmt.Sprintf("[动作被拒] 动作 #%d（%s）被人工拒绝。理由：%s", act.ID, act.Summary, reason))
	return act, nil
}

func (t *Tools) pending(actionID int64, tenantID string) (*store.AgentAction, error) {
	act, err := t.m.st.GetAgentAction(actionID)
	if err != nil {
		return nil, err
	}
	if act == nil || act.TenantID != tenantID {
		return nil, fmt.Errorf("动作不存在：#%d", actionID)
	}
	if act.Status != store.ActionPending {
		return nil, fmt.Errorf("动作 #%d 已处理（%s）", actionID, act.Status)
	}
	return act, nil
}

func (t *Tools) whitelisted(tenantID, name string) bool {
	for _, a := range t.m.env.Config(tenantID).AgentAutoActions() {
		if a == name {
			return true
		}
	}
	return false
}

// audit 把写操作执行结果发布为事件（任务详情/Slack 可见，全程可审计）。
func (t *Tools) audit(tenantID, tool string, args map[string]any, out string, err error, how string) {
	level, result := "info", out
	if err != nil {
		level, result = "warn", "失败："+err.Error()
	}
	_ = t.m.bus.Publish(&store.Event{
		TenantID: tenantID, Type: "agent.action", Level: level,
		Message: fmt.Sprintf("Agent %s：%s → %s", how, summarize(tool, args), trim(result, 200)),
	})
}

// summarize 生成给人看的动作一句话。
func summarize(tool string, args map[string]any) string {
	parts := make([]string, 0, len(args))
	for k, v := range args {
		parts = append(parts, fmt.Sprintf("%s=%v", k, v))
	}
	return tool + " " + strings.Join(parts, " ")
}

// --- 工具实现 ---

func (t *Tools) buildDefs() []*ToolDef {
	return []*ToolDef{
		{
			Name:        "list_tasks",
			Description: "列出本租户任务（含状态/票号/PR）。可选 state 过滤（如 running_c / awaiting_review / adjudication）。",
			Schema:      obj(map[string]any{"state": str("按任务状态过滤，可空")}),
			Run:         t.listTasks,
		},
		{
			Name:        "get_task",
			Description: "查看任务详情：状态、Issue/PR 链接、错误信息、修订轮次。task 用 #编号 / 票号 / uuid。",
			Schema:      obj(map[string]any{"task": str("任务引用：#编号、票号或 uuid")}, "task"),
			Run:         t.getTask,
		},
		{
			Name:        "list_task_events",
			Description: "查看某任务的事件流尾部（进度/日志事件）。",
			Schema:      obj(map[string]any{"task": str("任务引用"), "limit": num("返回条数，默认 30")}, "task"),
			Run:         t.listTaskEvents,
		},
		{
			Name:        "read_task_log",
			Description: "读某任务某阶段（B/C/D）claude 全量日志的尾部，排障用。",
			Schema:      obj(map[string]any{"task": str("任务引用"), "label": str("阶段：B / C / D"), "lines": num("尾部行数，默认 80")}, "task", "label"),
			Run:         t.readTaskLog,
		},
		{
			Name:        "list_tickets",
			Description: "列出票源（JIRA/Linear）候选工单，每行：票号 [状态] (经办人) 标题 | 标签 | URL。按人查票用经办人字段筛选。query 可为票号、标题关键词，JIRA 还支持原生 JQL 透传（含 = ~ AND ORDER BY 时原样执行）。",
			Schema:      obj(map[string]any{"query": str("搜索词，可空=默认最近")}),
			Run:         t.listTickets,
		},
		{
			Name:        "get_ticket",
			Description: "读工单全文（标题/状态/经办人/正文）。传 task=任务引用 读该任务对应的票，或直接传 ticket=票号（如 PROJ-1234，无需先起任务）。",
			Schema:      obj(map[string]any{"task": str("任务引用（与 ticket 二选一）"), "ticket": str("票号（与 task 二选一）")}),
			Run:         t.getTicket,
		},
		{
			Name:        "get_issue",
			Description: "读某任务 B 段产出的 GitHub Issue 全文（标题/正文/标签）——辅审时的审核对象。",
			Schema:      obj(map[string]any{"task": str("任务引用")}, "task"),
			Run:         t.getIssueTool,
		},
		{
			Name:        "get_pr",
			Description: "读某任务 PR 的标题/正文/完整 diff——审查 PR 时的审查对象（对照 get_issue 的最新 Issue）。",
			Schema:      obj(map[string]any{"task": str("任务引用")}, "task"),
			Run:         t.getPR,
		},
		{
			Name:        "update_issue",
			Description: "改写任务的 GitHub Issue 标题与正文（写操作；title/body 都必填——先 get_issue 取原文，改后整体提交）。",
			Schema:      obj(map[string]any{"task": str("任务引用"), "title": str("新标题"), "body": str("新正文（markdown 全文）")}, "task", "title", "body"),
			Write:       true,
			Run:         t.updateIssue,
		},
		{
			Name:        "get_health",
			Description: "查看健康面板快照（claude/gh/票源连通性）。",
			Schema:      obj(map[string]any{}),
			Run:         t.getHealth,
		},
		{
			Name:        "start_task",
			Description: "对某张票起新任务（写操作）。repo 为登记仓名。",
			Schema:      obj(map[string]any{"ticket_id": str("票号，如 PROJ-123 / ENG-12"), "repo": str("登记仓名")}, "ticket_id", "repo"),
			Write:       true,
			Run:         t.startTask,
		},
		{
			Name:        "resume_review",
			Description: "把中断/待裁决的任务送回审核闸口（写操作；Issue 已产出时无需重跑 B，直接恢复等待审核）。",
			Schema:      obj(map[string]any{"task": str("任务引用")}, "task"),
			Write:       true,
			Run:         t.resumeReview,
		},
		{
			Name:        "resume_pr_review",
			Description: "把中断的任务送回 PR 审查闸口（写操作；PR 已产出时无需重跑 D，直接恢复等待 PR 审查）。",
			Schema:      obj(map[string]any{"task": str("任务引用")}, "task"),
			Write:       true,
			Run:         t.resumePRReview,
		},
		{
			Name:        "restart_task",
			Description: "原地从指定段重跑同一任务（写操作，不开新任务）：stage=B 重新调查建 Issue（既有 Issue 走 upsert）/ C 重新实装（已有 PR 时按最新 Issue 对齐修正既有 PR，不重开）/ D 重新按意见修订。任务须非运行中；C 需已有 Issue 且审核已通过、D 需已有 PR。",
			Schema:      obj(map[string]any{"task": str("任务引用"), "stage": str("重跑起点：B / C / D")}, "task", "stage"),
			Write:       true,
			Run:         t.restartTask,
		},
		{
			Name:        "delete_task",
			Description: "删除已结束的任务及其记录（写操作；不可恢复）。",
			Schema:      obj(map[string]any{"task": str("任务引用")}, "task"),
			Write:       true,
			Run:         t.deleteTask,
		},
		{
			Name:        "cancel_task",
			Description: "取消运行中的任务（写操作；杀掉 claude 子进程）。",
			Schema:      obj(map[string]any{"task": str("任务引用")}, "task"),
			Write:       true,
			Run:         t.cancelTask,
		},
		{
			Name:        "approve_issue",
			Description: "审核通过 Issue、启动实装。默认需人工确认；租户启用「塔台自动审核」后对你直通。",
			Schema:      obj(map[string]any{"task": str("任务引用")}, "task"),
			Write:       true, HumanGate: true,
			Run: t.approveIssue,
		},
		{
			Name:        "escalate_review",
			Description: "自动审核中判定「需人工介入」时调用：点亮人审节点并 Slack 提醒人工（附原因）。仅用于等待审核态的任务。",
			Schema:      obj(map[string]any{"task": str("任务引用"), "reason": str("需要人工的原因（会展示给人看）")}, "task", "reason"),
			Run:         t.escalateReview,
		},
		{
			Name:        "reject_issue",
			Description: "人审打回 Issue（写操作）。",
			Schema:      obj(map[string]any{"task": str("任务引用"), "reason": str("打回理由")}, "task", "reason"),
			Write:       true,
			Run:         t.rejectIssue,
		},
		{
			Name:        "approve_pr",
			Description: "把任务标记为「PR 审查已通过」并结束（人审闸口：永远需人工确认）。只改流水线状态，不合并也不改动 PR——合并由人在 GitHub 上操作。",
			Schema:      obj(map[string]any{"task": str("任务引用")}, "task"),
			Write:       true, HumanGate: true,
			Run: t.approvePR,
		},
		{
			Name:        "request_revise",
			Description: "立即触发一轮 PR 修订（写操作；不等轮询）。",
			Schema:      obj(map[string]any{"task": str("任务引用")}, "task"),
			Write:       true,
			Run:         t.requestRevise,
		},
	}
}

func (t *Tools) listTasks(_ context.Context, tenantID, _ string, args map[string]any) (string, error) {
	tasks, err := t.m.st.ListTasksByTenant(tenantID)
	if err != nil {
		return "", err
	}
	state, _ := args["state"].(string)
	var b strings.Builder
	n := 0
	for _, tk := range tasks {
		if state != "" && string(tk.State) != state {
			continue
		}
		fmt.Fprintf(&b, "#%d [%s] %s %s (repo=%s)", tk.Seq, tk.State, tk.SourceID, trim(tk.Title, 40), tk.Repo)
		if tk.PRURL != "" {
			fmt.Fprintf(&b, " PR=%s", tk.PRURL)
		}
		if tk.Error != "" {
			fmt.Fprintf(&b, " err=%s", trim(tk.Error, 80))
		}
		b.WriteString("\n")
		if n++; n >= 50 {
			b.WriteString("（已截断，仅前 50 条）\n")
			break
		}
	}
	if n == 0 {
		return "（无匹配任务）", nil
	}
	return b.String(), nil
}

func (t *Tools) getTask(_ context.Context, tenantID, _ string, args map[string]any) (string, error) {
	tk, err := t.findTask(tenantID, argStr(args, "task"))
	if err != nil {
		return "", err
	}
	b, _ := json.MarshalIndent(tk, "", "  ")
	return string(b), nil
}

func (t *Tools) listTaskEvents(_ context.Context, tenantID, _ string, args map[string]any) (string, error) {
	tk, err := t.findTask(tenantID, argStr(args, "task"))
	if err != nil {
		return "", err
	}
	limit := argInt(args, "limit", 30)
	evs, err := t.m.st.ListEvents(tk.ID, 0)
	if err != nil {
		return "", err
	}
	if len(evs) > limit {
		evs = evs[len(evs)-limit:]
	}
	var b strings.Builder
	for _, ev := range evs {
		fmt.Fprintf(&b, "%s [%s/%s] %s\n", ev.TS.Format("01-02 15:04"), ev.Type, ev.Level, ev.Message)
	}
	if b.Len() == 0 {
		return "（无事件）", nil
	}
	return b.String(), nil
}

func (t *Tools) readTaskLog(_ context.Context, tenantID, _ string, args map[string]any) (string, error) {
	tk, err := t.findTask(tenantID, argStr(args, "task"))
	if err != nil {
		return "", err
	}
	label := strings.ToUpper(argStr(args, "label"))
	if label != "B" && label != "C" && label != "D" {
		return "", fmt.Errorf("label 须为 B / C / D")
	}
	path := filepath.Join(t.m.env.Config(tenantID).LogsDir(), tk.ID+"."+label+".log")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("日志不存在（该阶段可能未跑过）：%s", filepath.Base(path))
	}
	lines := strings.Split(string(data), "\n")
	n := argInt(args, "lines", 80)
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n"), nil
}

func (t *Tools) listTickets(ctx context.Context, tenantID, _ string, args map[string]any) (string, error) {
	prov := t.m.env.Provider(tenantID)
	if prov == nil {
		return "", fmt.Errorf("票源未配置")
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	q, _ := args["query"].(string)
	tickets, err := prov.List(cctx, q, 20)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, tc := range tickets {
		who := tc.Assignee
		if who == "" {
			who = "未分配"
		}
		fmt.Fprintf(&b, "%s [%s] (%s) %s", tc.ID, tc.Status, who, trim(tc.Title, 80))
		if len(tc.Labels) > 0 {
			fmt.Fprintf(&b, " | %s", strings.Join(tc.Labels, ","))
		}
		fmt.Fprintf(&b, " | %s\n", tc.URL)
	}
	if b.Len() == 0 {
		return "（无候选票）", nil
	}
	return b.String(), nil
}

func (t *Tools) getHealth(_ context.Context, tenantID, _ string, _ map[string]any) (string, error) {
	if t.m.health == nil {
		return "健康面板暂不可用", nil
	}
	return t.m.health(tenantID), nil
}

func (t *Tools) startTask(ctx context.Context, tenantID, actor string, args map[string]any) (string, error) {
	ticketID, repo := argStr(args, "ticket_id"), argStr(args, "repo")
	title := ""
	if prov := t.m.env.Provider(tenantID); prov != nil { // 标题快照 best-effort
		cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		if tc, err := prov.Get(cctx, ticketID); err == nil && tc != nil {
			title = tc.Title
		}
		cancel()
	}
	tk, err := t.m.orch.StartTask(tenantID, actor, ticketID, repo, title, "")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("已起任务 %s：%s → %s", taskRef(tk), ticketID, repo), nil
}

func (t *Tools) resumeReview(_ context.Context, tenantID, _ string, args map[string]any) (string, error) {
	tk, err := t.findTask(tenantID, argStr(args, "task"))
	if err != nil {
		return "", err
	}
	if err := t.m.orch.ResumeReview(tk.ID); err != nil {
		return "", err
	}
	return fmt.Sprintf("任务 %s 已回到审核闸口", taskRef(tk)), nil
}

func (t *Tools) resumePRReview(_ context.Context, tenantID, _ string, args map[string]any) (string, error) {
	tk, err := t.findTask(tenantID, argStr(args, "task"))
	if err != nil {
		return "", err
	}
	if err := t.m.orch.ResumePRReview(tk.ID); err != nil {
		return "", err
	}
	return fmt.Sprintf("任务 %s 已回到 PR 审查闸口", taskRef(tk)), nil
}

func (t *Tools) restartTask(_ context.Context, tenantID, _ string, args map[string]any) (string, error) {
	tk, err := t.findTask(tenantID, argStr(args, "task"))
	if err != nil {
		return "", err
	}
	stage := strings.ToUpper(argStr(args, "stage"))
	if err := t.m.orch.Restart(tk.ID, stage); err != nil {
		return "", err
	}
	return fmt.Sprintf("任务 %s 已从 %s 段重跑", taskRef(tk), stage), nil
}

func (t *Tools) deleteTask(_ context.Context, tenantID, _ string, args map[string]any) (string, error) {
	tk, err := t.findTask(tenantID, argStr(args, "task"))
	if err != nil {
		return "", err
	}
	if !tk.State.Terminal() {
		return "", fmt.Errorf("任务 %s 尚未结束（%s），不能删除", taskRef(tk), tk.State)
	}
	if err := t.m.st.DeleteTask(tk.ID); err != nil {
		return "", err
	}
	_ = t.m.bus.Publish(&store.Event{TenantID: tenantID, Type: "task.deleted", Level: "info",
		Message: fmt.Sprintf("任务 #%d（%s）已删除", tk.Seq, tk.SourceID)})
	return fmt.Sprintf("任务 %s 已删除", taskRef(tk)), nil
}

func (t *Tools) getTicket(ctx context.Context, tenantID, _ string, args map[string]any) (string, error) {
	// 两种寻址：直接票号（无需起过任务），或任务引用（读该任务对应的票）。
	id := strings.TrimSpace(argStr(args, "ticket"))
	if id == "" {
		tk, err := t.findTask(tenantID, argStr(args, "task"))
		if err != nil {
			return "", fmt.Errorf("须传 ticket=票号 或 task=任务引用（%v）", err)
		}
		id = tk.SourceID
	}
	prov := t.m.env.Provider(tenantID)
	if prov == nil {
		return "", fmt.Errorf("票源未配置")
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	tc, err := prov.Get(cctx, id)
	if err != nil {
		return "", err
	}
	who := tc.Assignee
	if who == "" {
		who = "未分配"
	}
	return fmt.Sprintf("票号: %s\n标题: %s\n状态: %s\n经办人: %s\nURL: %s\n\n%s",
		tc.ID, tc.Title, tc.Status, who, tc.URL, trim(tc.Body, 6000)), nil
}

func (t *Tools) getIssueTool(ctx context.Context, tenantID, _ string, args map[string]any) (string, error) {
	tk, err := t.findTask(tenantID, argStr(args, "task"))
	if err != nil {
		return "", err
	}
	if tk.IssueNum == 0 {
		return "", fmt.Errorf("任务 %s 尚无 Issue（B 段未产出）", taskRef(tk))
	}
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	repo := t.m.env.Config(tenantID).Repos[tk.Repo].GitHub
	iss, err := t.m.env.Github(tenantID, "").GetIssue(cctx, repo, tk.IssueNum)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Issue #%d（%s）\n标题: %s\n\n%s", tk.IssueNum, tk.IssueURL, iss.Title, trim(iss.Body, 8000)), nil
}

func (t *Tools) getPR(ctx context.Context, tenantID, _ string, args map[string]any) (string, error) {
	tk, err := t.findTask(tenantID, argStr(args, "task"))
	if err != nil {
		return "", err
	}
	if tk.PRNum == 0 {
		return "", fmt.Errorf("任务 %s 尚无 PR", taskRef(tk))
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	repo := t.m.env.Config(tenantID).Repos[tk.Repo].GitHub
	gh := t.m.env.Github(tenantID, "")
	title, body, err := gh.PRView(cctx, repo, tk.PRNum)
	if err != nil {
		return "", err
	}
	diff, err := gh.PRDiff(cctx, repo, tk.PRNum)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("PR #%d（%s）\n标题: %s\n\n%s\n\n===== DIFF =====\n%s",
		tk.PRNum, tk.PRURL, title, trim(body, 4000), trim(diff, 16000)), nil
}

func (t *Tools) updateIssue(ctx context.Context, tenantID, actor string, args map[string]any) (string, error) {
	tk, err := t.findTask(tenantID, argStr(args, "task"))
	if err != nil {
		return "", err
	}
	if tk.IssueNum == 0 {
		return "", fmt.Errorf("任务 %s 尚无 Issue，无从修改", taskRef(tk))
	}
	title, body := argStr(args, "title"), argStr(args, "body")
	if title == "" || body == "" {
		return "", fmt.Errorf("title 与 body 都必填（先 get_issue 取原文，改后整体提交）")
	}
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	repo := t.m.env.Config(tenantID).Repos[tk.Repo].GitHub
	if err := t.m.env.Github(tenantID, actor).UpdateIssue(cctx, repo, tk.IssueNum, title, body); err != nil {
		return "", err
	}
	return fmt.Sprintf("任务 %s 的 Issue #%d 已更新（标题与正文已写回 GitHub）", taskRef(tk), tk.IssueNum), nil
}

func (t *Tools) cancelTask(_ context.Context, tenantID, _ string, args map[string]any) (string, error) {
	tk, err := t.findTask(tenantID, argStr(args, "task"))
	if err != nil {
		return "", err
	}
	if err := t.m.orch.Cancel(tk.ID); err != nil {
		return "", err
	}
	return fmt.Sprintf("已取消任务 %s", taskRef(tk)), nil
}

func (t *Tools) approveIssue(ctx context.Context, tenantID, _ string, args map[string]any) (string, error) {
	tk, err := t.findTask(tenantID, argStr(args, "task"))
	if err != nil {
		return "", err
	}
	if err := t.m.orch.Approve(ctx, tk.ID); err != nil {
		return "", err
	}
	return fmt.Sprintf("任务 %s 人审已通过，实装（C）已启动", taskRef(tk)), nil
}

func (t *Tools) escalateReview(_ context.Context, tenantID, _ string, args map[string]any) (string, error) {
	tk, err := t.findTask(tenantID, argStr(args, "task"))
	if err != nil {
		return "", err
	}
	reason := argStr(args, "reason")
	if reason == "" {
		return "", fmt.Errorf("必须说明需要人工的原因")
	}
	if err := t.m.orch.EscalateReview(tk.ID, reason); err != nil {
		return "", err
	}
	return fmt.Sprintf("任务 %s 已上报人工审核（Slack 已提醒）", taskRef(tk)), nil
}

func (t *Tools) rejectIssue(ctx context.Context, tenantID, _ string, args map[string]any) (string, error) {
	tk, err := t.findTask(tenantID, argStr(args, "task"))
	if err != nil {
		return "", err
	}
	if err := t.m.orch.Reject(ctx, tk.ID, argStr(args, "reason")); err != nil {
		return "", err
	}
	return fmt.Sprintf("任务 %s 已打回", taskRef(tk)), nil
}

func (t *Tools) approvePR(ctx context.Context, tenantID, _ string, args map[string]any) (string, error) {
	tk, err := t.findTask(tenantID, argStr(args, "task"))
	if err != nil {
		return "", err
	}
	if err := t.m.orch.ApprovePR(ctx, tk.ID); err != nil {
		return "", err
	}
	return fmt.Sprintf("任务 %s 的 PR 审查已确认通过，任务完成", taskRef(tk)), nil
}

func (t *Tools) requestRevise(ctx context.Context, tenantID, _ string, args map[string]any) (string, error) {
	tk, err := t.findTask(tenantID, argStr(args, "task"))
	if err != nil {
		return "", err
	}
	if err := t.m.orch.RequestRevise(ctx, tk.ID); err != nil {
		return "", err
	}
	return fmt.Sprintf("已对任务 %s 触发一轮 PR 修订", taskRef(tk)), nil
}

// findTask 按引用（#编号 / 编号 / 票号 / uuid）在本租户内找任务；同票多任务取最新。
func (t *Tools) findTask(tenantID, ref string) (*store.Task, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, fmt.Errorf("缺少任务引用")
	}
	tasks, err := t.m.st.ListTasksByTenant(tenantID)
	if err != nil {
		return nil, err
	}
	seq, _ := strconv.Atoi(strings.TrimPrefix(ref, "#"))
	for _, tk := range tasks { // ListTasksByTenant 按创建时间倒序，命中即最新
		if tk.ID == ref || (seq > 0 && tk.Seq == seq) || strings.EqualFold(tk.SourceID, ref) {
			return tk, nil
		}
	}
	return nil, fmt.Errorf("找不到任务：%q（可用 list_tasks 查看现有任务）", ref)
}

// --- schema / 参数小工具 ---

func obj(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
func num(desc string) map[string]any { return map[string]any{"type": "number", "description": desc} }

func argStr(args map[string]any, key string) string {
	v, _ := args[key].(string)
	return strings.TrimSpace(v)
}

func argInt(args map[string]any, key string, def int) int {
	if v, ok := args[key].(float64); ok && int(v) > 0 {
		return int(v)
	}
	return def
}
