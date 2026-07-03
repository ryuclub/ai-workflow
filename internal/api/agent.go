package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/ryuclub/ai-workflow/internal/core/store"
)

// GET /api/v1/agent/messages?since=<id>&limit=<n>&before=<id> — 聊天历史。
// since=增量；before=向上翻页（取 id<before 的最近 limit 条）；首屏默认尾部 60 条。
func (s *Server) agentMessages(c *gin.Context) {
	since, _ := strconv.ParseInt(c.Query("since"), 10, 64)
	before, _ := strconv.ParseInt(c.Query("before"), 10, 64)
	limit := 0
	if since == 0 {
		limit = 60 // 首屏取尾部最近 60 条（更早的向上翻页懒加载）
	}
	if v := c.Query("limit"); v != "" {
		limit, _ = strconv.Atoi(v)
	}
	var msgs []*store.AgentMessage
	var err error
	if before > 0 {
		msgs, err = s.st.ListAgentMessagesBefore(c.GetString(ctxTenantID), before, limit)
	} else {
		msgs, err = s.st.ListAgentMessages(c.GetString(ctxTenantID), since, limit)
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	cfg := s.cfg(c)
	model := cfg.AgentModel()
	if model == "" {
		model = "默认"
	}
	c.JSON(http.StatusOK, gin.H{"messages": msgs, "enabled": s.mgr.Enabled(c.GetString(ctxTenantID)), "model": model})
}

// POST /api/v1/agent/messages — 用户在聊天窗发言（可带附件引用）。
func (s *Server) agentSend(c *gin.Context) {
	tenantID := c.GetString(ctxTenantID)
	if !s.mgr.Enabled(tenantID) {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "调度 Agent 已停用（AGENT_ENABLED=false）"})
		return
	}
	var req struct {
		Content     string   `json:"content"`
		TaskID      string   `json:"task_id"`     // 任务过滤视图下发送：消息与塔台回复归属该任务
		Attachments []string `json:"attachments"` // 已上传附件 id 列表（agentUpload 返回）
	}
	if err := c.ShouldBindJSON(&req); err != nil || (strings.TrimSpace(req.Content) == "" && len(req.Attachments) == 0) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "content 与附件不能都为空"})
		return
	}
	content := strings.TrimSpace(req.Content)
	// 附件：正文追加 wf-upload:// 标记（前端渲染成图片/链接）；
	// 模型侧追加本地路径说明（塔台用 Read 工具查看，图片直接进视觉）。
	var note strings.Builder
	dir := s.cfg(c).UploadsDir()
	for _, id := range req.Attachments {
		id = filepath.Base(strings.TrimSpace(id)) // 防路径穿越
		if id == "" || id == "." {
			continue
		}
		abs := filepath.Join(dir, id)
		if _, err := os.Stat(abs); err != nil {
			continue // 未上传成功的引用直接忽略
		}
		if isImageName(id) {
			content += "\n\n![" + id + "](wf-upload://" + id + ")"
		} else {
			content += "\n\n📎 [" + id + "](wf-upload://" + id + ")"
		}
		fmt.Fprintf(&note, "\n[用户上传了附件：%s —— 用 Read 工具查看（图片可直接看）]", abs)
	}
	msg, err := s.mgr.Send(tenantID, c.GetString(ctxUserID), strings.TrimSpace(req.TaskID), content, note.String())
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error(), "message": msg})
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"message": msg})
}

// isImageName 按扩展名粗判是否图片（内联预览用）。
func isImageName(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".svg":
		return true
	}
	return false
}

// POST /api/v1/agent/uploads — 聊天附件上传（multipart file 字段；上限 15MB）。
func (s *Server) agentUpload(c *gin.Context) {
	fh, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "缺少 file 字段"})
		return
	}
	if fh.Size > 15<<20 {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "附件超过 15MB 上限"})
		return
	}
	dir := s.cfg(c).UploadsDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// 文件名：短随机前缀 + 净化原名（保留扩展名以便类型判断/Read 识别）
	name := strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|', ' ':
			return '_'
		}
		return r
	}, filepath.Base(fh.Filename))
	id := uuid.NewString()[:8] + "-" + name
	if err := c.SaveUploadedFile(fh, filepath.Join(dir, id)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": id, "name": name, "size": fh.Size, "image": isImageName(id)})
}

