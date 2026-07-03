package runner

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ryuclub/ai-workflow/internal/core/config"
	"github.com/ryuclub/ai-workflow/internal/core/gitauth"
	"github.com/ryuclub/ai-workflow/internal/core/store"
)

// Env 向 runner 提供运行期配置与目标仓本地路径（按租户解析，支持自管克隆隔离）。
type Env interface {
	Config(tenantID string) *config.Config
	RepoPath(ctx context.Context, tenantID, github string) (string, error)
	// ClaudeToken / GithubToken 解析该任务应使用的令牌（员工个人 > 租户共享 > 空）。
	ClaudeToken(tenantID, userID string) string
	GithubToken(tenantID, userID string) string
}

// Claude 在目标仓的临时 worktree 内跑 claude -p（真实实现）。
type Claude struct {
	env  Env
	live *Live // 输出直播器（可为 nil：仅落盘不直播）
}

// NewClaude 构造真实 runner。live 可为 nil。
func NewClaude(env Env, live *Live) *Claude { return &Claude{env: env, live: live} }

// RunB 跑「票 → GitHub Issue」。命令按活跃源选择对应 skill。
func (c *Claude) RunB(ctx context.Context, t *store.Task) error {
	var prompt string
	switch t.Source {
	case "jira":
		prompt = "/jira-to-issue " + t.SourceID
	case "linear":
		prompt = "/linear-to-issue " + t.SourceID
	default:
		return fmt.Errorf("未知源 %q，无对应 B skill", t.Source)
	}
	return c.run(ctx, t, prompt, "B")
}

// RunC 跑「已审核 Issue → PR」。需 B 阶段回传的 Issue 编号。
func (c *Claude) RunC(ctx context.Context, t *store.Task) error {
	if t.IssueNum == 0 {
		return fmt.Errorf("缺少 Issue 编号，无法实装")
	}
	return c.run(ctx, t, fmt.Sprintf("/issue-to-pr %d", t.IssueNum), "C")
}

// RunD 跑「按 PR review 意见修订」。需 C 阶段产出的 PR 编号；skill 自行 checkout PR 分支并 push。
func (c *Claude) RunD(ctx context.Context, t *store.Task) error {
	if t.PRNum == 0 {
		return fmt.Errorf("缺少 PR 编号，无法修订")
	}
	return c.run(ctx, t, fmt.Sprintf("/pr-revise %d", t.PRNum), "D")
}

