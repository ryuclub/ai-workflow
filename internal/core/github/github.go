// Package github 是对 gh CLI 的薄封装：取/改 Issue 与切换 label。
// 凭据沿用 gh 登录态（token 留在后端，浏览器不碰）。
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Label 是 Issue 上的标签。
type Label struct {
	Name string `json:"name"`
}

// Issue 是 gh issue view 的精简投影。
type Issue struct {
	Number int     `json:"number"`
	Title  string  `json:"title"`
	Body   string  `json:"body"`
	URL    string  `json:"url"`
	State  string  `json:"state"`
	Labels []Label `json:"labels"`
}

// LabelNames 返回标签名列表，便于前端/编排判断状态。
func (i *Issue) LabelNames() []string {
	out := make([]string, len(i.Labels))
	for idx, l := range i.Labels {
		out[idx] = l.Name
	}
	return out
}

// Client 通过 gh CLI 操作某个仓。
type Client struct {
	GH    string // 默认 gh
	Token string // 非空则经 GH_TOKEN 注入给 gh（否则用 gh 登录态）
}

// New 构造客户端。可经 GH_BIN 覆盖可执行（测试/特殊环境用）。
func New() *Client { return NewWithToken("") }

// NewWithToken 构造带 token 的客户端。
func NewWithToken(token string) *Client {
	bin := os.Getenv("GH_BIN")
	if bin == "" {
		bin = "gh"
	}
	return &Client{GH: bin, Token: token}
}

func (c *Client) run(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, c.GH, args...)
	if c.Token != "" {
		cmd.Env = append(os.Environ(), "GH_TOKEN="+c.Token)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("gh %v 失败: %v: %s", args, err, stderr.String())
	}
	return stdout.Bytes(), nil
}

// GetIssue 读取 Issue（编号、标题、正文 markdown、标签、URL）。
func (c *Client) GetIssue(ctx context.Context, repo string, num int) (*Issue, error) {
	out, err := c.run(ctx, "issue", "view", fmt.Sprint(num), "-R", repo,
		"--json", "number,title,body,labels,url,state")
	if err != nil {
		return nil, err
	}
	var iss Issue
	if err := json.Unmarshal(out, &iss); err != nil {
		return nil, fmt.Errorf("解析 gh issue view 输出失败: %v", err)
	}
	return &iss, nil
}

// UpdateIssue 改写 Issue 标题与正文（人审就地编辑后写穿透）。
func (c *Client) UpdateIssue(ctx context.Context, repo string, num int, title, body string) error {
	_, err := c.run(ctx, "issue", "edit", fmt.Sprint(num), "-R", repo,
		"--title", title, "--body", body)
	return err
}

// PRView 读取 PR 的标题与正文（塔台审查 PR 用）。
func (c *Client) PRView(ctx context.Context, repo string, num int) (title, body string, err error) {
	out, err := c.run(ctx, "pr", "view", fmt.Sprint(num), "-R", repo, "--json", "title,body")
	if err != nil {
		return "", "", err
	}
	var v struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return "", "", fmt.Errorf("解析 gh pr view 输出失败: %v", err)
	}
	return v.Title, v.Body, nil
}

