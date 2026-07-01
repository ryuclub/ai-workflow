// Package api 是薄 HTTP 适配层：校验 → 调 core 服务 → 序列化。无业务逻辑。
package api

import (
	"net/http"
	"os"
	"path/filepath"

	"github.com/ryuclub/ai-workflow/internal/core/config"
	"github.com/ryuclub/ai-workflow/internal/core/events"
	"github.com/ryuclub/ai-workflow/internal/core/github"
	"github.com/ryuclub/ai-workflow/internal/core/orchestrator"
	"github.com/ryuclub/ai-workflow/internal/core/secret"
	"github.com/ryuclub/ai-workflow/internal/core/source"
	"github.com/ryuclub/ai-workflow/internal/core/store"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

// Deps 提供随设置热重载而变的依赖；Reload 在设置变更后重建。
type Deps interface {
	Config(tenantID string) *config.Config    // tenantID 为空返回全局模板（运行期操作键）
	Provider(tenantID string) source.Provider // 按租户票源
	Github(tenantID string) *github.Client    // 按租户 GitHub 客户端
	Reload(tenantID string) error             // 使某租户配置缓存失效；空=重载全局模板
}

// Server 持有 API 处理器所需的 core 依赖。
type Server struct {
	deps   Deps
	st     store.Store
	ids    store.IdentityStore
	vault  *secret.Vault // 凭据保险箱；未配 MASTER_KEY 时为 nil
	bus    *events.Bus
	orch   *orchestrator.Orchestrator
	health *healthState
}

// NewServer 组装依赖。deps 在每次取用时返回当前生效配置/票源/客户端。
func NewServer(deps Deps, st store.Store, ids store.IdentityStore, vault *secret.Vault, bus *events.Bus, orch *orchestrator.Orchestrator) *Server {
	return &Server{deps: deps, st: st, ids: ids, vault: vault, bus: bus, orch: orch, health: newHealth()}
}

// cfg/src/gh 按当前会话租户解析。tid 从 requireAuth 注入的 ctx 取。
func (s *Server) cfg(c *gin.Context) *config.Config  { return s.deps.Config(c.GetString(ctxTenantID)) }
func (s *Server) src(c *gin.Context) source.Provider { return s.deps.Provider(c.GetString(ctxTenantID)) }
func (s *Server) gh(c *gin.Context) *github.Client   { return s.deps.Github(c.GetString(ctxTenantID)) }

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

	// 公开：登录与鉴权探测（不暴露数据）。
	pub := r.Group("/api/v1")
	{
		pub.POST("/auth/login", s.login)
		pub.GET("/auth/status", func(c *gin.Context) {
			n, _ := s.ids.CountUsers()
			c.JSON(http.StatusOK, gin.H{"auth_required": true, "bootstrapped": n > 0})
		})
	}

	// 鉴权：一律要求登录会话（Bearer 或 ?token=）。
	v1 := r.Group("/api/v1", s.requireAuth())
	{
		v1.POST("/auth/logout", s.logout)
		v1.GET("/auth/me", s.me)
		// Claude 登录态令牌：租户共享（管理员）+ 个人（本人）。
		v1.GET("/settings/claude-token", s.requireAdminRole(), s.getTenantClaudeToken)
		v1.PUT("/settings/claude-token", s.requireAdminRole(), s.putTenantClaudeToken)
		v1.GET("/me/claude-token", s.getMyClaudeToken)
		v1.PUT("/me/claude-token", s.putMyClaudeToken)
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
		// 设置中心：读放行给成员，写/连测/仓管理需租户管理员。
		v1.GET("/settings", s.getSettings)
		v1.PUT("/settings", s.requireAdminRole(), s.putSettings)
		v1.POST("/settings/test/:kind", s.requireAdminRole(), s.testConnection)
		v1.GET("/settings/repos", s.listSettingRepos)
		v1.PUT("/settings/repos", s.requireAdminRole(), s.upsertRepo)
		v1.POST("/settings/repos/import", s.requireAdminRole(), s.importRepos)
		v1.PUT("/settings/repos/:name/enabled", s.requireAdminRole(), s.setRepoEnabled)
		v1.DELETE("/settings/repos/:name", s.requireAdminRole(), s.deleteRepo)
	}

	// 内部接口：仅 skill 回传事件，token 校验，不公开、不版本化。
	in := r.Group("/internal", s.requireInternalToken())
	{
		in.POST("/tasks/:id/event", s.ingestEvent)
	}

	s.mountFrontend(r)
	return r
}

// requireInternalToken 校验 /internal 的共享 token。
func (s *Server) requireInternalToken() gin.HandlerFunc {
	want := s.deps.Config("").InternalToken()
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
	dist := filepath.Join(s.deps.Config("").Root, "frontend", "dist")
	if _, err := os.Stat(filepath.Join(dist, "index.html")); err != nil {
		return
	}
	r.Static("/assets", filepath.Join(dist, "assets"))
	r.NoRoute(func(c *gin.Context) {
		c.File(filepath.Join(dist, "index.html"))
	})
}
