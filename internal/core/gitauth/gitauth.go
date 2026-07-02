// Package gitauth 用 http.extraheader 给 git 注入 GitHub 认证，
// 避免把 token 写进 remote URL / .git/config（防落盘、防跨任务串），
// 并使 worktree 内 skill 发起的 git push/fetch 走「按任务解析出的」令牌。
package gitauth

import "encoding/base64"

// header 返回 GitHub git-over-https 的 Authorization 头值（token 为空返回 ""）。
// 用 base64("x-access-token:"+token)：对 GitHub App 安装令牌与个人 PAT 均适用。
func header(token string) string {
	if token == "" {
		return ""
	}
	return "Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:"+token))
}

// ConfigArgs 返回给单条 git 命令用的 -c 参数（把 extraheader 限定到 github.com）。
// token 为空返回 nil。token 出现在 argv（仅命令存续期间），不落 .git/config。
func ConfigArgs(token string) []string {
	h := header(token)
	if h == "" {
		return nil
	}
	return []string{"-c", "http.https://github.com/.extraheader=" + h}
}

// ConfigEnv 返回让本进程内所有 git 命令对 github.com 认证的环境变量（GIT_CONFIG_*）。
// 用于注入子进程（如 claude worktree），使其内部任意 git push/fetch 走该令牌。token 为空返回 nil。
func ConfigEnv(token string) []string {
	h := header(token)
	if h == "" {
		return nil
	}
	return []string{
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=http.https://github.com/.extraheader",
		"GIT_CONFIG_VALUE_0=" + h,
	}
}

// EnvKeys 是 ConfigEnv 写入的键，供注入前从继承环境中剔除（防与宿主已有值冲突）。
var EnvKeys = []string{"GIT_CONFIG_COUNT", "GIT_CONFIG_KEY_0", "GIT_CONFIG_VALUE_0"}