// PRDiff 读取 PR 的完整 diff（塔台审查 PR 用；调用方自行截断）。
func (c *Client) PRDiff(ctx context.Context, repo string, num int) (string, error) {
	out, err := c.run(ctx, "pr", "diff", fmt.Sprint(num), "-R", repo)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// WhoAmI 返回当前 gh 认证的登录名（连接测试用）。
func (c *Client) WhoAmI(ctx context.Context) (string, error) {
	out, err := c.run(ctx, "api", "user", "--jq", ".login")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// RepoExists 校验对某仓的可访问性（加仓时验证）。
func (c *Client) RepoExists(ctx context.Context, full string) error {
	_, err := c.run(ctx, "repo", "view", full, "--json", "name")
	return err
}

// RepoBrief 是列举 owner 仓库时的精简投影。
type RepoBrief struct {
	Name          string `json:"name"`          // 短名（如 example-channel-service）
	NameWithOwner string `json:"nameWithOwner"` // owner/repo
	IsArchived    bool   `json:"isArchived"`
}

// ListOwnerRepos 列出某 owner（用户或组织）下可访问的仓库（用于「一键导入全部仓」）。
// 已归档的仓一并返回，由上层决定是否过滤。
func (c *Client) ListOwnerRepos(ctx context.Context, owner string) ([]RepoBrief, error) {
	out, err := c.run(ctx, "repo", "list", owner, "--no-archived",
		"--limit", "500", "--json", "name,nameWithOwner,isArchived")
	if err != nil {
		return nil, err
	}
	var repos []RepoBrief
	if err := json.Unmarshal(out, &repos); err != nil {
		return nil, fmt.Errorf("解析 gh repo list 输出失败: %v", err)
	}
	return repos, nil
}

// PRReview 是 PR 审查态的精简投影，供控制面轮询判定「通过 / 需修订 / 仍等待」。
type PRReview struct {
	Decision string    // APPROVED / CHANGES_REQUESTED / REVIEW_REQUIRED / ""（未决）
	LatestAt time.Time // 最新一条「决定性」review（APPROVED/CHANGES_REQUESTED）的提交时间，作幂等游标
	State    string    // PR 自身状态：OPEN / MERGED / CLOSED
}

// GetPRReview 读取 PR 的审查决议与最新决定性 review 时间。
// Decision 优先取 GitHub 聚合的 reviewDecision；仓库未设 required review 时其可能为空，
// 此时退回「最新一条决定性 review 的状态」兜底。
// LatestAt 仅计决定性 review：纯 COMMENTED 评论不推进游标，避免误触发修订。
func (c *Client) GetPRReview(ctx context.Context, repo string, num int) (*PRReview, error) {
	out, err := c.run(ctx, "pr", "view", fmt.Sprint(num), "-R", repo,
		"--json", "reviewDecision,state,reviews")
	if err != nil {
		return nil, err
	}
	var raw struct {
		ReviewDecision string `json:"reviewDecision"`
		State          string `json:"state"`
		Reviews        []struct {
			State       string    `json:"state"`
			SubmittedAt time.Time `json:"submittedAt"`
		} `json:"reviews"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("解析 gh pr view 输出失败: %v", err)
	}
	r := &PRReview{Decision: raw.ReviewDecision, State: raw.State}
	var latestDecisive string
	for _, rv := range raw.Reviews {
		if rv.State != "APPROVED" && rv.State != "CHANGES_REQUESTED" {
			continue // COMMENTED / DISMISSED / PENDING 不计入游标与兜底决议
		}
		if rv.SubmittedAt.After(r.LatestAt) {
			r.LatestAt = rv.SubmittedAt
			latestDecisive = rv.State
		}
	}
	if r.Decision == "" {
		r.Decision = latestDecisive
	}
	return r, nil
}

// EnsureLabels 确保仓库存在这些标签（不存在则创建）。幂等：已存在视为成功、不改其颜色。
// 状态标签（待审核/已审核/…）在新仓可能尚未创建，直接 --add-label 会失败，故切标签前先 ensure。
func (c *Client) EnsureLabels(ctx context.Context, repo string, names []string) error {
	for _, n := range names {
		if n == "" {
			continue
		}
		if _, err := c.run(ctx, "label", "create", n, "-R", repo, "--description", "AI 工作流状态标签"); err != nil {
			msg := strings.ToLower(err.Error())
			// 已存在两种措辞（REST / GraphQL）均视为成功。
			if strings.Contains(msg, "already exists") || strings.Contains(msg, "already been taken") {
				continue
			}
			return err
		}
	}
	return nil
}

// PRRef 标识一个待批量查询的 PR。Key 由调用方自定义（如 taskID），用于回取结果。
type PRRef struct {
	Key  string
	Repo string // owner/repo
	Num  int
}

// GetPRReviewsBatch 用一次 GraphQL 查询多个（可跨仓）PR 的审查决议，返回 Key→PRReview。
// 单次 gh 调用替代 N 次 gh pr view，显著降请求量。个别 PR 查询失败/不存在则在结果里缺席（调用方跳过）。
func (c *Client) GetPRReviewsBatch(ctx context.Context, refs []PRRef) (map[string]*PRReview, error) {
	if len(refs) == 0 {
		return map[string]*PRReview{}, nil
	}
	var sb strings.Builder
	sb.WriteString("query {")
	for i, r := range refs {
		owner, name := splitRepo(r.Repo)
		fmt.Fprintf(&sb, " a%d: repository(owner:%q, name:%q){ pullRequest(number:%d){ reviewDecision state reviews(last:50){ nodes{ state submittedAt } } } }",
			i, owner, name, r.Num)
	}
	sb.WriteString(" }")
	out, err := c.run(ctx, "api", "graphql", "-f", "query="+sb.String())
	if err != nil {
		return nil, err
	}
	var resp struct {
		Data map[string]struct {
			PullRequest *struct {
				ReviewDecision string `json:"reviewDecision"`
				State          string `json:"state"`
				Reviews        struct {
					Nodes []struct {
						State       string    `json:"state"`
						SubmittedAt time.Time `json:"submittedAt"`
					} `json:"nodes"`
				} `json:"reviews"`
			} `json:"pullRequest"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, fmt.Errorf("解析 graphql 批量 review 失败: %v", err)
	}
	res := make(map[string]*PRReview, len(refs))
	for i, r := range refs {
		node, ok := resp.Data[fmt.Sprintf("a%d", i)]
		if !ok || node.PullRequest == nil {
			continue // 该 PR 查询失败/不存在
		}
		pr := &PRReview{Decision: node.PullRequest.ReviewDecision, State: node.PullRequest.State}
		var latestDecisive string
		for _, rv := range node.PullRequest.Reviews.Nodes {
			if rv.State != "APPROVED" && rv.State != "CHANGES_REQUESTED" {
				continue
			}
			if rv.SubmittedAt.After(pr.LatestAt) {
				pr.LatestAt = rv.SubmittedAt
				latestDecisive = rv.State
			}
		}
		if pr.Decision == "" {
			pr.Decision = latestDecisive
		}
		res[r.Key] = pr
	}
	return res, nil
}

func splitRepo(full string) (owner, name string) {
	if i := strings.IndexByte(full, '/'); i >= 0 {
		return full[:i], full[i+1:]
	}
	return full, ""
}

// SetLabels 增删标签（如 待审核→已审核）。空切片表示不操作该方向。
// 对将要新增的标签先 ensure 存在，避免目标仓缺该标签时 --add-label 直接失败。
func (c *Client) SetLabels(ctx context.Context, repo string, num int, add, remove []string) error {
	if len(add) == 0 && len(remove) == 0 {
		return nil
	}
	if err := c.EnsureLabels(ctx, repo, add); err != nil {
		return err
	}
	args := []string{"issue", "edit", fmt.Sprint(num), "-R", repo}
	for _, l := range add {
		args = append(args, "--add-label", l)
	}
	for _, l := range remove {
		args = append(args, "--remove-label", l)
	}
	_, err := c.run(ctx, args...)
	return err
}