// filterEnv 返回剔除了指定 KEY（大小写敏感，按 KEY= 前缀匹配）的环境副本。
func filterEnv(env []string, drop ...string) []string {
	out := make([]string, 0, len(env))
	for _, e := range env {
		keep := true
		for _, k := range drop {
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

func (c *Claude) run(ctx context.Context, t *store.Task, prompt, label string) error {
	cfg := c.env.Config(t.TenantID)
	repo, ok := cfg.Repos[t.Repo]
	if !ok {
		return fmt.Errorf("未登记的仓：%q", t.Repo)
	}
	repoPath, err := c.env.RepoPath(ctx, t.TenantID, repo.GitHub)
	if err != nil {
		return fmt.Errorf("解析仓本地路径失败（%s）：%w", repo.GitHub, err)
	}
	if !isDir(repoPath) {
		return fmt.Errorf("仓本地路径无效：%q", repoPath)
	}
	base := detectBase(repoPath)
	wt := filepath.Join(cfg.WorktreeBase(), fmt.Sprintf("%s-%s-%d", t.Repo, label, time.Now().UnixNano()))
	if err := os.MkdirAll(cfg.WorktreeBase(), 0o755); err != nil {
		return err
	}
	if out, err := gitC(repoPath, "worktree", "add", "--detach", wt, base); err != nil {
		return fmt.Errorf("worktree add 失败（base=%s）：%v：%s", base, err, out)
	}
	defer gitC(repoPath, "worktree", "remove", "--force", wt)

	// 注入控制面的 .env（凭据）+ B/C skill 包到 worktree。
	// skill 包临时注入而非要求目标仓常驻：跑完随 worktree 清理，任何登记仓开箱即用。
	injectEnv(cfg, wt)
	injectSkills(cfg.Root, wt)

	runCtx, cancel := context.WithTimeout(ctx, time.Duration(cfg.TaskTimeoutMin())*time.Minute)
	defer cancel()
	// stream-json + 翻译器：纯文本模式只在结束时打印最终结果，运行期零输出——
	// 直播/日志毫无进度可看；stream-json 逐事件输出，由 renderWriter 实时转可读行。
	cmdArgs := []string{"-p", prompt, "--dangerously-skip-permissions",
		"--output-format", "stream-json", "--verbose"}
	if m := cfg.TaskModel(); m != "" {
		cmdArgs = append(cmdArgs, "--model", m)
	}
	cmd := exec.CommandContext(runCtx, cfg.ClaudeBin(), cmdArgs...)
	cmd.Dir = wt
	// 注入任务上下文：skill 经 emit-event.sh 用这些把阶段事件 POST 回控制面。
	env := append(os.Environ(),
		"WF_TASK_ID="+t.ID,
		"WF_EVENT_URL="+cfg.EventURLBase()+"/internal/tasks/"+t.ID+"/event",
		"WF_INTERNAL_TOKEN="+cfg.InternalToken(),
		"WF_RUN_GEN="+strconv.Itoa(t.RunGen), // 运行代数：过期代数的回传被丢弃（防孤儿进程串写）
	)
	if repo.Base != "" {
		env = append(env, "WF_PR_BASE="+repo.Base) // 每仓 PR base 覆盖（issue-to-pr 优先取）
	}
	// 登录态令牌（非 API）：解析出租户/用户令牌时，先剔除宿主继承的 Claude 凭据
	// （防跨租户串登录态），再注入本任务令牌；未解析出则原样保留宿主登录态（单机开发回落）。
	if tok := c.env.ClaudeToken(t.TenantID, t.CreatedBy); tok != "" {
		env = filterEnv(env, "CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY")
		env = append(env, "CLAUDE_CODE_OAUTH_TOKEN="+tok)
	}
	// GitHub token（两级：员工个人 > 租户共享）：总是先剔除宿主 GITHUB_TOKEN/GH_TOKEN
	// 及继承的 GIT_CONFIG_*（写能力,防串宿主/跨租户凭据）；解析出则注入——
	// GH_TOKEN 给 gh，GIT_CONFIG_* 给 git push/fetch（经 http.extraheader,故也按该身份走）。
	env = filterEnv(env, "GITHUB_TOKEN", "GH_TOKEN")
	env = filterEnv(env, gitauth.EnvKeys...)
	if tok := c.env.GithubToken(t.TenantID, t.CreatedBy); tok != "" {
		env = append(env, "GITHUB_TOKEN="+tok, "GH_TOKEN="+tok)
		env = append(env, gitauth.ConfigEnv(tok)...)
	}
	cmd.Env = env
	var buf bytes.Buffer
	sink := io.Writer(&buf)
	if c.live != nil { // tee：落盘缓冲 + 塔台实时直播
		lw := c.live.Start(t.ID, t.TenantID, label)
		defer c.live.End(t.ID)
		sink = io.MultiWriter(&buf, lw)
	}
	rw := newRenderWriter(sink) // stream-json → 可读行
	cmd.Stdout = rw
	cmd.Stderr = rw
	err = cmd.Run()
	rw.Flush()
	out := buf.String()
	writeLog(cfg.LogsDir(), t.ID, label, prompt, out) // 全量输出落盘，便于排查

	// claude 未登录时会打印「Not logged in」却仍以退出码 0 退出 —— 必须显式识别，
	// 否则后台环境（够不到 keychain 登录态）会静默「假成功」。（沿用旧 server.py 踩坑）
	if authFailed(out) {
		return fmt.Errorf("claude 未登录/认证失效（须在能访问 keychain 的登录会话内跑，或配 CLAUDE_CODE_OAUTH_TOKEN/ANTHROPIC_API_KEY）：%s", tail(out, 300))
	}
	if err != nil {
		return fmt.Errorf("claude 退出异常：%v：%s", err, tail(out, 300))
	}
	return nil
}

// detectBase 返回建 worktree 的起点 ref。
// 关键：用 origin/HEAD 指向的**远程跟踪引用**（如 origin/main），而非本地分支——
// EnsureLocal 只 fetch（更新 origin/*），从不移动本地分支；用本地分支会拿到首次 clone 时的陈旧代码。
// 远程引用在 fetch 后即最新，worktree add --detach origin/<base> 直接基于最新提交。
func detectBase(repoPath string) string {
	if out, err := gitC(repoPath, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil {
		if ref := strings.TrimSpace(out); ref != "" {
			return ref // 形如 origin/main
		}
	}
	// 兜底：无 origin/HEAD（罕见）时退回本地当前分支（可能非最新）。
	if out, err := gitC(repoPath, "symbolic-ref", "--short", "HEAD"); err == nil {
		if b := strings.TrimSpace(out); b != "" {
			return b
		}
	}
	return "main"
}

func gitC(repoPath string, args ...string) (string, error) {
	full := append([]string{"-C", repoPath}, args...)
	out, err := exec.Command("git", full...).CombinedOutput()
	return string(out), err
}

// injectEnv 把「该租户」的凭据写进 worktree 的 .env，供 skill（jira_api.py 等）读取。
// 按租户注入而非拷全局 .env：避免跨租户串凭据。
func injectEnv(cfg *config.Config, wt string) {
	dst := filepath.Join(wt, ".claude", "ai-workflow", ".env")
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return
	}
	var b strings.Builder
	b.WriteString("# 由控制面按租户注入（勿手改）\n")
	for _, k := range config.CredentialKeys {
		// GitHub token 走进程级两级注入（个人 > 租户），不写进 .env：
		// 否则租户共享令牌明文落文件、且会与进程级个人令牌冲突架空归属。
		if k == "GITHUB_TOKEN" {
			continue
		}
		if v := cfg.Env[k]; v != "" {
			fmt.Fprintf(&b, "%s=%s\n", k, v)
		}
	}
	_ = os.WriteFile(dst, []byte(b.String()), 0o600)
}

// injectSkills 把控制面的 B/C skill 包临时拷进 worktree，使目标仓无需常驻安装。
// 目标仓自身的 CLAUDE.md/guidelines 仍由其 checkout 提供（skill 运行时读）。
func injectSkills(root, wt string) {
	cw := filepath.Join(wt, ".claude")
	// B/D skill 包（票→Issue、按意见修订 PR）；按活跃源选 B skill，但全部注入无副作用。
	for _, name := range []string{"jira-to-issue", "linear-to-issue", "pr-revise"} {
		copyDir(filepath.Join(root, ".claude", "skills", name),
			filepath.Join(cw, "skills", name))
	}
	_ = os.MkdirAll(filepath.Join(cw, "commands"), 0o755)
	copyFile(filepath.Join(root, ".claude", "commands", "issue-to-pr.md"),
		filepath.Join(cw, "commands", "issue-to-pr.md"))
	awf := filepath.Join(cw, "ai-workflow")
	_ = os.MkdirAll(awf, 0o755)
	for _, f := range []string{"jira_api.py", "emit-event.sh", "notify.sh"} {
		copyFile(filepath.Join(root, ".claude", "ai-workflow", f), filepath.Join(awf, f))
	}
	_ = os.Chmod(filepath.Join(awf, "emit-event.sh"), 0o755)
	_ = os.Chmod(filepath.Join(awf, "notify.sh"), 0o755)
}

// copyDir 递归拷贝目录（用于 skill 包注入）。
func copyDir(src, dst string) {
	entries, err := os.ReadDir(src)
	if err != nil {
		return
	}
	_ = os.MkdirAll(dst, 0o755)
	for _, e := range entries {
		s, d := filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())
		if e.IsDir() {
			copyDir(s, d)
		} else {
			copyFile(s, d)
		}
	}
}

func copyFile(src, dst string) {
	in, err := os.Open(src)
	if err != nil {
		return
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return
	}
	defer out.Close()
	_, _ = io.Copy(out, in)
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// writeLog 把单次 claude 运行的命令与全量输出落到 <LogsDir>/<taskID>.<label>.log。
func writeLog(dir, taskID, label, prompt, out string) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	content := fmt.Sprintf("# claude -p %q\n# label=%s\n\n%s", prompt, label, out)
	_ = os.WriteFile(filepath.Join(dir, taskID+"."+label+".log"), []byte(content), 0o644)
}

func authFailed(out string) bool {
	for _, s := range []string{"Not logged in", "Please run /login", "Invalid API key"} {
		if strings.Contains(out, s) {
			return true
		}
	}
	return false
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
