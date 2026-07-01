// Package app 装配运行时：持有当前生效的配置/票源/客户端，支持设置热重载。
package app

import (
	"context"
	"fmt"
	"sync"

	"github.com/ryuclub/ai-workflow/internal/core/config"
	"github.com/ryuclub/ai-workflow/internal/core/github"
	"github.com/ryuclub/ai-workflow/internal/core/repomanager"
	"github.com/ryuclub/ai-workflow/internal/core/secret"
	"github.com/ryuclub/ai-workflow/internal/core/source"
)

// Runtime 实现 orchestrator.Deps / runner.Env / api.Deps：取用时返回当前实例，Reload 后切换。
type Runtime struct {
	root  string
	mu    sync.RWMutex
	cfg   *config.Config
	prov  source.Provider
	gh    *github.Client
	repo  *repomanager.Manager
	vault *secret.Vault // 凭据/登录态令牌解析；未配 MASTER_KEY 时为 nil
}

// SetVault 注入凭据保险箱（main 打开 store 后装配）。
func (rt *Runtime) SetVault(v *secret.Vault) {
	rt.mu.Lock()
	rt.vault = v
	rt.mu.Unlock()
}

// ClaudeToken 实现 runner.Env：两级解析任务应使用的登录态令牌；未配保险箱返回空（回落宿主登录态）。
func (rt *Runtime) ClaudeToken(tenantID, userID string) string {
	rt.mu.RLock()
	v := rt.vault
	rt.mu.RUnlock()
	if v == nil {
		return ""
	}
	return v.ResolveClaudeToken(tenantID, userID)
}

// NewRuntime 加载配置并完成首次装配。
func NewRuntime(root string) (*Runtime, error) {
	rt := &Runtime{root: root}
	if err := rt.Reload(); err != nil {
		return nil, err
	}
	return rt, nil
}

// Reload 重读配置文件并重建票源/客户端/仓管理器。
func (rt *Runtime) Reload() error {
	cfg, err := config.Load(rt.root)
	if err != nil {
		return err
	}
	prov, err := buildProvider(cfg)
	if err != nil {
		return err
	}
	rt.mu.Lock()
	rt.cfg = cfg
	rt.prov = prov
	rt.gh = github.NewWithToken(cfg.GithubToken())
	rt.repo = repomanager.New(cfg.ReposDir(), cfg.GithubToken())
	rt.mu.Unlock()
	return nil
}

func (rt *Runtime) Config() *config.Config    { rt.mu.RLock(); defer rt.mu.RUnlock(); return rt.cfg }
func (rt *Runtime) Provider() source.Provider { rt.mu.RLock(); defer rt.mu.RUnlock(); return rt.prov }
func (rt *Runtime) Github() *github.Client    { rt.mu.RLock(); defer rt.mu.RUnlock(); return rt.gh }

// RepoPath 返回控制面自管的「主仓」克隆路径（按 GitHub 地址）。
// 一律自管，不使用开发者本地工作目录——任务在主仓之外的独立 worktree 里干活，绝不碰主仓工作树。
func (rt *Runtime) RepoPath(ctx context.Context, githubFull string) (string, error) {
	rt.mu.RLock()
	rm := rt.repo
	rt.mu.RUnlock()
	return rm.EnsureLocal(ctx, githubFull)
}

// buildProvider 据配置实例化活跃票源（jira / linear，二选一）。
func buildProvider(cfg *config.Config) (source.Provider, error) {
	switch cfg.ActiveSource() {
	case "jira":
		return source.NewJira(cfg.JiraScript(), cfg.JiraProject(), cfg.AtlassianDomain()), nil
	case "linear":
		return source.NewLinear(cfg.LinearAPIKey(), cfg.LinearTeam()), nil
	default:
		return nil, fmt.Errorf("未知票源: %q（仅支持 jira / linear）", cfg.ActiveSource())
	}
}