// GET /api/v1/agent/uploads/:id — 取聊天附件（图片内联预览/文件下载）。
func (s *Server) agentUploadGet(c *gin.Context) {
	id := filepath.Base(c.Param("id")) // 防路径穿越
	abs := filepath.Join(s.cfg(c).UploadsDir(), id)
	if _, err := os.Stat(abs); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "附件不存在"})
		return
	}
	c.File(abs)
}

// GET /api/v1/agent/stream — 租户级聊天 SSE：agent.message（落库消息，按游标拉齐）+ agent.delta（打字机增量）。
func (s *Server) agentStream(c *gin.Context) {
	tenantID := c.GetString(ctxTenantID)
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

	subID, sig := s.mgr.SubscribeChat(tenantID)
	defer s.mgr.UnsubscribeChat(tenantID, subID)

	send := func() {
		msgs, err := s.st.ListAgentMessages(tenantID, lastID, 0)
		if err != nil {
			return
		}
		for _, m := range msgs {
			b, _ := json.Marshal(m)
			fmt.Fprintf(c.Writer, "id: %d\nevent: agent.message\ndata: %s\n\n", m.ID, b)
			lastID = m.ID
		}
		flusher.Flush()
	}

	send() // 补发游标之后的历史
	heartbeat := time.NewTicker(20 * time.Second)
	defer heartbeat.Stop()
	ctx := c.Request.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case sg := <-sig:
			if sg.Delta != "" {
				b, _ := json.Marshal(gin.H{"text": sg.Delta})
				fmt.Fprintf(c.Writer, "event: agent.delta\ndata: %s\n\n", b)
				flusher.Flush()
			} else {
				send()
			}
		case <-heartbeat.C:
			fmt.Fprint(c.Writer, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}

// GET /api/v1/agent/actions?status=pending — 动作列表（确认卡）。
func (s *Server) agentActions(c *gin.Context) {
	acts, err := s.st.ListAgentActions(c.GetString(ctxTenantID), c.Query("status"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"actions": acts})
}

// POST /api/v1/agent/actions/:id/confirm — 人工确认并执行待确认动作。
func (s *Server) agentConfirmAction(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	act, err := s.mgr.Tools().Confirm(c.Request.Context(), id, c.GetString(ctxTenantID), c.GetString(ctxUserID))
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"action": act})
}

// POST /api/v1/agent/actions/:id/deny — 人工拒绝待确认动作。
func (s *Server) agentDenyAction(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var req struct {
		Reason string `json:"reason"`
	}
	_ = c.ShouldBindJSON(&req)
	act, err := s.mgr.Tools().Deny(id, c.GetString(ctxTenantID), c.GetString(ctxUserID), req.Reason)
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"action": act})
}

// GET /api/v1/events/stream — 租户级全局事件 SSE（任务列表去轮询用）。
// 无 since 时从「当下」开始（不回放历史）：列表刷新只关心新事件。
func (s *Server) streamTenantEvents(c *gin.Context) {
	tenantID := c.GetString(ctxTenantID)
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
	} else {
		lastID, _ = s.st.MaxEventID()
	}

	subID, nudge := s.bus.SubscribeTenant(tenantID)
	defer s.bus.UnsubscribeTenant(tenantID, subID)

	send := func() {
		evs, err := s.st.ListTenantEvents(tenantID, lastID, 0)
		if err != nil {
			return
		}
		for _, ev := range evs {
			b, _ := json.Marshal(ev)
			fmt.Fprintf(c.Writer, "id: %d\nevent: %s\ndata: %s\n\n", ev.ID, ev.Type, b)
			lastID = ev.ID
		}
		flusher.Flush()
	}

	send()
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

