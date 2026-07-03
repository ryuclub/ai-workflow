// Package pipeline 定义流水线的 DAG 数据模型。
// 当前内置一条线性流水线（8 节点），但数据结构按 DAG 建模，
// 预留后续「通用节点执行引擎」让编辑真正生效。
package pipeline

// NodeKind 区分自动执行节点与人审闸口。
type NodeKind string

const (
	KindAuto     NodeKind = "auto"     // 由 claude -p 跑 skill
	KindGate     NodeKind = "gate"     // 人审闸口，流水线暂停等审批
	KindTerminal NodeKind = "terminal" // 终点：按任务终态点亮（完成/无需处理/待裁决）
)

// Node 是流水线中的一个阶段或终点。
type Node struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Kind      NodeKind `json:"kind"`
	X         int      `json:"x"`                   // 布局列
	Y         int      `json:"y"`                   // 布局行
	Skill     string   `json:"skill,omitempty"`     // 该节点所属 skill（B/C/D）
	PhaseKeys []string `json:"phaseKeys,omitempty"` // skill 上报的 phase 标识 → 归到本节点
	// 连线 handle 朝向（前端画图用）：left/right/top/bottom。空则默认 target=left、source=right（横向流）。
	// U 形折行时靠它让折角走垂直、第二行走「右进左出」。
	SourcePos string `json:"sourcePos,omitempty"`
	TargetPos string `json:"targetPos,omitempty"`
}

// Edge 是节点间的有向连接（from → to）。
type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Pipeline 是节点 + 连线的有向图。
type Pipeline struct {
	ID    string `json:"id"`
	Nodes []Node `json:"nodes"`
	Edges []Edge `json:"edges"`
}

// 节点 ID 常量，供 orchestrator/runner 与事件映射引用。
const (
	NodeReadJira    = "read_jira"
	NodeInvestigate = "investigate"
	NodeCreateIssue = "create_issue"
	NodeAgentReview = "agent_review" // 塔台自动审核（仅启用该功能的租户出现在图中）
	NodeReview      = "review"       // 人审闸口（Issue）
	NodeBlueprint   = "blueprint"
	NodeImplement   = "implement"
	NodeTest        = "test"
	NodePR          = "pr"
	NodePRReview    = "pr_review" // PR 审查闸口（人审 PR，可多轮）
	NodeRevise      = "revise"    // 按 review 意见修订 PR（D 阶段）
	// 终点节点
	NodeDone    = "done"    // 正常完成（PR approved）
	NodeSkipped = "skipped" // 正常无需处理
	NodeFailed  = "failed"  // 异常 → 待裁决
)

// Default 返回全量流水线（含塔台审核节点；orchestrator 初始化节点运行态用超集）。
// 前端画图请用 ForTenant：未启用塔台审核的租户不出现该节点。
func Default() Pipeline { return build(true) }

// ForTenant 按租户功能开关返回流水线视图。
func ForTenant(agentReview bool) Pipeline { return build(agentReview) }

