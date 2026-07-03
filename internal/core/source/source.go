// Package source 抽象「票源」：JIRA / Linear 等都实现 Provider，
// 上层（编排、列表、缓存）只认归一化的 Ticket，不认具体源。
// 当前 JIRA 与 Linear 二选一（配置选活跃源，非同时并存）。
package source

import "context"

// Ticket 是跨源归一化的工单。
type Ticket struct {
	Source   string   `json:"source"` // jira / linear
	ID       string   `json:"id"`     // 源内标识：PROJ-3252 / ENG-12
	Title    string   `json:"title"`
	Body     string   `json:"body,omitempty"` // 正文（尽量归一为可读文本/markdown）
	Status   string   `json:"status"`
	URL      string   `json:"url"`
	Assignee string   `json:"assignee,omitempty"`
	Labels   []string `json:"labels,omitempty"`
}

// Provider 是一个票源的统一接口。
type Provider interface {
	// Name 返回源标识（jira / linear），写入 Task.Source。
	Name() string
	// List 按源相关的查询拉取候选票（query 语义由各源定义；空串用 DefaultQuery）。
	List(ctx context.Context, query string, max int) ([]Ticket, error)
	// Get 按源内标识读单票。
	Get(ctx context.Context, id string) (*Ticket, error)
	// ValidateID 校验源内标识格式。
	ValidateID(id string) bool
	// DefaultQuery 是列表的默认查询。
	DefaultQuery() string
	// Transition 把工单流转到指定状态（name 为源的流转/状态名）。不支持则可返回 nil。
	Transition(ctx context.Context, id, name string) error
	// Transitions 返回该票当前可用的流转目标名。JIRA=工作流当前合法 transitions；
	// Linear=团队工作流状态集（任意可达）。
	Transitions(ctx context.Context, id string) ([]string, error)
}
