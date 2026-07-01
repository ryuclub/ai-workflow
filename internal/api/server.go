// Package api 是薄 HTTP 适配层：校验 → 调 core 服务 → 序列化。无业务逻辑。
package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/ryuclub/ai-workflow/internal/core/config"
	"github.com/ryuclub/ai-workflow/internal/core/events"
	"github.com/ryuclub/ai-workflow/internal/core/github"
	"github.com/ryuclub/ai-workflow/internal/core/orchestrator"
	"github.com/ryuclub/ai-workflow/internal/core/source"
	"github.com/ryuclub/ai-workflow/internal/core/store"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

// Deps 提供随设置热重载而变的依赖；Reload 在设置变更后重建。
type Deps interface {
	Config() *config.Config
	Provider() source.Provider
	Github() *github.Client
	Reload() error
}

// Server 持有 API 处理器所需的 core 依赖。
type Server struct {
	deps   Deps
	st     store.Store
	bus    *events.Bus
	orch   *orchestrator.Orchestrator
	health *healthState
}

// NewServer 组装依赖。deps 在每次取用时返回当前生效配置/票源/客户端。
func NewServer(deps Deps, st store.Store, bus *events.Bus, orch *orchestrator.Orchestrator) *Server {
	return &Server{deps: deps, st: st, bus: bus, orch: orch, health: newHealth()}
}

func (s *Server) cfg() *config.Config  { return s.deps.Config() }
func (s *Server) src() source.Provider { return s.deps.Provider() }
func (s *Server) gh() *github.Client   { return s.deps.Github() }

// Router 构建 gin 引擎并挂载公共(v1) + 内部路由。
func (s *Server) Router() *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())

	corsCfg := cors.Config{
		AllowAllOrigins: true,
		AllowMethods:    []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:    []string{"Content-Type", "Idempotency-Key", "Last-Event-ID", "Authorization"},
	}
	r.Use(cors.New(corsCfg))

	// 鉴权：设了 admin token 才强制（Bearer）；未设则开放（纯 localhost 起步）。
	v1 := r.Group("/api/v1", s.requireAdmin())
	{
		v1.GET("/repos", s.listRepos)
		v1.GET("/pipeline", s.getPipeline)
		v1.GET("/source", s.getSource)
		v1.GET("/tickets", s.listTickets)
		v1.POST("/tasks", s.startTask)
		v1.GET("/tasks", s.listTasks)
		v1.GET("/tasks/:id", s.getTask)
		v1.GET("/tasks/:id/ticket", s.getTaskTicket)
		v1.GET("/tasks/:id/issue", s.getIssue)
		v1.PUT("/tasks/:id/issue", s.editIssue)
		v1.POST("/tasks/:id/approve", s.approveTask)
		v1.POST("/tasks/:id/reject", s.rejectTask)
		v1.POST("/tasks/:id/approve-pr", s.approvePRTask)
		v1.POST("/tasks/:id/request-revise", s.requestReviseTask)
		v1.POST("/tasks/:id/cancel", s.cancelTask)
		v1.GET("/tasks/:id/events", s.streamEvents)
		v1.GET("/tasks/:id/logs", s.getTaskLogs)
		// 健康面板
		v1.GET("/health", s.getHealth)
		v1.POST("/health/check", s.checkHealth)
		// 设置中心
		v1.GET("/settings", s.getSettings)
		v1.PUT("/settings", s.putSettings)
		v1.POST("/settings/test/:kind", s.testConnection)
		v1.GET("/settings/repos", s.listSettingRepos)
		v1.PUT("/settings/repos", s.upsertRepo)
		v1.POST("/settings/repos/import", s.importRepos)
		v1.PUT("/settings/repos/:name/enabled", s.setRepoEnabled)
		v1.DELETE("/settings/repos/:name", s.deleteRepo)
	}
	// 鉴权探测：前端据此判断是否需要登录（不暴露任何数据）。
	r.GET("/api/v1/auth/status", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"auth_required": s.cfg().AdminToken() != ""})
	})

	// 内部接口：仅 skill 回传事件，token 校验，不公开、不版本化。
	in := r.Group("/internal", s.requireInternalToken())
	{
		in.POST("/tasks/:id/event", s.ingestEvent)
	}

	s.mountFrontend(r)
	return r
}

// requireAdmin 校验公共 API 的 admin token：设了才强制。
// 支持 Authorization: Bearer 或 ?token=（SSE/EventSource 无法设自定义头，故留 query 口子）。
func (s *Server) requireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		want := s.cfg().AdminToken()
		if want == "" {
			c.Next()
			return
		}
		got := strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
		if got == "" {
			got = c.Query("token")
		}
		if got != want {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		c.Next()
	}
}

// requireInternalToken 校验 /internal 的共享 token。
func (s *Server) requireInternalToken() gin.HandlerFunc {
	want := s.cfg().InternalToken()
	return func(c *gin.Context) {
		if want == "" || c.GetHeader("X-Internal-Token") != want {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "bad internal token"})
			return
		}
		c.Next()
	}
}

// mountFrontend 在前端构建产物存在时托管 SPA（dev 模式由 Vite 单独跑）。
func (s *Server) mountFrontend(r *gin.Engine) {
	dist := filepath.Join(s.cfg().Root, "frontend", "dist")
	if _, err := os.Stat(filepath.Join(dist, "index.html")); err != nil {
		return
	}
	r.Static("/assets", filepath.Join(dist, "assets"))
	r.NoRoute(func(c *gin.Context) {
		c.File(filepath.Join(dist, "index.html"))
	})
}
