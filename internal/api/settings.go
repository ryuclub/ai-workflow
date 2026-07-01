package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ryuclub/ai-workflow/internal/core/config"
	"github.com/gin-gonic/gin"
)

// settingsResp 是设置的对外视图：密钥只回「是否已设」，不回明文。
type settingsResp struct {
	Source         string            `json:"source"`
	JiraDomain     string            `json:"jira_domain"`
	JiraUser       string            `json:"jira_user"`
	JiraProject    string            `json:"jira_project"`
	JiraKeySet     bool              `json:"jira_api_key_set"`
	LinearTeam     string            `json:"linear_team"`
	LinearKeySet   bool              `json:"linear_api_key_set"`
	GithubTokenSet bool              `json:"github_token_set"`
	AdminTokenSet  bool              `json:"admin_token_set"`
	StatusMap      map[string]string `json:"status_map"`
	MaxConcurrent  int               `json:"max_concurrent"`
	TaskTimeoutMin int               `json:"task_timeout_min"`
}

// settingsReq：空字符串=不改（避免清空密钥）；StatusMap 非 nil=整体替换。
type settingsReq struct {
	Source         string            `json:"source"`
	JiraDomain     string            `json:"jira_domain"`
	JiraUser       string            `json:"jira_user"`
	JiraProject    string            `json:"jira_project"`
	JiraAPIKey     string            `json:"jira_api_key"`
	LinearTeam     string            `json:"linear_team"`
	LinearAPIKey   string            `json:"linear_api_key"`
	GithubToken    string            `json:"github_token"`
	AdminToken     string            `json:"admin_token"`
	StatusMap      map[string]string `json:"status_map"`
	MaxConcurrent  int               `json:"max_concurrent"`   // 0=不改
	TaskTimeoutMin int               `json:"task_timeout_min"` // 0=不改
}

// GET /api/v1/settings
func (s *Server) getSettings(c *gin.Context) {
	cfg := s.cfg()
	c.JSON(http.StatusOK, settingsResp{
		Source:         cfg.ActiveSource(),
		JiraDomain:     cfg.Env["ATLASSIAN_DOMAIN"],
		JiraUser:       cfg.Env["ATLASSIAN_USERNAME"],
		JiraProject:    cfg.JiraProject(),
		JiraKeySet:     cfg.Env["ATLASSIAN_API_KEY"] != "",
		LinearTeam:     cfg.LinearTeam(),
		LinearKeySet:   cfg.LinearAPIKey() != "",
		GithubTokenSet: cfg.GithubToken() != "",
		AdminTokenSet:  cfg.AdminToken() != "",
		StatusMap:      cfg.StatusMap,
		MaxConcurrent:  cfg.MaxConcurrent(),
		TaskTimeoutMin: cfg.TaskTimeoutMin(),
	})
}

// PUT /api/v1/settings
func (s *Server) putSettings(c *gin.Context) {
	var req settingsReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	cfg := s.cfg()
	// 凭据/标量写 .env（空=不改）
	env := map[string]string{
		"ATLASSIAN_DOMAIN":   req.JiraDomain,
		"ATLASSIAN_USERNAME": req.JiraUser,
		"JIRA_PROJECT":       req.JiraProject,
		"ATLASSIAN_API_KEY":  req.JiraAPIKey,
		"LINEAR_TEAM":        req.LinearTeam,
		"LINEAR_API_KEY":     req.LinearAPIKey,
		"GITHUB_TOKEN":       req.GithubToken,
		"ADMIN_TOKEN":        req.AdminToken,
	}
	if req.MaxConcurrent > 0 {
		env["MAX_CONCURRENT"] = strconv.Itoa(req.MaxConcurrent)
	}
	if req.TaskTimeoutMin > 0 {
		env["TASK_TIMEOUT_MIN"] = strconv.Itoa(req.TaskTimeoutMin)
	}
	if err := cfg.SetEnv(env); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "写 .env 失败：" + err.Error()})
		return
	}
	// source / status_map 写 config.json
	if req.Source != "" {
		if req.Source != "jira" && req.Source != "linear" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "source 仅支持 jira / linear"})
			return
		}
		cfg.Source = req.Source
	}
	if req.StatusMap != nil {
		cfg.StatusMap = req.StatusMap
	}
	if err := cfg.SaveConfigJSON(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "写 config.json 失败：" + err.Error()})
		return
	}
	if err := s.deps.Reload(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "重载失败：" + err.Error()})
		return
	}
	s.getSettings(c)
}

// POST /api/v1/settings/test/:kind  — kind: source | github
func (s *Server) testConnection(c *gin.Context) {
	ctx, cancel := contextWithTimeout(c, 30*time.Second)
	defer cancel()
	switch c.Param("kind") {
	case "source":
		items, err := s.src().List(ctx, "", 1)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"ok": false, "error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true, "detail": "票源连通，示例返回 " + strconv.Itoa(len(items)) + " 条"})
	case "github":
		login, err := s.gh().WhoAmI(ctx)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"ok": false, "error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true, "detail": "GitHub 已认证：" + login})
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "未知测试类型"})
	}
}

// GET /api/v1/settings/repos
func (s *Server) listSettingRepos(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"default": s.cfg().Default, "repos": s.cfg().RepoList()})
}

