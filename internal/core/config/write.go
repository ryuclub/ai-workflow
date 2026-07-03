package config

import (
	"encoding/json"
	"os"
	"strings"
)

// SetEnv 把若干键写入 .env：已存在的行就地替换，新键追加，其余行（含注释）保留。
// 值为空字符串表示「不改该键」（避免清空已有凭据）。
func (c *Config) SetEnv(kv map[string]string) error {
	path := c.EnvPath()
	var lines []string
	if b, err := os.ReadFile(path); err == nil {
		lines = strings.Split(string(b), "\n")
	}
	done := map[string]bool{}
	for i, line := range lines {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") || !strings.Contains(t, "=") {
			continue
		}
		key := strings.TrimSpace(strings.SplitN(t, "=", 2)[0])
		if v, ok := kv[key]; ok && v != "" {
			lines[i] = key + "=" + v
			done[key] = true
		}
	}
	for k, v := range kv {
		if v != "" && !done[k] {
			lines = append(lines, k+"="+v)
		}
	}
	out := strings.Join(lines, "\n")
	if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	if err := os.WriteFile(path, []byte(out), 0o600); err != nil {
		return err
	}
	// 同步内存层，供本次 Reload 前的即时读取
	for k, v := range kv {
		if v != "" {
			c.Env[k] = v
		}
	}
	return nil
}

// configFile 是 config.json 的可序列化形态。
type configFile struct {
	Source      string            `json:"source,omitempty"`
	Default     string            `json:"default,omitempty"`
	StatusMap   map[string]string `json:"status_map,omitempty"`
	Repos       map[string]Repo   `json:"repos"`
	AgentReview bool              `json:"agent_auto_review,omitempty"`
	TaskModel   string            `json:"task_model,omitempty"`
	AgentModel  string            `json:"agent_model,omitempty"`
}

// SaveConfigJSON 把 source/default/status_map/repos 持久化回 config.json。
func (c *Config) SaveConfigJSON() error {
	repos := map[string]Repo{}
	for name, r := range c.Repos {
		r.Name = "" // name 由 map key 承载，不重复写
		repos[name] = r
	}
	cf := configFile{Source: c.Source, Default: c.Default, StatusMap: c.StatusMap, Repos: repos, AgentReview: c.AgentReview, TaskModel: c.TaskModelC, AgentModel: c.AgentModelC}
	b, err := json.MarshalIndent(cf, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(c.ConfigPath(), append(b, '\n'), 0o644)
}