// GET /api/v1/stream — 合流 SSE（每页一条连接）：租户事件（按事件名分发）+
// agent.message / agent.delta（塔台聊天）+ task.output / task.output.end（全部任务直播）。
// 游标：?ev_since / ?chat_since，缺省从「当下」开始（历史由客户端经 REST 拉取）。
func (s *Server) streamHub(c *gin.Context) {
	tenantID := c.GetString(ctxTenantID)
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "streaming unsupported"})
		return
	}
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")

	evSince, _ := strconv.ParseInt(c.Query("ev_since"), 10, 64)
	if evSince == 0 {
		evSince, _ = s.st.MaxEventID()
	}
	chatSince, _ := strconv.ParseInt(c.Query("chat_since"), 10, 64)
	if chatSince == 0 {
		if last, err := s.st.ListAgentMessages(tenantID, 0, 1); err == nil && len(last) > 0 {
			chatSince = last[0].ID
		}
	}

	busID, busCh := s.bus.SubscribeTenant(tenantID)
	defer s.bus.UnsubscribeTenant(tenantID, busID)
	chatID, chatCh := s.mgr.SubscribeChat(tenantID)
	defer s.mgr.UnsubscribeChat(tenantID, chatID)
	liveID, liveCh := s.live.SubscribeTenantOutput(tenantID)
	defer s.live.UnsubscribeTenantOutput(tenantID, liveID)

	emit := func(event string, v any) {
		b, _ := json.Marshal(v)
		fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", event, b)
	}
	pumpEvents := func() {
		evs, err := s.st.ListTenantEvents(tenantID, evSince, 0)
		if err != nil {
			return
		}
		for _, ev := range evs {
			emit(ev.Type, ev)
			evSince = ev.ID
		}
		flusher.Flush()
	}
	pumpChat := func() {
		msgs, err := s.st.ListAgentMessages(tenantID, chatSince, 0)
		if err != nil {
			return
		}
		for _, m := range msgs {
			emit("agent.message", m)
			chatSince = m.ID
		}
		flusher.Flush()
	}
	offsets := map[string]int{} // taskID → 已推送的直播偏移
	ended := map[string]bool{}  // taskID → 已发收尾（宽限期内 done 流仍在 Active 列表，防重复）
	pumpOutput := func() {
		active := map[string]bool{}
		for _, ref := range s.live.ActiveOutputs(tenantID) {
			if ended[ref.TaskID] {
				if ref.Done {
					continue
				}
				delete(ended, ref.TaskID) // 同任务重跑开了新直播流：重新转播
			}
			active[ref.TaskID] = true
			chunk, next, done, ok := s.live.Read(ref.TaskID, offsets[ref.TaskID])
			if ok && next < offsets[ref.TaskID] { // 新直播流比旧偏移短 → 任务重跑，偏移归零重读
				offsets[ref.TaskID] = 0
				chunk, next, done, ok = s.live.Read(ref.TaskID, 0)
			}
			if ok && len(chunk) > 0 {
				emit("task.output", gin.H{"task_id": ref.TaskID, "label": ref.Label, "text": string(chunk)})
				offsets[ref.TaskID] = next
			}
			if done || !ok {
				emit("task.output.end", gin.H{"task_id": ref.TaskID})
				delete(offsets, ref.TaskID)
				ended[ref.TaskID] = true
			}
		}
		// 流被回收（不再出现在 Active 列表）→ 补发收尾
		for taskID := range offsets {
			if !active[taskID] {
				emit("task.output.end", gin.H{"task_id": taskID})
				delete(offsets, taskID)
				ended[taskID] = true
			}
		}
		flusher.Flush()
	}

	pumpOutput() // 已在跑的任务：先推快照
	heartbeat := time.NewTicker(20 * time.Second)
	defer heartbeat.Stop()
	ctx := c.Request.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-busCh:
			pumpEvents()
		case sg := <-chatCh:
			if sg.Delta != "" {
				emit("agent.delta", gin.H{"text": sg.Delta})
				flusher.Flush()
			} else {
				pumpChat()
			}
		case <-liveCh:
			pumpOutput()
		case <-heartbeat.C:
			fmt.Fprint(c.Writer, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}

