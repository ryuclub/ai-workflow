package store

import (
	"database/sql"
	"time"
)

// AgentMessage 是调度 Agent 对话流中的一条消息（对话历史，id 作 SSE 游标）。
type AgentMessage struct {
	ID        int64     `json:"id"`
	TenantID  string    `json:"tenant_id"`
	Role      string    `json:"role"` // user / assistant / system
	Kind      string    `json:"kind"` // chat / wake / action_request / action_result
	Content   string    `json:"content"`
	UserID    string    `json:"user_id,omitempty"`    // role=user 时的发言人
	UserEmail string    `json:"user_email,omitempty"` // 发言人邮箱（读取时 JOIN users 填充，不落库）
	ActionID  int64     `json:"action_id,omitempty"`  // kind=action_request 时关联的待确认动作
	TaskID    string    `json:"task_id,omitempty"`    // 关联任务（可确定归属的消息：唤醒/动作类），按任务过滤视图用
	CreatedAt time.Time `json:"created_at"`
}

// Agent 动作状态。
const (
	ActionPending  = "pending"  // 等人确认
	ActionExecuted = "executed" // 已执行（直通或确认后）
	ActionDenied   = "denied"   // 人工拒绝
	ActionFailed   = "failed"   // 执行出错
)

// AgentAction 是 Agent 发起的一次写操作记录（白名单外先落 pending 等确认）。
type AgentAction struct {
	ID        int64      `json:"id"`
	TenantID  string     `json:"tenant_id"`
	Tool      string     `json:"tool"`
	ArgsJSON  string     `json:"args_json"`
	Summary   string     `json:"summary"` // 给人看的一句话
	Status    string     `json:"status"`
	Result    string     `json:"result,omitempty"`
	DecidedBy string     `json:"decided_by,omitempty"`
	TaskID    string     `json:"task_id,omitempty"` // 关联任务（能解析出时）
	CreatedAt time.Time  `json:"created_at"`
	DecidedAt *time.Time `json:"decided_at,omitempty"`
}

// AgentStore 是调度 Agent 的持久化接口，由 *SQLite 实现。
type AgentStore interface {
	// 会话映射：租户 ↔ claude session id（进程重启后 --resume 找回上下文）。
	// GetAgentSession 同时返回该映射的落库时间（交接班判据：会话年龄）。
	GetAgentSession(tenantID string) (string, time.Time, error)
	PutAgentSession(tenantID, claudeSessionID string) error

	AppendAgentMessage(m *AgentMessage) error
	// CountAgentMessagesSince 统计某时刻后的消息数（交接班判据：会话消息量）。
	CountAgentMessagesSince(tenantID string, t time.Time) (int, error)
	// ListAgentMessagesBefore 取 id < before 的最近 limit 条（聊天窗向上翻页）。
	ListAgentMessagesBefore(tenantID string, beforeID int64, limit int) ([]*AgentMessage, error)
	// AgentHandoverEpoch 返回当前班次起点：最近一条 kind=handover 消息的时间；
	// 从未交接过则为首条消息时间；无消息返回零值。
	AgentHandoverEpoch(tenantID string) (time.Time, error)
	ListAgentMessages(tenantID string, sinceID int64, limit int) ([]*AgentMessage, error)

	CreateAgentAction(a *AgentAction) error
	GetAgentAction(id int64) (*AgentAction, error)
	UpdateAgentAction(a *AgentAction) error
	ListAgentActions(tenantID, status string) ([]*AgentAction, error)

	// ListTenantEvents 按租户拉全局事件（watcher / 全局视图用）。limit<=0 不限。
	ListTenantEvents(tenantID string, sinceID int64, limit int) ([]*Event, error)
	// ListEventsSince 跨租户增量拉事件（watcher 全局游标用）。
	ListEventsSince(sinceID int64, limit int) ([]*Event, error)
	// MaxEventID 返回当前最大事件 id（watcher 起始游标：不回放历史）。
	MaxEventID() (int64, error)
}

