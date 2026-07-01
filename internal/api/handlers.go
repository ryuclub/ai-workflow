package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/ryuclub/ai-workflow/internal/core/store"
	"github.com/gin-gonic/gin"
)

// GET /api/v1/repos — 登记仓列表（仅启用，供候选票选仓下拉/路由）。
func (s *Server) listRepos(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"default": s.cfg().Default, "repos": s.cfg().EnabledRepoList()})
}

// GET /api/v1/pipeline — 内置流水线定义（供前端画节点图）。
func (s *Server) getPipeline(c *gin.Context) {
	c.JSON(http.StatusOK, s.orch.Pipeline())
}

// GET /api/v1/source — 活跃票源信息（前端据此适配）。
func (s *Server) getSource(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"source": s.src().Name(), "default_query": s.src().DefaultQuery()})
}

// GET /api/v1/tickets?query=&max= — 候选票列表（入口），跨源 + 增强。
func (s *Server) listTickets(c *gin.Context) {
	max := 50
	if v := c.Query("max"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			max = n
		}
	}
	ctx, cancel := contextWithTimeout(c, 30*time.Second)
	defer cancel()
	tickets, err := s.src().List(ctx, c.Query("query"), max)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	items := make([]ticketItem, 0, len(tickets))
	for _, t := range tickets {
		it := ticketItem{Ticket: t, SuggestedRepo: s.cfg().SuggestRepo(t.Title)}
		if existing, _ := s.st.FindBySource(t.Source, t.ID); existing != nil {
			it.ExistingTaskID = existing.ID
			it.ExistingTaskState = string(existing.State)
		}
		items = append(items, it)
	}
	c.JSON(http.StatusOK, gin.H{"source": s.src().Name(), "tickets": items})
}

// POST /api/v1/tasks — 起任务（202 + 任务资源；支持 Idempotency-Key）。
func (s *Server) startTask(c *gin.Context) {
	var req startTaskReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	t, err := s.orch.StartTask(c.GetString(ctxTenantID), c.GetString(ctxUserID), req.SourceID, req.Repo, req.Title, c.GetHeader("Idempotency-Key"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusAccepted, t)
}

// GET /api/v1/tasks — 任务列表（仅当前租户）。
func (s *Server) listTasks(c *gin.Context) {
	tasks, err := s.st.ListTasksByTenant(c.GetString(ctxTenantID))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"tasks": tasks})
}

// GET /api/v1/tasks/:id — 任务详情（含各节点运行态）。
func (s *Server) getTask(c *gin.Context) {
	t := s.mustTask(c)
	if t == nil {
		return
	}
	runs, err := s.st.ListNodeRuns(t.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, taskDetail{Task: t, NodeRuns: runs})
}

// GET /api/v1/tasks/:id/issue — 取该任务对应的 GitHub Issue（人审就地渲染）。
func (s *Server) getIssue(c *gin.Context) {
	t := s.mustTask(c)
	if t == nil {
		return
	}
	if t.IssueNum == 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "Issue 尚未生成"})
		return
	}
	ctx, cancel := contextWithTimeout(c, 20*time.Second)
	defer cancel()
	iss, err := s.gh().GetIssue(ctx, s.cfg().Repos[t.Repo].GitHub, t.IssueNum)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, iss)
}

// PUT /api/v1/tasks/:id/issue — 就地编辑 Issue（写穿透回 GitHub）。
func (s *Server) editIssue(c *gin.Context) {
	t := s.mustTask(c)
	if t == nil {
		return
	}
	if t.IssueNum == 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "Issue 尚未生成"})
		return
	}
	var req editIssueReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx, cancel := contextWithTimeout(c, 20*time.Second)
	defer cancel()
	if err := s.gh().UpdateIssue(ctx, s.cfg().Repos[t.Repo].GitHub, t.IssueNum, req.Title, req.Body); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// POST /api/v1/tasks/:id/approve — 人审通过。
func (s *Server) approveTask(c *gin.Context) {
	t := s.mustTask(c)
	if t == nil {
		return
	}
	ctx, cancel := contextWithTimeout(c, 30*time.Second)
	defer cancel()
	if err := s.orch.Approve(ctx, t.ID); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"ok": true})
}

// POST /api/v1/tasks/:id/reject — 人审打回。
func (s *Server) rejectTask(c *gin.Context) {
	t := s.mustTask(c)
	if t == nil {
		return
	}
	var req rejectReq
	_ = c.ShouldBindJSON(&req)
	ctx, cancel := contextWithTimeout(c, 30*time.Second)
	defer cancel()
	if err := s.orch.Reject(ctx, t.ID, req.Reason); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"ok": true})
}

