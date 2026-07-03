package events

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ryuclub/ai-workflow/internal/core/store"
)

// TaskLookup 供 sink 回查事件归属（任务/发起人），由 *store.SQLite 实现。
type TaskLookup interface {
	GetTask(id string) (*store.Task, error)
	GetUserByID(id string) (*store.User, error)
}

// SlackSink 把任务级/告警事件转发到 Slack（一种 sink，非特例）。
type SlackSink struct {
	url    string
	lk     TaskLookup
	client *http.Client
	// autoReview 报告某租户是否启用塔台自动审核（可 nil）：启用时抑制「等待审核」
	// 的原始提醒——塔台先审，判定需人工时才经 task.review_escalated 发提醒。
	autoReview func(tenantID string) bool
}

// NewSlackSink 构造 Slack sink；url 为空时 Deliver 直接跳过。lk / autoReview 可为 nil。
func NewSlackSink(url string, lk TaskLookup, autoReview func(tenantID string) bool) *SlackSink {
	return &SlackSink{url: url, lk: lk, client: &http.Client{Timeout: 10 * time.Second}, autoReview: autoReview}
}

// Deliver 仅转发任务级事件与 warn/error，避免节点级日志刷屏。非阻塞。
// 多人并行时频道消息交错，故每条带归属前缀 [#编号 票号 · 仓]；task.created 附发起人。
func (s *SlackSink) Deliver(ev store.Event) {
	if s.url == "" {
		return
	}
	if !strings.HasPrefix(ev.Type, "task.") && ev.Level != "warn" && ev.Level != "error" {
		return
	}
	if ev.Type == "task.awaiting_review" && s.autoReview != nil && s.autoReview(ev.TenantID) {
		return // 塔台自动审核中：先不惊动人，需人工时另有 task.review_escalated
	}
	go func() {
		body, _ := json.Marshal(map[string]string{
			"text": s.format(ev),
		})
		req, err := http.NewRequest(http.MethodPost, s.url, bytes.NewReader(body))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json")
		if resp, err := s.client.Do(req); err == nil {
			_ = resp.Body.Close()
		}
	}()
}

// format 生成带归属标识的消息文本（回查失败退回原格式）。
func (s *SlackSink) format(ev store.Event) string {
	base := "[" + ev.Type + "] " + ev.Message
	if ev.Type == "agent.notice" || ev.Type == "agent.action" {
		return "[塔台] " + base
	}
	if s.lk == nil || ev.TaskID == "" {
		return base
	}
	t, err := s.lk.GetTask(ev.TaskID)
	if err != nil || t == nil {
		return base
	}
	prefix := fmt.Sprintf("[#%d %s · %s] ", t.Seq, t.SourceID, t.Repo)
	if ev.Type == "task.created" && t.CreatedBy != "" {
		if u, err := s.lk.GetUserByID(t.CreatedBy); err == nil && u != nil {
			return prefix + base + "（发起人：" + u.Email + "）"
		}
	}
	return prefix + base
}
