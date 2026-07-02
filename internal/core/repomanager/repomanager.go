// Package repomanager 按 GitHub 地址自管本地克隆：首次 clone、用前 fetch。
// 这样登记仓只需填 owner/repo，无需预先手动克隆并写死本地路径。
package repomanager

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/ryuclub/ai-workflow/internal/core/gitauth"
)

// Manager 在受管目录下维护各仓的本地克隆。
type Manager struct {
	dir   string // 克隆根目录
	token string // GitHub token（私有仓用；空则靠 git 凭据助手/SSH）

	mu    sync.Mutex
	locks map[string]*sync.Mutex // 按仓路径串行化 clone/fetch，避免并发首克隆竞态
}

// New 构造管理器。
func New(reposDir, token string) *Manager {
	return &Manager{dir: reposDir, token: token, locks: map[string]*sync.Mutex{}}
}

// lockFor 返回某路径的专属锁（懒建）。
func (m *Manager) lockFor(path string) *sync.Mutex {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.locks[path] == nil {
		m.locks[path] = &sync.Mutex{}
	}
	return m.locks[path]
}

// localPath 把 owner/repo 映射到受管本地路径。
func (m *Manager) localPath(full string) string {
	return filepath.Join(m.dir, strings.ReplaceAll(full, "/", "__"))
}

// cloneURL 恒为无令牌 URL：认证经 http.extraheader（-c 参数）注入，token 不落 .git/config，
// 使 worktree 继承的 remote 无内嵌凭据——git push/fetch 由 skill 进程按任务解析令牌认证。
func (m *Manager) cloneURL(full string) string {
	return fmt.Sprintf("https://github.com/%s.git", full)
}

// EnsureLocal 确保 owner/repo 已克隆且最新，返回本地路径。
func (m *Manager) EnsureLocal(ctx context.Context, full string) (string, error) {
	if full == "" || !strings.Contains(full, "/") {
		return "", fmt.Errorf("非法 github 地址: %q（应为 owner/repo）", full)
	}
	path := m.localPath(full)
	lk := m.lockFor(path) // 同一仓的 clone/fetch 串行
	lk.Lock()
	defer lk.Unlock()
	// 认证经 GIT_CONFIG_* 环境注入 http.extraheader（token 不进 argv、不落 .git/config）。
	var gitEnv []string
	if e := gitauth.ConfigEnv(m.token); e != nil {
		gitEnv = append(os.Environ(), e...)
	}
	if _, err := os.Stat(filepath.Join(path, ".git")); err == nil {
		// 已克隆：先把 origin 抹成无令牌 URL（清除历史版本可能内嵌的旧令牌，幂等），再 fetch。
		_ = run(ctx, path, gitEnv, "git", "remote", "set-url", "origin", m.cloneURL(full))
		_ = run(ctx, path, gitEnv, "git", "fetch", "--prune", "origin") // 失败不致命，用现有副本继续
		return path, nil
	}
	if err := os.MkdirAll(m.dir, 0o755); err != nil {
		return "", err
	}
	// 有 token → git clone 无令牌 URL + extraheader 认证；无 token → gh repo clone（复用 gh 登录态）。
	if m.token != "" {
		if err := run(ctx, "", gitEnv, "git", "clone", "--quiet", m.cloneURL(full), path); err != nil {
			return "", fmt.Errorf("克隆 %s 失败: %w", full, err)
		}
	} else {
		if err := run(ctx, "", nil, "gh", "repo", "clone", full, path); err != nil {
			return "", fmt.Errorf("gh 克隆 %s 失败（检查 gh 登录或配 GITHUB_TOKEN）: %w", full, err)
		}
	}
	return path, nil
}

func run(ctx context.Context, dir string, env []string, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	if env != nil {
		cmd.Env = env
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, tail(string(out), 200))
	}
	return nil
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
