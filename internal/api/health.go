package api

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/ryuclub/ai-workflow/internal/core/secret"
	"github.com/ryuclub/ai-workflow/internal/core/store"
	"github.com/gin-gonic/gin"
)

// healthCheck 是单项探测结果。
type healthCheck struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

// healthState 缓存某 (租户,用户) 的探测结果，避免频繁打扰外部服务。
type healthState struct {
	mu      sync.Mutex
	checks  map[string]healthCheck
	ts      time.Time
	running bool
}

// healthRegistry 按 (租户,用户) 维护各自的健康缓存——GitHub/Claude 令牌均按用户解析，
// 故健康须按用户隔离，不能全局单份（否则跨用户/跨租户串结果）。
type healthRegistry struct {
	mu    sync.Mutex
	byKey map[string]*healthState
}

func newHealthRegistry() *healthRegistry { return &healthRegistry{byKey: map[string]*healthState{}} }

func (r *healthRegistry) forKey(key string) *healthState {
	r.mu.Lock()
	defer r.mu.Unlock()
	h := r.byKey[key]
	if h == nil {
		h = &healthState{checks: map[string]healthCheck{}}
		r.byKey[key] = h
	}
	return h
}

// healthFor 返回当前会话 (租户,用户) 的健康状态。
func (s *Server) healthFor(c *gin.Context) *healthState {
	return s.health.forKey(c.GetString(ctxTenantID) + "|" + c.GetString(ctxUserID))
}

func (h *healthState) snapshot() []healthCheck {
	h.mu.Lock()
	defer h.mu.Unlock()
	order := []string{"github", "source", "claude"}
	out := make([]healthCheck, 0, len(order))
	for _, k := range order {
		if c, ok := h.checks[k]; ok {
			out = append(out, c)
		}
	}
	return out
}

func (h *healthState) set(c healthCheck) {
	h.mu.Lock()
	h.checks[c.Name] = c
	h.mu.Unlock()
}

// probeGithub：按(租户,用户)两级令牌探测；probeSource：按租户；probeClaude：按用户令牌。
func (s *Server) probeGithub(ctx context.Context, h *healthState, tenantID, userID string) {
	// 按「解析出的令牌是否存在」判未配置——GitHub 客户端恒非 nil，若无令牌 WhoAmI 会
	// 回落宿主 gh 登录态、误显运维身份，故先判令牌串。
	hasTenant := s.deps.Config(tenantID).GithubToken() != ""
	hasUser := s.vault != nil && s.vault.HasUser(userID, secret.KeyGithubToken)
	if !hasTenant && !hasUser {
		// 未配令牌 ≠ 不可用：单机模式下任务会回落宿主凭据（gh 登录态 / ssh config）。
		// 实探宿主回落身份，能用就绿灯并如实标注来源。
		if login, err := s.deps.Github(tenantID, userID).WhoAmI(ctx); err == nil && login != "" {
			h.set(healthCheck{"github", true, "未配置 token，回落宿主凭据：" + login})
			return
		}
		h.set(healthCheck{"github", false, "未配置 token，宿主亦无 gh 登录态"})
		return
	}
	login, err := s.deps.Github(tenantID, userID).WhoAmI(ctx)
	if err != nil {
		h.set(healthCheck{"github", false, "未认证：" + tailErr(err)})
		return
	}
	h.set(healthCheck{"github", true, "已认证：" + login})
}

func (s *Server) probeSource(ctx context.Context, h *healthState, tenantID string) {
	prov := s.deps.Provider(tenantID)
	if prov == nil {
		h.set(healthCheck{"source", false, "该公司未配置票源"})
		return
	}
	if _, err := prov.List(ctx, "", 1); err != nil {
		h.set(healthCheck{"source", false, "不可达：" + tailErr(err)})
		return
	}
	h.set(healthCheck{"source", true, prov.Name() + " 可达"})
}

