// Package app 装配运行时：持有全局模板配置，并按租户解析出各自的配置/票源/客户端。
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

// TenantConfigStore 提供某租户的非机密配置 JSON（source/default/status_map/repos）。
type TenantConfigStore interface {
	GetTenantConfigJSON(tenantID string) (string, error)
}

// tenantRT 是某租户解析出的运行期实例。
// gh 不在此缓存：GitHub 客户端按「租户共享 + 用户个人」两级令牌逐次构建（见 Github）。
type tenantRT struct {
	cfg  *config.Config
	prov source.Provider
	repo *repomanager.Manager
}

// Runtime 持有全局模板（base），并按租户懒构建 + 缓存各自的运行期实例。
// 实现 orchestrator.Deps / runner.Env / api.Deps：取用时按 tenantID 返回对应实例。
type Runtime struct {
	root  string
	mu    sync.RWMutex
	base  *config.Config    // 全局模板：运行期操作键（PORT/WORKTREE_BASE…）+ 默认 repos/source
	vault *secret.Vault     // 按租户凭据；nil=未配 MASTER_KEY（回落全局 .env 凭据）
	cfgSt TenantConfigStore // 按租户配置 JSON
	cache map[string]*tenantRT
}

// NewRuntime 加载全局模板并完成首次装配。
func NewRuntime(root string) (*Runtime, error) {
	rt := &Runtime{root: root, cache: map[string]*tenantRT{}}
	if err := rt.ReloadBase(); err != nil {
		return nil, err
	}
	return rt, nil
}

// SetVault / SetConfigStore 在 main 打开 store 后注入（构造租户级配置所需）。
func (rt *Runtime) SetVault(v *secret.Vault)            { rt.mu.Lock(); rt.vault = v; rt.mu.Unlock() }
func (rt *Runtime) SetConfigStore(s TenantConfigStore)  { rt.mu.Lock(); rt.cfgSt = s; rt.mu.Unlock() }

// ReloadBase 重读全局模板文件并清空所有租户缓存（下次取用时按新模板重建）。
func (rt *Runtime) ReloadBase() error {
	base, err := config.Load(rt.root)
	if err != nil {
		return err
	}
	rt.mu.Lock()
	rt.base = base
	rt.cache = map[string]*tenantRT{}
	rt.mu.Unlock()
	return nil
}

// Reload 使某租户缓存失效（下次取用时按最新配置/凭据重建）。tenantID 为空则等价 ReloadBase。
func (rt *Runtime) Reload(tenantID string) error {
	if tenantID == "" {
		return rt.ReloadBase()
	}
	rt.mu.Lock()
	delete(rt.cache, tenantID)
	rt.mu.Unlock()
	return nil
}

// resolve 返回租户运行期实例（懒构建 + 缓存）。tenantID 为空返回全局模板实例（运行期操作键用）。
func (rt *Runtime) resolve(tenantID string) *tenantRT {
	rt.mu.RLock()
	if t := rt.cache[tenantID]; t != nil {
		rt.mu.RUnlock()
		return t
	}
	base, vault, cfgSt := rt.base, rt.vault, rt.cfgSt
	rt.mu.RUnlock()

	if tenantID == "" {
		return &tenantRT{cfg: base} // 全局模板：仅供读运行期操作键，不构造票源/客户端
	}

	// 该租户配置 JSON（无则继承 base 模板）。
	var tenantJSON string
	if cfgSt != nil {
		tenantJSON, _ = cfgSt.GetTenantConfigJSON(tenantID)
	}
	// 该租户凭据：有 vault 取加密凭据；否则回落全局 .env（单机开发）。
	creds := map[string]string{}
	for _, k := range config.CredentialKeys {
		if vault != nil {
			if v := vault.GetTenant(tenantID, k); v != "" {
				creds[k] = v
			}
		} else if v := base.Env[k]; v != "" {
			creds[k] = v
		}
	}
	cfg, err := config.FromTenant(base, tenantID, tenantJSON, creds)
	if err != nil {
		cfg, _ = config.FromTenant(base, tenantID, "", creds) // 坏 JSON 回落模板，不致命
	}
	prov, _ := buildProvider(cfg) // 源构造失败留 nil，由上层处理
	t := &tenantRT{
		cfg:  cfg,
		prov: prov,
		repo: repomanager.New(cfg.ReposDir(), cfg.GithubToken()), // 克隆用租户共享令牌（公司仓）
	}
	rt.mu.Lock()
	rt.cache[tenantID] = t
	rt.mu.Unlock()
	return t
}

// Config / Provider 按租户返回。tenantID 为空返回全局模板配置（运行期操作键）。
func (rt *Runtime) Config(tenantID string) *config.Config    { return rt.resolve(tenantID).cfg }
func (rt *Runtime) Provider(tenantID string) source.Provider { return rt.resolve(tenantID).prov }

// GithubToken 两级解析该(租户,用户)应使用的 GitHub token：用户个人 > 租户共享 > 空。
func (rt *Runtime) GithubToken(tenantID, userID string) string {
	tenantShared := rt.resolve(tenantID).cfg.GithubToken()
	rt.mu.RLock()
	v := rt.vault
	rt.mu.RUnlock()
	if v == nil {
		return tenantShared
	}
	return v.ResolveGithubToken(userID, tenantShared)
}

// Github 按(租户,用户)两级令牌构建 GitHub 客户端（逐次构建，NewWithToken 很轻）。
func (rt *Runtime) Github(tenantID, userID string) *github.Client {
	return github.NewWithToken(rt.GithubToken(tenantID, userID))
}

// RepoPath 返回某租户自管的主仓克隆路径（按 GitHub 地址；克隆目录已按租户隔离）。
func (rt *Runtime) RepoPath(ctx context.Context, tenantID, githubFull string) (string, error) {
	rm := rt.resolve(tenantID).repo
	if rm == nil {
		return "", fmt.Errorf("租户 %q 无仓管理器", tenantID)
	}
	return rm.EnsureLocal(ctx, githubFull)
}

// ClaudeToken 两级解析任务应使用的登录态令牌；未配保险箱返回空（回落宿主登录态）。
func (rt *Runtime) ClaudeToken(tenantID, userID string) string {
	rt.mu.RLock()
	v := rt.vault
	rt.mu.RUnlock()
	if v == nil {
		return ""
	}
	return v.ResolveClaudeToken(tenantID, userID)
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
