// Package config 加载多仓登记表（config.json）与运行期环境（.env）。
// 纯领域层：不依赖任何 web 框架。
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Repo 是一个被自动化的目标仓登记项（来自 config.json）。
type Repo struct {
	Name    string   `json:"name,omitempty"`    // 登记键（运行时由 map key 回填）
	GitHub  string   `json:"github"`            // owner/repo
	Path    string   `json:"path,omitempty"`    // 已弃用：任务一律用控制面自管克隆，不使用本地工作目录
	Match   []string `json:"match"`             // 标题关键词（路由兜底用）
	Base    string   `json:"base,omitempty"`    // PR base 覆盖（如 stage；空=由目标仓约定/默认分支决定）
	Enabled *bool    `json:"enabled,omitempty"` // 是否启用；nil=启用（向后兼容既有登记，自动导入的仓显式置 false）
}

// IsEnabled 判定该仓是否启用：未显式设置（nil）视为启用，保持对旧 config.json 的兼容。
func (r Repo) IsEnabled() bool { return r.Enabled == nil || *r.Enabled }

// Config 是控制面的运行配置。
type Config struct {
	Source      string            `json:"source"`     // 活跃票源：jira / linear（二选一）
	Default     string            `json:"default"`    // 标题无信号时的默认仓
	StatusMap   map[string]string `json:"status_map"` // 任务态→票源流转名（空=不联动；写错会污染真实票，故默认关闭）
	Repos       map[string]Repo   `json:"repos"`
	AgentReview bool              `json:"agent_auto_review,omitempty"` // 塔台自动审核 Issue（无碍自动通过，有碍才要人审）
	TaskModelC  string            `json:"task_model,omitempty"`        // 任务 claude 模型（B/C/D 段；空=CLI 默认）
	AgentModelC string            `json:"agent_model,omitempty"`      // 塔台会话模型（空=CLI 默认）
	Env         map[string]string `json:"-"`                           // 来自 .env / 租户密钥，承载凭据/端口/token 等
	Root        string            `json:"-"`                           // 仓根目录（解析相对路径用）
	TenantID    string            `json:"-"`                           // 非空=按租户解析出的配置；克隆/worktree/日志目录据此命名空间隔离
}

// tenantSub 在 TenantID 非空时把路径下沉到 <base>/<tenantID>，实现按租户隔离。
func (c *Config) tenantSub(base string) string {
	if c.TenantID == "" {
		return base
	}
	return filepath.Join(base, c.TenantID)
}

// Clone 深拷贝可变部分（Repos/StatusMap/Env），供设置 handler 在副本上改，
// 避免与编排器并发读同一缓存实例的 map 而 fatal panic。
func (c *Config) Clone() *Config {
	nc := *c
	nc.Repos = make(map[string]Repo, len(c.Repos))
	for k, v := range c.Repos {
		nc.Repos[k] = v
	}
	nc.StatusMap = make(map[string]string, len(c.StatusMap))
	for k, v := range c.StatusMap {
		nc.StatusMap[k] = v
	}
	nc.Env = make(map[string]string, len(c.Env))
	for k, v := range c.Env {
		nc.Env[k] = v
	}
	return &nc
}

// getCred 取「按租户凭据」：只读该租户 Env，不看进程环境——否则宿主导出的
// GITHUB_TOKEN/ATLASSIAN_* 等会架空所有租户的隔离凭据。
func (c *Config) getCred(key string) string { return c.Env[key] }

// Load 读取仓根下的 config.json 与 .claude/ai-workflow/.env。
// 二者缺失都不致命：返回可用的空配置，由各取值方法兜底。
func Load(root string) (*Config, error) {
	c := &Config{
		Repos: map[string]Repo{},
		Env:   map[string]string{},
		Root:  root,
	}
	cfgPath := filepath.Join(root, "config.json")
	if b, err := os.ReadFile(cfgPath); err == nil {
		if err := json.Unmarshal(b, c); err != nil {
			return nil, err
		}
	}
	for name, r := range c.Repos {
		r.Name = name
		c.Repos[name] = r
	}
	// .env 承载凭据，沿用既有工具目录位置
	c.Env = loadEnv(filepath.Join(root, ".claude", "ai-workflow", ".env"))
	return c, nil
}

// loadEnv 解析 KEY=VALUE 行（去首尾引号），与既有 jira_api.py/server.py 行为一致。
func loadEnv(path string) map[string]string {
	env := map[string]string{}
	b, err := os.ReadFile(path)
	if err != nil {
		return env
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || !strings.Contains(line, "=") {
			continue
		}
		kv := strings.SplitN(line, "=", 2)
		k := strings.TrimSpace(kv[0])
		v := strings.TrimSpace(kv[1])
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
		}
		env[k] = v
	}
	return env
}

// get 返回配置值。优先级：进程环境变量 > .env 文件 > 默认（12-factor，允许部署期覆盖）。
func (c *Config) get(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	if v, ok := c.Env[key]; ok && v != "" {
		return v
	}
	return def
}

