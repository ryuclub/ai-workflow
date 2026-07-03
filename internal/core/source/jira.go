package source

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/ryuclub/ai-workflow/internal/core/jira"
)

var jiraIDRE = regexp.MustCompile(`^[A-Z][A-Z0-9]+-\d+$`)
var digitsRE = regexp.MustCompile(`^\d+$`)

// Jira 把既有 jira_api.py（经 core/jira 客户端）适配为 Provider。
type Jira struct {
	client  *jira.Client
	project string
	domain  string
}

// NewJira 构造 JIRA provider。script 为 jira_api.py 路径，project 为默认 JQL 的项目键，
// domain 为 Atlassian 站点域名（用于拼 browse 链接，如 your-domain.atlassian.net）。
func NewJira(script, project, domain string) *Jira {
	return &Jira{client: jira.New(script), project: project, domain: domain}
}

func (j *Jira) Name() string { return "jira" }

func (j *Jira) ValidateID(id string) bool { return jiraIDRE.MatchString(id) }

func (j *Jira) DefaultQuery() string {
	return fmt.Sprintf("project = %s AND statusCategory != Done ORDER BY updated DESC", j.project)
}

// buildJQL 把用户搜索词翻成 JQL：
//
//	空            → 默认最近列表
//	PROJ-1234     → 按票号精确
//	纯数字 1234   → 补项目前缀按票号
//	像 JQL（含运算符/排序）→ 原样透传（高级用法）
//	其它          → 标题全文检索
func (j *Jira) buildJQL(query string) string {
	q := strings.TrimSpace(query)
	if q == "" {
		return j.DefaultQuery()
	}
	low := strings.ToLower(q)
	if strings.ContainsAny(q, "=~") || strings.Contains(low, "order by") || strings.Contains(low, " and ") {
		return q
	}
	up := strings.ToUpper(q)
	if jiraIDRE.MatchString(up) {
		return "key = " + up
	}
	if digitsRE.MatchString(q) {
		return fmt.Sprintf("key = %s-%s", j.project, q)
	}
	esc := strings.ReplaceAll(q, `"`, `\"`)
	return fmt.Sprintf(`summary ~ "%s" ORDER BY updated DESC`, esc)
}

func (j *Jira) List(ctx context.Context, query string, max int) ([]Ticket, error) {
	items, err := j.client.Search(ctx, j.buildJQL(query), max)
	if err != nil {
		return nil, err
	}
	out := make([]Ticket, 0, len(items))
	for _, it := range items {
		t := Ticket{
			Source: "jira", ID: it.Key, Title: it.Summary,
			Status: it.Status, Labels: it.Labels,
			URL: fmt.Sprintf("https://%s/browse/%s", j.domain, it.Key),
		}
		if it.Assignee != nil {
			t.Assignee = *it.Assignee
		}
		out = append(out, t)
	}
	return out, nil
}

func (j *Jira) Get(ctx context.Context, id string) (*Ticket, error) {
	iss, err := j.client.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	t := &Ticket{
		Source: "jira", ID: iss.Key, Title: iss.Summary, Status: iss.Status,
		Labels: iss.Labels, Body: flattenADF(iss.Description),
		URL: fmt.Sprintf("https://%s/browse/%s", j.domain, iss.Key),
	}
	if iss.Assignee != nil {
		t.Assignee = *iss.Assignee
	}
	return t, nil
}

// Transition 经 jira_api.py 流转工单状态。
func (j *Jira) Transition(ctx context.Context, id, name string) error {
	return j.client.Transition(ctx, id, name)
}

func (j *Jira) Transitions(ctx context.Context, id string) ([]string, error) {
	return j.client.Transitions(ctx, id)
}

// flattenADF 把 JIRA 的 ADF（Atlassian Document Format）JSON 递归抽取为可读纯文本。
// 仅用于 Dashboard 预览；权威的 ADF 解析仍由 worktree 内的 jira_api.py/skill 负责。
func flattenADF(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var node any
	if err := json.Unmarshal(raw, &node); err != nil {
		return ""
	}
	var sb strings.Builder
	var walk func(n any)
	walk = func(n any) {
		switch v := n.(type) {
		case map[string]any:
			if txt, ok := v["text"].(string); ok {
				sb.WriteString(txt)
			}
			if v["type"] == "paragraph" || v["type"] == "heading" {
				defer sb.WriteString("\n")
			}
			if content, ok := v["content"].([]any); ok {
				for _, c := range content {
					walk(c)
				}
			}
		case []any:
			for _, c := range v {
				walk(c)
			}
		}
	}
	walk(node)
	return strings.TrimSpace(sb.String())
}
