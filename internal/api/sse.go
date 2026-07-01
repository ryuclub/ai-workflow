package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

// contextWithTimeout 基于请求上下文派生带超时的 ctx（子进程调用用）。
func contextWithTimeout(c *gin.Context, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(c.Request.Context(), d)
}

// GET /api/v1/tasks/:id/events — SSE 实时进度。
// store 为唯一真相：先补发历史(Last-Event-ID 之后)，再随 nudge 增量拉取，保证不丢事件。
func (s *Server) streamEvents(c *gin.Context) {
	t := s.mustTask(c)
	if t == nil {
		return
	}
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "streaming unsupported"})
		return
	}
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")

	var lastID int64
	if v := c.GetHeader("Last-Event-ID"); v != "" {
		lastID, _ = strconv.ParseInt(v, 10, 64)
	} else if v := c.Query("since"); v != "" {
		lastID, _ = strconv.ParseInt(v, 10, 64)
	}

	subID, nudge := s.bus.Subscribe(t.ID)
	defer s.bus.Unsubscribe(t.ID, subID)

	send := func() bool {
		evs, err := s.st.ListEvents(t.ID, lastID)
		if err != nil {
			return true // 暂时性错误，保持连接
		}
		for _, ev := range evs {
			b, _ := json.Marshal(ev)
			fmt.Fprintf(c.Writer, "id: %d\nevent: %s\ndata: %s\n\n", ev.ID, ev.Type, b)
			lastID = ev.ID
		}
		flusher.Flush()
		return true
	}

	send() // 首次补发历史
	heartbeat := time.NewTicker(20 * time.Second)
	defer heartbeat.Stop()
	ctx := c.Request.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-nudge:
			send()
		case <-heartbeat.C:
			fmt.Fprint(c.Writer, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}
