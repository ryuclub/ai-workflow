// Package store 定义任务/节点运行/事件的持久化模型与接口。
// 接口隔离实现：现以 SQLite 落地，量大可换 Postgres，不动上层。
package store

import "time"

// TaskState 是任务在状态机中的位置。
type TaskState string

const (
	StateQueued           TaskState = "queued"             // 已创建，待跑 B
	StateRunningB         TaskState = "running_b"          // 正在调查→建 Issue
	StateAwaitingReview   TaskState = "awaiting_review"    // 等人审 Issue（gate）
	StateRunningC         TaskState = "running_c"          // 正在实装→PR
	StateAwaitingPRReview TaskState = "awaiting_pr_review" // 出 PR，等人审查（gate，可多轮）
	StateRunningD         TaskState = "running_d"          // 正在按 review 意见修订 PR
	StateDone             TaskState = "done"               // PR 已通过（approved），完成
	StateFailed           TaskState = "failed"             // 硬关卡失败
	StateAdjudication     TaskState = "adjudication"       // 待裁决（异常停：失败/遇阻/超轮次，需人工）
	StateRejected         TaskState = "rejected"           // 人审打回
	StateCanceled         TaskState = "canceled"           // 人工取消
	StateSkipped          TaskState = "skipped"            // 正常停：判定无需处理（非错误）
)

// Terminal 表示任务已到终态，不可再取消/流转。
func (s TaskState) Terminal() bool {
	switch s {
	case StateDone, StateFailed, StateAdjudication, StateRejected, StateCanceled, StateSkipped:
		return true
	}
	return false
}

// NodeState 是单个流水线节点的运行态。
type NodeState string

const (
	NodePending NodeState = "pending"
	NodeRunning NodeState = "running"
	NodeOK      NodeState = "ok"
	NodeFail    NodeState = "fail"
	NodeWaiting NodeState = "waiting" // 等人工
)

// Task 是一次流水线执行。
type Task struct {
	ID           string    `json:"id"`
	Seq          int       `json:"seq"`       // 人类可读自增编号（#1、#2…），便于口头引用
	Source       string    `json:"source"`    // jira / linear
	SourceID     string    `json:"source_id"` // 源内标识：PROJ-3252 / ENG-12
	Title        string    `json:"title"`     // 起任务时的票标题快照（列表/详情展示用）
	Repo         string    `json:"repo"`
	PipelineID   string    `json:"pipeline_id"`
	State        TaskState `json:"state"`
	IssueURL     string    `json:"issue_url,omitempty"`
	IssueNum     int       `json:"issue_num,omitempty"`
	PRURL        string    `json:"pr_url,omitempty"`
	PRNum        int       `json:"pr_num,omitempty"`        // PR 编号（进入 PR 审查闸口 / 修订循环时用）
	ReviewRound  int       `json:"review_round,omitempty"`  // 已进行的修订轮次（PR 审查循环计数）
	ReviewCursor string    `json:"review_cursor,omitempty"` // 上次已处理的 review 提交时间（RFC3339，幂等游标）
	IdemKey      string    `json:"-"`
	Error        string    `json:"error,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// NodeRun 是某任务某节点的运行记录。
type NodeRun struct {
	TaskID    string     `json:"task_id"`
	NodeID    string     `json:"node_id"`
	State     NodeState  `json:"state"`
	StartedAt *time.Time `json:"started_at,omitempty"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
}

// Event 是一条进度/日志事件（对外契约的一部分）。
type Event struct {
	ID      int64     `json:"id"`
	TaskID  string    `json:"task_id"`
	NodeID  string    `json:"node_id,omitempty"`
	Type    string    `json:"type"`  // task.created / node.started / node.completed / ...
	Level   string    `json:"level"` // info / warn / error
	Message string    `json:"message"`
	TS      time.Time `json:"ts"`
}

// Store 是持久化接口。所有方法应并发安全。
type Store interface {
	CreateTask(t *Task) error
	GetTask(id string) (*Task, error)
	ListTasks() ([]*Task, error)
	UpdateTask(t *Task) error
	FindByIdem(idem string) (*Task, error)
	FindBySource(source, sourceID string) (*Task, error) // 最近一个，供列表去重标记

	UpsertNodeRun(nr *NodeRun) error
	ListNodeRuns(taskID string) ([]*NodeRun, error)

	AppendEvent(ev *Event) error
	ListEvents(taskID string, sinceID int64) ([]*Event, error)

	// ReconcileInterrupted 把上次进程残留的运行中任务标为待裁决（启动时调用）。
	ReconcileInterrupted() (int64, error)

	Close() error
}