// POST /api/v1/tasks/:id/approve-pr — 人工确认 PR 审查通过（PR 审查闸口，兜底轮询）。
func (s *Server) approvePRTask(c *gin.Context) {
	t := s.mustTask(c)
	if t == nil {
		return
	}
	ctx, cancel := contextWithTimeout(c, 30*time.Second)
	defer cancel()
	if err := s.orch.ApprovePR(ctx, t.ID); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"ok": true})
}

// POST /api/v1/tasks/:id/request-revise — 人工在 PR 审查闸口触发一轮修订。
func (s *Server) requestReviseTask(c *gin.Context) {
	t := s.mustTask(c)
	if t == nil {
		return
	}
	ctx, cancel := contextWithTimeout(c, 30*time.Second)
	defer cancel()
	if err := s.orch.RequestRevise(ctx, t.ID); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"ok": true})
}

// GET /api/v1/tasks/:id/ticket — 取该任务对应的源工单（标题/正文/状态），供详情查看票内容。
func (s *Server) getTaskTicket(c *gin.Context) {
	t := s.mustTask(c)
	if t == nil {
		return
	}
	ctx, cancel := contextWithTimeout(c, 30*time.Second)
	defer cancel()
	tk, err := s.src().Get(ctx, t.SourceID)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, tk)
}

// GET /api/v1/tasks/:id/logs — 该任务各阶段的 claude 全量输出（排查用）。
func (s *Server) getTaskLogs(c *gin.Context) {
	t := s.mustTask(c)
	if t == nil {
		return
	}
	dir := s.cfg().LogsDir()
	logs := []gin.H{}
	for _, label := range []string{"B", "C", "D"} {
		b, err := os.ReadFile(filepath.Join(dir, t.ID+"."+label+".log"))
		if err == nil {
			logs = append(logs, gin.H{"label": label, "content": string(b)})
		}
	}
	c.JSON(http.StatusOK, gin.H{"logs": logs})
}

// POST /api/v1/tasks/:id/cancel — 取消任务（杀掉运行中的 claude）。
func (s *Server) cancelTask(c *gin.Context) {
	t := s.mustTask(c)
	if t == nil {
		return
	}
	if err := s.orch.Cancel(t.ID); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"ok": true})
}

// POST /internal/tasks/:id/event — skill 回传结构化阶段事件。
func (s *Server) ingestEvent(c *gin.Context) {
	t := s.mustTask(c)
	if t == nil {
		return
	}
	var req ingestReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// 携带产物 → 更新任务字段
	dirty := false
	if req.IssueNum > 0 {
		t.IssueNum, t.IssueURL, dirty = req.IssueNum, req.IssueURL, true
	}
	if req.PRURL != "" {
		t.PRURL, dirty = req.PRURL, true
	}
	if dirty {
		_ = s.st.UpdateTask(t)
	}

	// phase → 节点状态
	node := s.orch.Pipeline().NodeForPhase(req.Phase)
	evType := "node.log"
	if node != "" {
		now := time.Now()
		switch req.Status {
		case "start":
			_ = s.st.UpsertNodeRun(&store.NodeRun{TaskID: t.ID, NodeID: node, State: store.NodeRunning, StartedAt: &now})
			evType = "node.started"
		case "ok", "skip":
			_ = s.st.UpsertNodeRun(&store.NodeRun{TaskID: t.ID, NodeID: node, State: store.NodeOK, EndedAt: &now})
			evType = "node.completed"
		case "fail":
			_ = s.st.UpsertNodeRun(&store.NodeRun{TaskID: t.ID, NodeID: node, State: store.NodeFail, EndedAt: &now})
			evType = "node.failed"
		}
	}
	level := req.Level
	if level == "" {
		level = "info"
	}
	_ = s.bus.Publish(&store.Event{
		TaskID: t.ID, NodeID: node, Type: evType, Level: level, Message: req.Message,
	})

	// 终局声明：skip=正常无需处理(已跳过)，fail=异常遇阻(待裁决)。
	switch req.Status {
	case "skip":
		s.orch.DeclareSkip(t.ID, req.Message)
	case "fail":
		s.orch.DeclareFail(t.ID, node, req.Message)
	}
	c.JSON(http.StatusAccepted, gin.H{"ok": true})
}

// mustTask 取路径任务，不存在则写 404 并返回 nil。 //nolint
func (s *Server) mustTask(c *gin.Context) *store.Task {
	t, err := s.st.GetTask(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return nil
	}
	// 跨租户不可见：非本租户任务一律当作不存在（防越权枚举）。
	if t == nil || t.TenantID != c.GetString(ctxTenantID) {
		c.JSON(http.StatusNotFound, gin.H{"error": "任务不存在"})
		return nil
	}
	return t
}