// build 构造内置流水线：JIRA→建Issue→[塔台审核]→人审→实装→测试→PR→PR审查(可多轮修订)，外加三个终点。
func build(agentReview bool) Pipeline {
	// s 是塔台审核节点带来的第一行横向偏移（含审核节点时后续列右移一格）。
	s := 0
	if agentReview {
		s = 1
	}
	// U 形折行：第一行（y=0）左→右到「建蓝图」，折角垂直下降，第二行（y=2）右→左到「完成」。
	nodes := []Node{
		// —— 第一行 y=0（B 段 + 审核 + 建蓝图），左→右 ——
		{ID: NodeReadJira, Name: "读取 JIRA", Kind: KindAuto, X: 0, Y: 0, Skill: "jira-to-issue", PhaseKeys: []string{"B.read"}},
		{ID: NodeInvestigate, Name: "调查现状", Kind: KindAuto, X: 1, Y: 0, Skill: "jira-to-issue", PhaseKeys: []string{"B.investigate"}},
		{ID: NodeCreateIssue, Name: "分析建 Issue", Kind: KindAuto, X: 2, Y: 0, Skill: "jira-to-issue", PhaseKeys: []string{"B.issue"}},
	}
	if agentReview {
		nodes = append(nodes, Node{ID: NodeAgentReview, Name: "塔台审核", Kind: KindAuto, X: 3, Y: 0})
	}
	nodes = append(nodes,
		Node{ID: NodeReview, Name: "人工审核", Kind: KindGate, X: 3 + s, Y: 0},
		Node{ID: NodeBlueprint, Name: "建分支+蓝图", Kind: KindAuto, X: 4 + s, Y: 0, Skill: "issue-to-pr", PhaseKeys: []string{"C.blueprint"}, SourcePos: "bottom"}, // 折角：向下拐
		// —— 第二行 y=2（C 段 + PR 审查），右→左 ——
		Node{ID: NodeImplement, Name: "实装+自查", Kind: KindAuto, X: 4 + s, Y: 2, Skill: "issue-to-pr", PhaseKeys: []string{"C.implement"}, TargetPos: "top", SourcePos: "left"}, // 折角：从上接
		Node{ID: NodeTest, Name: "测试", Kind: KindAuto, X: 3 + s, Y: 2, Skill: "issue-to-pr", PhaseKeys: []string{"C.test"}, TargetPos: "right", SourcePos: "left"},
		Node{ID: NodePR, Name: "提 PR+回写", Kind: KindAuto, X: 2 + s, Y: 2, Skill: "issue-to-pr", PhaseKeys: []string{"C.pr"}, TargetPos: "right", SourcePos: "left"},
		Node{ID: NodePRReview, Name: "PR 审查", Kind: KindGate, X: 1 + s, Y: 2, TargetPos: "right", SourcePos: "left"},
		Node{ID: NodeRevise, Name: "按意见修订", Kind: KindAuto, X: 1 + s, Y: 4, Skill: "pr-revise", PhaseKeys: []string{"D.revise"}, TargetPos: "top", SourcePos: "right"}, // 回边挂 PR 审查正下方
		// —— 终点 ——
		Node{ID: NodeDone, Name: "完成", Kind: KindTerminal, X: 0, Y: 2, TargetPos: "right"},
		Node{ID: NodeSkipped, Name: "无需处理", Kind: KindTerminal, X: 2, Y: -1, TargetPos: "bottom"},
		Node{ID: NodeFailed, Name: "待裁决（异常）", Kind: KindTerminal, X: 3 + s, Y: 4},
	)
	// 主干顺序连线
	spine := []string{NodeReadJira, NodeInvestigate, NodeCreateIssue}
	if agentReview {
		spine = append(spine, NodeAgentReview)
	}
	spine = append(spine, NodeReview, NodeBlueprint, NodeImplement, NodeTest, NodePR, NodePRReview)
	edges := make([]Edge, 0)
	for i := 0; i+1 < len(spine); i++ {
		edges = append(edges, Edge{From: spine[i], To: spine[i+1]})
	}
	edges = append(edges,
		Edge{From: NodePRReview, To: NodeDone},       // PR approved → 完成
		Edge{From: NodePRReview, To: NodeRevise},     // changes requested → 修订
		Edge{From: NodeRevise, To: NodePRReview},     // 修订完 → 回到审查闸口（多轮回边）
		Edge{From: NodeCreateIssue, To: NodeSkipped}, // B 正常判定无需处理
		Edge{From: NodeCreateIssue, To: NodeFailed},  // B 异常
		Edge{From: NodePR, To: NodeFailed},           // C 异常
		Edge{From: NodeRevise, To: NodeFailed},       // D 异常
		Edge{From: NodePRReview, To: NodeFailed},     // 超轮次上限 → 待裁决
	)
	return Pipeline{ID: "default", Nodes: nodes, Edges: edges}
}

// NodeIDs 返回参与运行态跟踪的节点（终点不计，终点按任务态点亮）。
func (p Pipeline) NodeIDs() []string {
	ids := make([]string, 0, len(p.Nodes))
	for _, n := range p.Nodes {
		if n.Kind != KindTerminal {
			ids = append(ids, n.ID)
		}
	}
	return ids
}

// NodeForPhase 把 skill 上报的 phase 标识映射到节点 ID；找不到返回 ""。
func (p Pipeline) NodeForPhase(phase string) string {
	for _, n := range p.Nodes {
		for _, pk := range n.PhaseKeys {
			if pk == phase {
				return n.ID
			}
		}
	}
	return ""
}
