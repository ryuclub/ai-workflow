package config

import "encoding/json"

// CredentialKeys 是「按租户」的机密/凭据键：不从全局 .env 继承，只来自该租户的加密凭据。
// 运行期操作键（PORT/WORKTREE_BASE/INTERNAL_TOKEN/CLAUDE_BIN 等）仍全局共享。
// SLACK_WEBHOOK_URL 暂为全局（事件总线单份消费），不列入——避免造成「已按租户配置」的错觉。
var CredentialKeys = []string{
	"ATLASSIAN_USERNAME", "ATLASSIAN_API_KEY", "ATLASSIAN_DOMAIN", "JIRA_PROJECT",
	"LINEAR_API_KEY", "LINEAR_TEAM", "GITHUB_TOKEN",
}

// FromTenant 据全局模板 base + 该租户配置 JSON + 该租户凭据构造租户级 Config。
// tenantJSON 为空表示该租户尚无独立配置：继承 base 的 source/default/status_map/repos 作默认。
func FromTenant(base *Config, tenantID, tenantJSON string, creds map[string]string) (*Config, error) {
	c := &Config{
		Root:      base.Root,
		TenantID:  tenantID,
		Env:       map[string]string{},
		Repos:     map[string]Repo{},
		StatusMap: map[string]string{},
	}
	// 继承全局运行期 Env，但剥掉凭据键（防跨租户串凭据）。
	for k, v := range base.Env {
		c.Env[k] = v
	}
	for _, k := range CredentialKeys {
		delete(c.Env, k)
	}
	// 叠加该租户凭据。
	for k, v := range creds {
		if v != "" {
			c.Env[k] = v
		}
	}
	// 配置：有租户 JSON 用之，否则继承 base 模板。
	if tenantJSON != "" {
		var cf configFile
		if err := json.Unmarshal([]byte(tenantJSON), &cf); err != nil {
			return nil, err
		}
		c.Source = cf.Source
		c.Default = cf.Default
		c.AgentReview = cf.AgentReview
		c.TaskModelC = cf.TaskModel
		c.AgentModelC = cf.AgentModel
		if cf.StatusMap != nil {
			c.StatusMap = cf.StatusMap
		}
		for name, r := range cf.Repos {
			r.Name = name
			c.Repos[name] = r
		}
	} else {
		c.Source = base.Source
		c.Default = base.Default
		c.AgentReview = base.AgentReview
		c.TaskModelC = base.TaskModelC
		c.AgentModelC = base.AgentModelC
		for k, v := range base.StatusMap {
			c.StatusMap[k] = v
		}
		for name, r := range base.Repos {
			c.Repos[name] = r
		}
	}
	return c, nil
}

// ToTenantJSON 序列化该租户的非机密配置（source/default/status_map/repos），供落库。
func (c *Config) ToTenantJSON() (string, error) {
	repos := map[string]Repo{}
	for name, r := range c.Repos {
		r.Name = "" // name 由 map key 承载
		repos[name] = r
	}
	cf := configFile{Source: c.Source, Default: c.Default, StatusMap: c.StatusMap, Repos: repos, AgentReview: c.AgentReview, TaskModel: c.TaskModelC, AgentModel: c.AgentModelC}
	b, err := json.Marshal(cf)
	return string(b), err
}