func (s *Server) probeClaude(ctx context.Context, h *healthState, tenantID, userID string) {
	cmd := exec.CommandContext(ctx, s.deps.Config("").ClaudeBin(), "-p", "只回复 ok", "--dangerously-skip-permissions")
	// 按当前用户解析登录态令牌注入（个人 > 公司共享）；解析出则用它、否则回落宿主登录态。
	env := os.Environ()
	if s.vault != nil {
		if tok := s.vault.ResolveClaudeToken(tenantID, userID); tok != "" {
			env = dropEnv(env, "CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY")
			env = append(env, "CLAUDE_CODE_OAUTH_TOKEN="+tok)
		}
	}
	cmd.Env = env
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	out := buf.String()
	for _, m := range []string{"Not logged in", "Please run /login", "Invalid API key"} {
		if strings.Contains(out, m) {
			h.set(healthCheck{"claude", false, "未登录（配公司共享或个人令牌，或宿主登录态）"})
			return
		}
	}
	if err != nil {
		h.set(healthCheck{"claude", false, "调用失败：" + tailErr(err)})
		return
	}
	h.set(healthCheck{"claude", true, "已登录可用"})
}

// dropEnv 返回剔除了指定 KEY（按 KEY= 前缀）的环境副本。
func dropEnv(env []string, keys ...string) []string {
	out := make([]string, 0, len(env))
	for _, e := range env {
		keep := true
		for _, k := range keys {
			if strings.HasPrefix(e, k+"=") {
				keep = false
				break
			}
		}
		if keep {
			out = append(out, e)
		}
	}
	return out
}

// GET /api/v1/health — 返回当前用户缓存；过期则后台刷新 github/source（claude 不自动跑）。
// 探测节流 2 分钟：健康不需要秒级粒度，且每次探测都真实敲 GitHub/票源。
// 探测结果与上次不同（好↔坏）时发 health.changed 进租户事件流，前端事件驱动刷新。
func (s *Server) getHealth(c *gin.Context) {
	h := s.healthFor(c)
	tenantID, userID := c.GetString(ctxTenantID), c.GetString(ctxUserID)
	h.mu.Lock()
	stale := time.Since(h.ts) > 2*time.Minute && !h.running
	if stale {
		h.running = true
		h.ts = time.Now()
	}
	h.mu.Unlock()
	if stale {
		before := healthKey(h.snapshot())
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()
			s.probeGithub(ctx, h, tenantID, userID)
			s.probeSource(ctx, h, tenantID)
			h.mu.Lock()
			h.running = false
			h.mu.Unlock()
			if after := healthKey(h.snapshot()); after != before {
				_ = s.bus.Publish(&store.Event{TenantID: tenantID, Type: "health.changed", Level: "info",
					Message: "环境健康状态变化"})
			}
		}()
	}
	c.JSON(200, gin.H{"checks": h.snapshot()})
}

// healthKey 把探测结果压成可比较的指纹（仅项名+好坏；detail 文本抖动不算状态变化）。
func healthKey(checks []healthCheck) string {
	var b strings.Builder
	for _, c := range checks {
		b.WriteString(c.Name)
		if c.OK {
			b.WriteString(":1;")
		} else {
			b.WriteString(":0;")
		}
	}
	return b.String()
}

// POST /api/v1/health/check — 全量探测（含 claude，较慢），同步返回。
func (s *Server) checkHealth(c *gin.Context) {
	ctx, cancel := contextWithTimeout(c, 40*time.Second)
	defer cancel()
	h := s.healthFor(c)
	tenantID, userID := c.GetString(ctxTenantID), c.GetString(ctxUserID)
	s.probeGithub(ctx, h, tenantID, userID)
	s.probeSource(ctx, h, tenantID)
	s.probeClaude(ctx, h, tenantID, userID)
	c.JSON(200, gin.H{"checks": h.snapshot()})
}

func tailErr(err error) string {
	m := err.Error()
	if len(m) > 120 {
		return m[len(m)-120:]
	}
	return m
}