func (s *SQLite) GetAgentSession(tenantID string) (string, time.Time, error) {
	var id string
	var at time.Time
	err := s.db.QueryRow(`SELECT claude_session_id, updated_at FROM agent_sessions WHERE tenant_id=?`, tenantID).Scan(&id, &at)
	if err == sql.ErrNoRows {
		return "", time.Time{}, nil
	}
	return id, at, err
}

func (s *SQLite) AgentHandoverEpoch(tenantID string) (time.Time, error) {
	var at time.Time
	err := s.db.QueryRow(`SELECT created_at FROM agent_messages WHERE tenant_id=? AND kind='handover' ORDER BY id DESC LIMIT 1`, tenantID).Scan(&at)
	if err == sql.ErrNoRows {
		err = s.db.QueryRow(`SELECT created_at FROM agent_messages WHERE tenant_id=? ORDER BY id LIMIT 1`, tenantID).Scan(&at)
		if err == sql.ErrNoRows {
			return time.Time{}, nil
		}
	}
	return at, err
}

func (s *SQLite) CountAgentMessagesSince(tenantID string, t time.Time) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM agent_messages WHERE tenant_id=? AND created_at>?`, tenantID, t).Scan(&n)
	return n, err
}

func (s *SQLite) PutAgentSession(tenantID, claudeSessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(
		`INSERT INTO agent_sessions(tenant_id,claude_session_id,updated_at) VALUES(?,?,?)
		 ON CONFLICT(tenant_id) DO UPDATE SET claude_session_id=excluded.claude_session_id,updated_at=excluded.updated_at`,
		tenantID, claudeSessionID, time.Now())
	return err
}

func (s *SQLite) AppendAgentMessage(m *AgentMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m.CreatedAt.IsZero() {
		m.CreatedAt = time.Now()
	}
	res, err := s.db.Exec(
		`INSERT INTO agent_messages(tenant_id,role,kind,content,user_id,action_id,task_id,created_at) VALUES(?,?,?,?,?,?,?,?)`,
		m.TenantID, m.Role, m.Kind, m.Content, m.UserID, m.ActionID, m.TaskID, m.CreatedAt)
	if err != nil {
		return err
	}
	m.ID, _ = res.LastInsertId()
	return nil
}

// agentMsgCols 读消息时 LEFT JOIN users 填发言人邮箱（气泡显示是谁说的）。
const agentMsgCols = `m.id,m.tenant_id,m.role,m.kind,m.content,m.user_id,m.action_id,m.task_id,m.created_at,IFNULL(u.email,'')`

func scanAgentMsgs(rows *sql.Rows) ([]*AgentMessage, error) {
	defer rows.Close()
	var out []*AgentMessage
	for rows.Next() {
		var m AgentMessage
		if err := rows.Scan(&m.ID, &m.TenantID, &m.Role, &m.Kind, &m.Content, &m.UserID, &m.ActionID, &m.TaskID, &m.CreatedAt, &m.UserEmail); err != nil {
			return nil, err
		}
		out = append(out, &m)
	}
	return out, rows.Err()
}

func (s *SQLite) ListAgentMessages(tenantID string, sinceID int64, limit int) ([]*AgentMessage, error) {
	q := `SELECT ` + agentMsgCols + ` FROM agent_messages m LEFT JOIN users u ON u.id=m.user_id WHERE m.tenant_id=? AND m.id>? ORDER BY m.id`
	args := []any{tenantID, sinceID}
	if limit > 0 {
		// 取「最近 limit 条」而非「最早 limit 条」：首屏加载要的是尾部历史。
		q = `SELECT ` + agentMsgCols + ` FROM (
		       SELECT * FROM agent_messages WHERE tenant_id=? AND id>? ORDER BY id DESC LIMIT ?
		     ) m LEFT JOIN users u ON u.id=m.user_id ORDER BY m.id`
		args = append(args, limit)
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	return scanAgentMsgs(rows)
}

func (s *SQLite) ListAgentMessagesBefore(tenantID string, beforeID int64, limit int) ([]*AgentMessage, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(`SELECT `+agentMsgCols+` FROM (
	       SELECT * FROM agent_messages WHERE tenant_id=? AND id<? ORDER BY id DESC LIMIT ?
	     ) m LEFT JOIN users u ON u.id=m.user_id ORDER BY m.id`, tenantID, beforeID, limit)
	if err != nil {
		return nil, err
	}
	return scanAgentMsgs(rows)
}

func (s *SQLite) CreateAgentAction(a *AgentAction) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now()
	}
	res, err := s.db.Exec(
		`INSERT INTO agent_actions(tenant_id,tool,args_json,summary,status,result,decided_by,task_id,created_at,decided_at)
		 VALUES(?,?,?,?,?,?,?,?,?,?)`,
		a.TenantID, a.Tool, a.ArgsJSON, a.Summary, a.Status, a.Result, a.DecidedBy, a.TaskID, a.CreatedAt, a.DecidedAt)
	if err != nil {
		return err
	}
	a.ID, _ = res.LastInsertId()
	return nil
}

func (s *SQLite) GetAgentAction(id int64) (*AgentAction, error) {
	row := s.db.QueryRow(
		`SELECT id,tenant_id,tool,args_json,summary,status,result,decided_by,task_id,created_at,decided_at FROM agent_actions WHERE id=?`, id)
	var a AgentAction
	err := row.Scan(&a.ID, &a.TenantID, &a.Tool, &a.ArgsJSON, &a.Summary, &a.Status, &a.Result, &a.DecidedBy, &a.TaskID, &a.CreatedAt, &a.DecidedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func (s *SQLite) UpdateAgentAction(a *AgentAction) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(
		`UPDATE agent_actions SET status=?,result=?,decided_by=?,decided_at=? WHERE id=?`,
		a.Status, a.Result, a.DecidedBy, a.DecidedAt, a.ID)
	return err
}

func (s *SQLite) ListAgentActions(tenantID, status string) ([]*AgentAction, error) {
	q := `SELECT id,tenant_id,tool,args_json,summary,status,result,decided_by,task_id,created_at,decided_at FROM agent_actions WHERE tenant_id=?`
	args := []any{tenantID}
	if status != "" {
		q += ` AND status=?`
		args = append(args, status)
	}
	q += ` ORDER BY id DESC LIMIT 100`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*AgentAction
	for rows.Next() {
		var a AgentAction
		if err := rows.Scan(&a.ID, &a.TenantID, &a.Tool, &a.ArgsJSON, &a.Summary, &a.Status, &a.Result, &a.DecidedBy, &a.TaskID, &a.CreatedAt, &a.DecidedAt); err != nil {
			return nil, err
		}
		out = append(out, &a)
	}
	return out, rows.Err()
}

func (s *SQLite) MaxEventID() (int64, error) {
	var id int64
	err := s.db.QueryRow(`SELECT IFNULL(MAX(id),0) FROM events`).Scan(&id)
	return id, err
}

func (s *SQLite) ListEventsSince(sinceID int64, limit int) ([]*Event, error) {
	q := `SELECT id,task_id,tenant_id,node_id,type,level,message,ts FROM events WHERE id>? ORDER BY id`
	args := []any{sinceID}
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Event
	for rows.Next() {
		var ev Event
		if err := rows.Scan(&ev.ID, &ev.TaskID, &ev.TenantID, &ev.NodeID, &ev.Type, &ev.Level, &ev.Message, &ev.TS); err != nil {
			return nil, err
		}
		out = append(out, &ev)
	}
	return out, rows.Err()
}

func (s *SQLite) ListTenantEvents(tenantID string, sinceID int64, limit int) ([]*Event, error) {
	q := `SELECT id,task_id,tenant_id,node_id,type,level,message,ts FROM events WHERE tenant_id=? AND id>? ORDER BY id`
	args := []any{tenantID, sinceID}
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Event
	for rows.Next() {
		var ev Event
		if err := rows.Scan(&ev.ID, &ev.TaskID, &ev.TenantID, &ev.NodeID, &ev.Type, &ev.Level, &ev.Message, &ev.TS); err != nil {
			return nil, err
		}
		out = append(out, &ev)
	}
	return out, rows.Err()
}
