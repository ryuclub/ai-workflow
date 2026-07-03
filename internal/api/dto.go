package api

import (
	"github.com/ryuclub/ai-workflow/internal/core/source"
	"github.com/ryuclub/ai-workflow/internal/core/store"
)

// startTaskReq 是创建任务的请求体。source_id 为活跃源的工单标识（PROJ-3252 / ENG-12）。
type startTaskReq struct {
	SourceID string `json:"source_id" binding:"required"`
	Repo     string `json:"repo" binding:"required"`
	Title    string `json:"title"`
}

// ticketItem 是候选票列表项：归一化票 + 本地增强（建议仓 / 已起任务及其状态）。
type ticketItem struct {
	source.Ticket
	SuggestedRepo     string `json:"suggested_repo,omitempty"`
	ExistingTaskID    string `json:"existing_task_id,omitempty"`
	ExistingTaskState string `json:"existing_task_state,omitempty"` // 用于区分进行中 vs 已结束（可重开）
}

// rejectReq 是人审打回的请求体。
type rejectReq struct {
	Reason string `json:"reason"`
}

// editIssueReq 是就地编辑 Issue 的请求体。
type editIssueReq struct {
	Title string `json:"title" binding:"required"`
	Body  string `json:"body" binding:"required"`
}

// taskDetail 是任务详情（任务 + 各节点运行态），供前端画流水线。
type taskDetail struct {
	Task     *store.Task      `json:"task"`
	NodeRuns []*store.NodeRun `json:"node_runs"`
}

// ingestReq 是 skill 经 /internal 回传的结构化阶段事件。
type ingestReq struct {
	Phase    string `json:"phase"`   // 如 B.investigate / C.test（映射到节点）
	Status   string `json:"status"`  // start | ok | fail | info
	Level    string `json:"level"`   // info | warn | error
	Message  string `json:"message"` // 日志正文
	RunGen   int    `json:"run_gen,omitempty"` // 运行代数（emit-event.sh 携带；与任务当前代数不符则丢弃）
	IssueNum int    `json:"issue_num,omitempty"`
	IssueURL string `json:"issue_url,omitempty"`
	PRURL    string `json:"pr_url,omitempty"`
}