// RepoList 返回按名称排序的仓列表，供 API 输出稳定顺序。
// 输出时把 Enabled 归一为非 nil 指针，使前端始终拿到明确的 enabled 布尔。
func (c *Config) RepoList() []Repo {
	out := make([]Repo, 0, len(c.Repos))
	for _, r := range c.Repos {
		en := r.IsEnabled()
		r.Enabled = &en
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// EnabledRepoList 仅返回启用的登记仓，供候选票选仓下拉/路由使用。
func (c *Config) EnabledRepoList() []Repo {
	all := c.RepoList()
	out := make([]Repo, 0, len(all))
	for _, r := range all {
		if r.IsEnabled() {
			out = append(out, r)
		}
	}
	return out
}

// Port 是 HTTP 监听端口。
func (c *Config) Port() int {
	if n, err := strconv.Atoi(c.get("PORT", "8788")); err == nil {
		return n
	}
	return 8788
}

// Host 是 HTTP 监听地址。默认 127.0.0.1（仅本机）；设 0.0.0.0 对局域网开放。
func (c *Config) Host() string { return c.get("HOST", "127.0.0.1") }

// InternalToken 校验 /internal 事件回传（skill → 后端）。
func (c *Config) InternalToken() string { return c.get("INTERNAL_TOKEN", "") }

// JiraScript 是 jira_api.py 的路径（被当作外部工具 subprocess 调用）。
func (c *Config) JiraScript() string {
	return c.get("JIRA_API_SCRIPT", filepath.Join(c.Root, ".claude", "ai-workflow", "jira_api.py"))
}

// JiraProject 是默认 JQL 的项目键（无默认值，由租户在 .env/config 配置 JIRA_PROJECT）。
func (c *Config) JiraProject() string { return c.getCred("JIRA_PROJECT") }

// AtlassianDomain 是 Atlassian 站点域名（如 your-domain.atlassian.net），用于拼 browse 链接。
func (c *Config) AtlassianDomain() string { return c.getCred("ATLASSIAN_DOMAIN") }

// ActiveSource 返回活跃票源（jira / linear），config.json > 环境 > 默认 jira。
func (c *Config) ActiveSource() string {
	if c.Source != "" {
		return c.Source
	}
	return c.get("SOURCE", "jira")
}

// LinearAPIKey 是 Linear Personal API Key。
func (c *Config) LinearAPIKey() string { return c.getCred("LINEAR_API_KEY") }

// LinearTeam 是 Linear 团队 key（如 ENG），可空。
func (c *Config) LinearTeam() string { return c.getCred("LINEAR_TEAM") }

// SuggestRepo 据票标题预选目标仓（移植自旧 server.py 路由）：
// 标题含 [<repo-key>] 显式标记优先；否则按各仓 match 关键词；命中唯一才返回，否则回退默认仓。
func (c *Config) SuggestRepo(title string) string {
	s := strings.ToLower(title)
	var explicit []string
	for name, r := range c.Repos {
		if !r.IsEnabled() {
			continue // 停用仓不参与路由预选
		}
		if strings.Contains(s, "["+strings.ToLower(name)+"]") {
			explicit = append(explicit, name)
		}
	}
	if len(explicit) == 1 {
		return explicit[0]
	}
	if len(explicit) > 1 {
		return "" // 冲突，不预选
	}
	var hits []string
	for name, r := range c.Repos {
		if !r.IsEnabled() {
			continue
		}
		for _, kw := range r.Match {
			if kw != "" && strings.Contains(s, strings.ToLower(kw)) {
				hits = append(hits, name)
				break
			}
		}
	}
	if len(hits) == 1 {
		return hits[0]
	}
	if len(hits) > 1 {
		return ""
	}
	return c.Default
}

// ClaudeBin 是 claude CLI 可执行名。
func (c *Config) ClaudeBin() string { return c.get("CLAUDE_BIN", "claude") }

// WorktreeBase 是各任务临时 worktree 的根目录（按租户隔离）。
func (c *Config) WorktreeBase() string { return c.tenantSub(c.get("WORKTREE_BASE", "/tmp/wf-worktrees")) }

// SlackWebhook 为空则不发 Slack。
func (c *Config) SlackWebhook() string { return c.get("SLACK_WEBHOOK_URL", "") }

// DBPath 是 SQLite 文件路径。
func (c *Config) DBPath() string {
	return c.get("DB_PATH", filepath.Join(c.Root, ".claude", "ai-workflow", "tasks.db"))
}

// EventURLBase 是注入 worktree 的事件回传地址前缀（skill POST 到此）。
func (c *Config) EventURLBase() string {
	return c.get("EVENT_URL_BASE", "http://127.0.0.1:"+strconv.Itoa(c.Port()))
}

// GithubToken 用于自管克隆私有仓与 github API（空则回退 gh 登录态）。
func (c *Config) GithubToken() string { return c.getCred("GITHUB_TOKEN") }

// ReposDir 是自管克隆的根目录（按租户隔离，避免跨司串仓）。
func (c *Config) ReposDir() string {
	return c.tenantSub(c.get("REPOS_DIR", filepath.Join(c.Root, ".claude", "ai-workflow", "repos")))
}

// LogsDir 是各任务 claude 输出日志目录（按租户隔离）。
func (c *Config) LogsDir() string {
	return c.tenantSub(c.get("LOGS_DIR", filepath.Join(c.Root, ".claude", "ai-workflow", "logs")))
}

// MaxConcurrent 是同时跑 claude 的最大任务数（默认 3）。
func (c *Config) MaxConcurrent() int {
	if n, err := strconv.Atoi(c.get("MAX_CONCURRENT", "3")); err == nil && n > 0 {
		return n
	}
	return 3
}

// TaskTimeoutMin 是单个 claude 任务墙钟上限（分钟，默认 60）。
func (c *Config) TaskTimeoutMin() int {
	if n, err := strconv.Atoi(c.get("TASK_TIMEOUT_MIN", "60")); err == nil && n > 0 {
		return n
	}
	return 60
}

// PRPollInterval 是轮询 PR 审查决议的周期（秒，默认 120）。
// 人审是分钟级、手动按钮又能即时触发，故默认 2 分钟即可；改动需重启生效。
func (c *Config) PRPollInterval() time.Duration {
	if n, err := strconv.Atoi(c.get("PR_POLL_INTERVAL", "120")); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	return 120 * time.Second
}

// EnvPath / ConfigPath 是两个可写配置文件路径。
func (c *Config) EnvPath() string {
	return filepath.Join(c.Root, ".claude", "ai-workflow", ".env")
}
func (c *Config) ConfigPath() string { return filepath.Join(c.Root, "config.json") }

// --- M7 调度 Agent ---

// AgentEnabled 是调度 Agent 总开关（默认开）。
func (c *Config) AgentEnabled() bool { return c.get("AGENT_ENABLED", "true") != "false" }

// AgentGeneral 决定塔台是否保留 claude 内建工具（Bash/Read/联网等，≈本地终端会话）。
// 默认开；多租户互不信任的部署可用 AGENT_GENERAL=false 收紧为纯 MCP 沙箱。
func (c *Config) AgentGeneral() bool { return c.get("AGENT_GENERAL", "true") != "false" }

// AgentHandoverH 是塔台交接班的班次时长上限（小时，默认 24）：超过则新开会话轻装上岗。
func (c *Config) AgentHandoverH() int {
	if n, err := strconv.Atoi(c.get("AGENT_HANDOVER_H", "24")); err == nil && n > 0 {
		return n
	}
	return 24
}

// AgentHandoverMsgs 是塔台交接班的班次消息量上限（默认 300）：超过则新开会话。
func (c *Config) AgentHandoverMsgs() int {
	if n, err := strconv.Atoi(c.get("AGENT_HANDOVER_MSGS", "300")); err == nil && n > 0 {
		return n
	}
	return 300
}

// AgentModel 是塔台会话模型：租户配置 > AGENT_MODEL 环境 > 空（CLI 默认）。
func (c *Config) AgentModel() string {
	if c.AgentModelC != "" {
		return c.AgentModelC
	}
	return c.get("AGENT_MODEL", "")
}

// TaskModel 是任务 claude（B/C/D 段）模型：租户配置 > TASK_MODEL 环境 > 空（CLI 默认）。
func (c *Config) TaskModel() string {
	if c.TaskModelC != "" {
		return c.TaskModelC
	}
	return c.get("TASK_MODEL", "")
}

// AgentDir 是 Agent 会话工作目录（空目录，无 CLAUDE.md 干扰；按租户隔离）。
func (c *Config) AgentDir() string { return c.tenantSub(c.get("AGENT_DIR", "/tmp/wf-agent")) }

// AgentIdleMin 是 Agent 会话空闲回收分钟数（默认 30；会话 id 已持久化，回收后可 resume）。
func (c *Config) AgentIdleMin() int {
	if n, err := strconv.Atoi(c.get("AGENT_IDLE_MIN", "30")); err == nil && n > 0 {
		return n
	}
	return 30
}

// AgentReviewRemindH 是人审滞留提醒阈值（小时，默认 24；0=关闭提醒）。
func (c *Config) AgentReviewRemindH() int {
	if n, err := strconv.Atoi(c.get("AGENT_REVIEW_REMIND_H", "24")); err == nil && n >= 0 {
		return n
	}
	return 24
}

// AgentAutoReview 返回本租户是否启用塔台自动审核 Issue（受 Agent 总开关约束）。
// 启用后：Issue 产出 → 塔台先审，无阻碍且选项明确时自动通过继续实装；
// 有真阻碍/选项不明确才上报人工（escalate_review → Slack）。
func (c *Config) AgentAutoReview() bool { return c.AgentReview && c.AgentEnabled() }

// AgentAutoActions 是写工具自动执行白名单（逗号分隔）。人审闸口类工具（approve_issue/approve_pr）
// 属硬性确认制，配置在此也不会放行——例外：启用塔台自动审核后 approve_issue/update_issue 对塔台直通。
func (c *Config) AgentAutoActions() []string {
	raw := c.get("AGENT_AUTO_ACTIONS", "cancel_task,restart_task,request_revise")
	var out []string
	for _, s := range strings.Split(raw, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}
