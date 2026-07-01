package api

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// healthCheck 是单项探测结果。
type healthCheck struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

// healthState 缓存探测结果，避免频繁打扰外部服务。
type healthState struct {
	mu      sync.Mutex
	checks  map[string]healthCheck
	ts      time.Time
	running bool
}

func newHealth() *healthState { return &healthState{checks: map[string]healthCheck{}} }

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

// probeGithub / probeSource 轻量、可定时；probeClaude 较重、仅手动触发。
// 均按传入租户探测（健康缓存为全局单份，展示最近一次探测的租户结果——运维诊断用，非数据）。
func (s *Server) probeGithub(ctx context.Context, tenantID string) {
	gh := s.deps.Github(tenantID)
	if gh == nil {
		s.health.set(healthCheck{"github", false, "该租户未配置 GitHub"})
		return
	}
	login, err := gh.WhoAmI(ctx)
	if err != nil {
		s.health.set(healthCheck{"github", false, "未认证：" + tailErr(err)})
		return
	}
	s.health.set(healthCheck{"github", true, "已认证：" + login})
}

func (s *Server) probeSource(ctx context.Context, tenantID string) {
	prov := s.deps.Provider(tenantID)
	if prov == nil {
		s.health.set(healthCheck{"source", false, "该租户未配置票源"})
		return
	}
	if _, err := prov.List(ctx, "", 1); err != nil {
		s.health.set(healthCheck{"source", false, "不可达：" + tailErr(err)})
		return
	}
	s.health.set(healthCheck{"source", true, prov.Name() + " 可达"})
}

func (s *Server) probeClaude(ctx context.Context) {
	cmd := exec.CommandContext(ctx, s.deps.Config("").ClaudeBin(), "-p", "只回复 ok", "--dangerously-skip-permissions")
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	out := buf.String()
	for _, m := range []string{"Not logged in", "Please run /login", "Invalid API key"} {
		if strings.Contains(out, m) {
			s.health.set(healthCheck{"claude", false, "未登录（须在图形登录会话内，或配 OAuth token）"})
			return
		}
	}
	if err != nil {
		s.health.set(healthCheck{"claude", false, "调用失败：" + tailErr(err)})
		return
	}
	s.health.set(healthCheck{"claude", true, "已登录可用"})
}

// GET /api/v1/health — 返回缓存；过期则后台刷新 github/source（claude 不自动跑）。
func (s *Server) getHealth(c *gin.Context) {
	s.health.mu.Lock()
	stale := time.Since(s.health.ts) > 30*time.Second && !s.health.running
	if stale {
		s.health.running = true
		s.health.ts = time.Now()
	}
	s.health.mu.Unlock()
	if stale {
		tenantID := c.GetString(ctxTenantID)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()
			s.probeGithub(ctx, tenantID)
			s.probeSource(ctx, tenantID)
			s.health.mu.Lock()
			s.health.running = false
			s.health.mu.Unlock()
		}()
	}
	c.JSON(200, gin.H{"checks": s.health.snapshot()})
}

// POST /api/v1/health/check — 全量探测（含 claude，较慢），同步返回。
func (s *Server) checkHealth(c *gin.Context) {
	ctx, cancel := contextWithTimeout(c, 40*time.Second)
	defer cancel()
	tenantID := c.GetString(ctxTenantID)
	s.probeGithub(ctx, tenantID)
	s.probeSource(ctx, tenantID)
	s.probeClaude(ctx)
	c.JSON(200, gin.H{"checks": s.health.snapshot()})
}

func tailErr(err error) string {
	m := err.Error()
	if len(m) > 120 {
		return m[len(m)-120:]
	}
	return m
}