// GET /api/v1/tasks/:id/output/stream — 任务运行中 claude 输出的实时直播（SSE）。
// 只服务运行中的任务：未在跑（或已结束）立即发 output.end 收尾；历史输出走落盘日志。
func (s *Server) streamTaskOutput(c *gin.Context) {
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

	endEvent := func() {
		fmt.Fprint(c.Writer, "event: output.end\ndata: {}\n\n")
		flusher.Flush()
	}
	// 直播可能尚未开播（任务排队 / worktree 准备中）：只要任务仍会跑就等它开播，
	// 不能直接收尾——否则前端在 claude 真正启动前订阅会拿到假的 output.end。
	subID, nudge, label, ok := s.live.Subscribe(t.ID, t.TenantID)
	for !ok {
		fresh, err := s.st.GetTask(t.ID)
		if err != nil || fresh == nil {
			endEvent()
			return
		}
		switch fresh.State {
		case store.StateQueued, store.StateRunningB, store.StateRunningC, store.StateRunningD:
			// 仍在跑/将要跑：等开播
		default:
			endEvent()
			return
		}
		select {
		case <-c.Request.Context().Done():
			return
		case <-time.After(2 * time.Second):
		}
		fmt.Fprint(c.Writer, ": waiting\n\n")
		flusher.Flush()
		subID, nudge, label, ok = s.live.Subscribe(t.ID, t.TenantID)
	}
	defer s.live.Unsubscribe(t.ID, subID)

	offset := 0
	send := func() (done bool) {
		chunk, next, done, ok := s.live.Read(t.ID, offset)
		if !ok {
			return true
		}
		offset = next
		if len(chunk) > 0 {
			b, _ := json.Marshal(gin.H{"label": label, "text": string(chunk)})
			fmt.Fprintf(c.Writer, "event: output\ndata: %s\n\n", b)
			flusher.Flush()
		}
		return done
	}

	if send() {
		endEvent()
		return
	}
	heartbeat := time.NewTicker(20 * time.Second)
	defer heartbeat.Stop()
	ctx := c.Request.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-nudge:
			if send() {
				endEvent()
				return
			}
		case <-heartbeat.C:
			fmt.Fprint(c.Writer, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}

// GET /api/v1/tickets/:id/transitions — 该票当前可用的状态流转目标名。
// JIRA 只回工作流合法项（部分状态不可直达是 JIRA 工作流的天然约束）；Linear 回团队状态集。
func (s *Server) ticketTransitions(c *gin.Context) {
	p := s.srcReady(c)
	if p == nil {
		return
	}
	id := c.Param("id")
	if !p.ValidateID(id) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "非法工单标识"})
		return
	}
	ctx, cancel := contextWithTimeout(c, 30*time.Second)
	defer cancel()
	names, err := p.Transitions(ctx, id)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"transitions": names})
}

// POST /api/v1/tickets/:id/transition — 把工单流转到指定状态。
func (s *Server) ticketTransition(c *gin.Context) {
	p := s.srcReady(c)
	if p == nil {
		return
	}
	id := c.Param("id")
	if !p.ValidateID(id) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "非法工单标识"})
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Name) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "缺少目标状态名"})
		return
	}
	ctx, cancel := contextWithTimeout(c, 30*time.Second)
	defer cancel()
	if err := p.Transition(ctx, id, strings.TrimSpace(req.Name)); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// tenantHealthSnapshot 汇总某租户所有已缓存的健康探测结果（Agent get_health 工具用）。
func (s *Server) tenantHealthSnapshot(tenantID string) string {
	s.health.mu.Lock()
	keys := make([]string, 0)
	states := make([]*healthState, 0)
	for key, st := range s.health.byKey {
		if strings.HasPrefix(key, tenantID+"|") {
			keys = append(keys, strings.TrimPrefix(key, tenantID+"|"))
			states = append(states, st)
		}
	}
	s.health.mu.Unlock()

	var b strings.Builder
	for i, st := range states {
		for _, ck := range st.snapshot() {
			status := "OK"
			if !ck.OK {
				status = "异常"
			}
			fmt.Fprintf(&b, "[用户 %s] %s: %s（%s）\n", keys[i], ck.Name, status, ck.Detail)
		}
	}
	if b.Len() == 0 {
		return "暂无健康探测缓存（有人打开 Dashboard 触发健康检查后可查）"
	}
	return b.String()
}
