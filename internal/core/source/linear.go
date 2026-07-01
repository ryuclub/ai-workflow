package source

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
)

var linearIDRE = regexp.MustCompile(`^[A-Z][A-Z0-9]*-\d+$`)

const linearEndpoint = "https://api.linear.app/graphql"

// Linear 通过 GraphQL API 实现 Provider（无既有脚本，纯 Go）。
type Linear struct {
	apiKey string
	team   string // 团队 key（如 ENG），可空（空则不限团队）
	client *http.Client
}

// NewLinear 构造 Linear provider。apiKey 为 Personal API Key。
func NewLinear(apiKey, team string) *Linear {
	return &Linear{apiKey: apiKey, team: team, client: &http.Client{Timeout: 30 * time.Second}}
}

func (l *Linear) Name() string { return "linear" }

func (l *Linear) ValidateID(id string) bool { return linearIDRE.MatchString(id) }

// DefaultQuery：Linear 的过滤在 GraphQL 内表达，这里不用自由查询串。
func (l *Linear) DefaultQuery() string { return "" }

type lnIssue struct {
	Identifier  string `json:"identifier"`
	Title       string `json:"title"`
	Description string `json:"description"`
	URL         string `json:"url"`
	State       struct {
		Name string `json:"name"`
	} `json:"state"`
	Assignee *struct {
		DisplayName string `json:"displayName"`
	} `json:"assignee"`
	Labels struct {
		Nodes []struct {
			Name string `json:"name"`
		} `json:"nodes"`
	} `json:"labels"`
}

type lnResp struct {
	Issues struct {
		Nodes []lnIssue `json:"nodes"`
	} `json:"issues"`
}

// postGraphQL 执行一次 GraphQL（query 或 mutation），返回已校验的 data 原文。
func (l *Linear) postGraphQL(ctx context.Context, query string) (json.RawMessage, error) {
	if l.apiKey == "" {
		return nil, fmt.Errorf("未配置 LINEAR_API_KEY")
	}
	body, _ := json.Marshal(map[string]string{"query": query})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, linearEndpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", l.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := l.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var env struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return nil, fmt.Errorf("解析 Linear 响应失败: %w", err)
	}
	if len(env.Errors) > 0 {
		return nil, fmt.Errorf("Linear GraphQL 错误: %s", env.Errors[0].Message)
	}
	return env.Data, nil
}

func (l *Linear) doGraphQL(ctx context.Context, query string) (*lnResp, error) {
	data, err := l.postGraphQL(ctx, query)
	if err != nil {
		return nil, err
	}
	var r lnResp
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("解析 Linear 工单失败: %w", err)
	}
	return &r, nil
}

func (l *Linear) teamFilter() string {
	if l.team == "" {
		return ""
	}
	return fmt.Sprintf(`team: { key: { eq: "%s" } },`, l.team)
}

func (l *Linear) toTicket(i lnIssue) Ticket {
	t := Ticket{
		Source: "linear", ID: i.Identifier, Title: i.Title, Body: i.Description,
		Status: i.State.Name, URL: i.URL,
	}
	if i.Assignee != nil {
		t.Assignee = i.Assignee.DisplayName
	}
	for _, lab := range i.Labels.Nodes {
		t.Labels = append(t.Labels, lab.Name)
	}
	return t
}

func (l *Linear) List(ctx context.Context, _ string, max int) ([]Ticket, error) {
	q := fmt.Sprintf(`{ issues(first: %d, orderBy: updatedAt,
		filter: { %s state: { type: { nin: ["completed","canceled"] } } }) {
		nodes { identifier title url state { name } assignee { displayName } labels { nodes { name } } }
	} }`, max, l.teamFilter())
	r, err := l.doGraphQL(ctx, q)
	if err != nil {
		return nil, err
	}
	out := make([]Ticket, 0, len(r.Issues.Nodes))
	for _, i := range r.Issues.Nodes {
		out = append(out, l.toTicket(i))
	}
	return out, nil
}

// Transition 经 GraphQL issueUpdate 把工单流转到名为 name 的工作流状态。
// name 取自控制面 status_map 配置，须匹配该 team 的某个 workflow state 名（大小写不敏感）。
func (l *Linear) Transition(ctx context.Context, id, name string) error {
	idx := strings.LastIndex(id, "-")
	if idx < 0 {
		return fmt.Errorf("非法 Linear 标识: %q", id)
	}
	team, num := id[:idx], id[idx+1:]
	// 一次取回 issue UUID、当前状态、以及该 team 的全部工作流状态（用于按名解析 stateId）。
	q := fmt.Sprintf(`{ issues(first:1, filter:{ team:{key:{eq:"%s"}}, number:{eq:%s} }) {
		nodes { id state { name } team { states { nodes { id name } } } }
	} }`, team, num)
	data, err := l.postGraphQL(ctx, q)
	if err != nil {
		return err
	}
	var qr struct {
		Issues struct {
			Nodes []struct {
				ID    string `json:"id"`
				State struct {
					Name string `json:"name"`
				} `json:"state"`
				Team struct {
					States struct {
						Nodes []struct {
							ID   string `json:"id"`
							Name string `json:"name"`
						} `json:"nodes"`
					} `json:"states"`
				} `json:"team"`
			} `json:"nodes"`
		} `json:"issues"`
	}
	if err := json.Unmarshal(data, &qr); err != nil {
		return fmt.Errorf("解析 Linear 工单/状态失败: %w", err)
	}
	if len(qr.Issues.Nodes) == 0 {
		return fmt.Errorf("Linear 工单不存在: %s", id)
	}
	node := qr.Issues.Nodes[0]
	if strings.EqualFold(node.State.Name, name) {
		return nil // 已是目标状态，幂等
	}
	var stateID string
	for _, s := range node.Team.States.Nodes {
		if strings.EqualFold(s.Name, name) {
			stateID = s.ID
			break
		}
	}
	if stateID == "" {
		return fmt.Errorf("Linear team %s 无名为 %q 的工作流状态", team, name)
	}
	m := fmt.Sprintf(`mutation { issueUpdate(id: "%s", input: { stateId: "%s" }) { success } }`, node.ID, stateID)
	_, err = l.postGraphQL(ctx, m)
	return err
}

func (l *Linear) Get(ctx context.Context, id string) (*Ticket, error) {
	idx := strings.LastIndex(id, "-")
	if idx < 0 {
		return nil, fmt.Errorf("非法 Linear 标识: %q", id)
	}
	team, num := id[:idx], id[idx+1:]
	q := fmt.Sprintf(`{ issues(first: 1,
		filter: { team: { key: { eq: "%s" } }, number: { eq: %s } }) {
		nodes { identifier title description url state { name } assignee { displayName } labels { nodes { name } } }
	} }`, team, num)
	r, err := l.doGraphQL(ctx, q)
	if err != nil {
		return nil, err
	}
	if len(r.Issues.Nodes) == 0 {
		return nil, fmt.Errorf("Linear 工单不存在: %s", id)
	}
	t := l.toTicket(r.Issues.Nodes[0])
	return &t, nil
}
