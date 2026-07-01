package events

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/ryuclub/ai-workflow/internal/core/store"
)

// SlackSink 把任务级/告警事件转发到 Slack（一种 sink，非特例）。
type SlackSink struct {
	url    string
	client *http.Client
}

// NewSlackSink 构造 Slack sink；url 为空时 Deliver 直接跳过。
func NewSlackSink(url string) *SlackSink {
	return &SlackSink{url: url, client: &http.Client{Timeout: 10 * time.Second}}
}

// Deliver 仅转发任务级事件与 warn/error，避免节点级日志刷屏。非阻塞。
func (s *SlackSink) Deliver(ev store.Event) {
	if s.url == "" {
		return
	}
	if !strings.HasPrefix(ev.Type, "task.") && ev.Level != "warn" && ev.Level != "error" {
		return
	}
	go func() {
		body, _ := json.Marshal(map[string]string{
			"text": "[" + ev.Type + "] " + ev.Message,
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