type repoReq struct {
	Name    string   `json:"name" binding:"required"`
	GitHub  string   `json:"github" binding:"required"`
	Match   []string `json:"match"`
	Path    string   `json:"path"`    // 可选：填了用现成本地克隆，否则自管
	Base    string   `json:"base"`    // 可选：PR base 覆盖
	Enabled *bool    `json:"enabled"` // 可选：nil=沿用原值（新仓则视为启用）
}

// PUT /api/v1/settings/repos — 新增/更新一个登记仓（按 GitHub 地址）。
func (s *Server) upsertRepo(c *gin.Context) {
	var req repoReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if !strings.Contains(req.GitHub, "/") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "github 应为 owner/repo 形式"})
		return
	}
	// 校验可访问性（避免登记一个够不到的仓）
	ctx, cancel := contextWithTimeout(c, 20*time.Second)
	defer cancel()
	if err := s.gh().RepoExists(ctx, req.GitHub); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "无法访问该仓（检查地址/权限/GitHub token）：" + err.Error()})
		return
	}
	cfg := s.cfg()
	repo := config.Repo{Name: req.Name, GitHub: req.GitHub, Path: req.Path, Match: req.Match, Base: req.Base}
	switch {
	case req.Enabled != nil:
		repo.Enabled = req.Enabled
	default:
		if ex, ok := cfg.Repos[req.Name]; ok {
			repo.Enabled = ex.Enabled // 编辑既有仓：未指定则保留原启用态
		}
	}
	cfg.Repos[req.Name] = repo
	if cfg.Default == "" {
		cfg.Default = req.Name
	}
	if err := cfg.SaveConfigJSON(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	_ = s.deps.Reload()
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// PUT /api/v1/settings/repos/:name/enabled — 切换某登记仓的启用态。
func (s *Server) setRepoEnabled(c *gin.Context) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	cfg := s.cfg()
	name := c.Param("name")
	r, ok := cfg.Repos[name]
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "无此登记仓：" + name})
		return
	}
	en := req.Enabled
	r.Enabled = &en
	cfg.Repos[name] = r
	if err := cfg.SaveConfigJSON(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	_ = s.deps.Reload()
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// POST /api/v1/settings/repos/import — 拉取某 owner（用户/组织）下全部仓库并登记，
// 默认「不启用」；按 GitHub 地址对既有登记去重。owner 留空时按既有登记仓推断，再退回当前 gh 登录账号。
func (s *Server) importRepos(c *gin.Context) {
	var req struct {
		Owner string `json:"owner"`
	}
	_ = c.ShouldBindJSON(&req)
	cfg := s.cfg()
	ctx, cancel := contextWithTimeout(c, 60*time.Second)
	defer cancel()

	owner := strings.TrimSpace(req.Owner)
	if owner == "" {
		owner = inferOwner(cfg) // 从既有登记仓推断
	}
	if owner == "" {
		login, err := s.gh().WhoAmI(ctx)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "无法确定 owner，且获取 gh 登录账号失败：" + err.Error()})
			return
		}
		owner = login
	}

	repos, err := s.gh().ListOwnerRepos(ctx, owner)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "拉取仓库列表失败（检查 owner/权限）：" + err.Error()})
		return
	}

	existing := map[string]bool{}
	for _, r := range cfg.Repos {
		existing[strings.ToLower(r.GitHub)] = true
	}
	added := 0
	for _, rb := range repos {
		if existing[strings.ToLower(rb.NameWithOwner)] {
			continue // 已登记，跳过
		}
		key := uniqueRepoKey(cfg, rb.Name)
		disabled := false
		cfg.Repos[key] = config.Repo{Name: key, GitHub: rb.NameWithOwner, Enabled: &disabled}
		existing[strings.ToLower(rb.NameWithOwner)] = true
		added++
	}
	if added > 0 {
		if err := cfg.SaveConfigJSON(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		_ = s.deps.Reload()
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "owner": owner, "found": len(repos), "added": added, "skipped": len(repos) - added})
}

// inferOwner 从既有登记仓的 GitHub 地址推断 owner（取出现次数最多者）。
func inferOwner(cfg *config.Config) string {
	count := map[string]int{}
	best, bestN := "", 0
	for _, r := range cfg.Repos {
		if i := strings.Index(r.GitHub, "/"); i > 0 {
			o := r.GitHub[:i]
			count[o]++
			if count[o] > bestN {
				best, bestN = o, count[o]
			}
		}
	}
	return best
}

// uniqueRepoKey 由仓短名生成一个不与既有登记键冲突的键。
func uniqueRepoKey(cfg *config.Config, name string) string {
	base := strings.ToLower(strings.TrimSpace(name))
	base = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '-'
		}
	}, base)
	if base == "" {
		base = "repo"
	}
	key := base
	for i := 2; ; i++ {
		if _, taken := cfg.Repos[key]; !taken {
			return key
		}
		key = base + "-" + strconv.Itoa(i)
	}
}

// DELETE /api/v1/settings/repos/:name
func (s *Server) deleteRepo(c *gin.Context) {
	cfg := s.cfg()
	name := c.Param("name")
	delete(cfg.Repos, name)
	if cfg.Default == name {
		cfg.Default = ""
	}
	if err := cfg.SaveConfigJSON(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	_ = s.deps.Reload()
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
