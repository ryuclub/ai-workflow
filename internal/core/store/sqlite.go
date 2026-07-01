package store

import (
	"database/sql"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// SQLite 是基于 modernc.org/sqlite（纯 Go，无 cgo）的 Store 实现。
type SQLite struct {
	db *sql.DB
	mu sync.Mutex // SQLite 单写者，序列化写入避免 "database is locked"
}

// OpenSQLite 打开（或创建）数据库并建表。
func OpenSQLite(path string) (*SQLite, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &SQLite{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *SQLite) migrate() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS tasks (
  id TEXT PRIMARY KEY,
  seq INTEGER DEFAULT 0,
  source TEXT NOT NULL,
  source_id TEXT NOT NULL,
  title TEXT DEFAULT '',
  repo TEXT NOT NULL,
  pipeline_id TEXT NOT NULL,
  state TEXT NOT NULL,
  issue_url TEXT DEFAULT '',
  issue_num INTEGER DEFAULT 0,
  pr_url TEXT DEFAULT '',
  pr_num INTEGER DEFAULT 0,
  review_round INTEGER DEFAULT 0,
  review_cursor TEXT DEFAULT '',
  idem_key TEXT DEFAULT '',
  error TEXT DEFAULT '',
  created_at TIMESTAMP NOT NULL,
  updated_at TIMESTAMP NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_tasks_idem ON tasks(idem_key);
CREATE INDEX IF NOT EXISTS idx_tasks_source ON tasks(source, source_id);
CREATE TABLE IF NOT EXISTS node_runs (
  task_id TEXT NOT NULL,
  node_id TEXT NOT NULL,
  state TEXT NOT NULL,
  started_at TIMESTAMP,
  ended_at TIMESTAMP,
  PRIMARY KEY (task_id, node_id)
);
CREATE TABLE IF NOT EXISTS events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  task_id TEXT NOT NULL,
  node_id TEXT DEFAULT '',
  type TEXT NOT NULL,
  level TEXT NOT NULL,
  message TEXT NOT NULL,
  ts TIMESTAMP NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_events_task ON events(task_id, id);
`)
	if err != nil {
		return err
	}
	// 兼容旧库：列若已存在，ALTER 报错忽略。
	_, _ = s.db.Exec(`ALTER TABLE tasks ADD COLUMN title TEXT DEFAULT ''`)
	_, _ = s.db.Exec(`ALTER TABLE tasks ADD COLUMN seq INTEGER DEFAULT 0`)
	_, _ = s.db.Exec(`ALTER TABLE tasks ADD COLUMN pr_num INTEGER DEFAULT 0`)
	_, _ = s.db.Exec(`ALTER TABLE tasks ADD COLUMN review_round INTEGER DEFAULT 0`)
	_, _ = s.db.Exec(`ALTER TABLE tasks ADD COLUMN review_cursor TEXT DEFAULT ''`)
	// 回填历史任务的编号（仅 seq=0 的旧行，按 rowid 赋递增号）。
	_, _ = s.db.Exec(`UPDATE tasks SET seq=rowid WHERE seq=0`)
	return nil
}

func (s *SQLite) CreateTask(t *Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var seq int64
	_ = s.db.QueryRow(`SELECT IFNULL(MAX(seq),0)+1 FROM tasks`).Scan(&seq)
	t.Seq = int(seq)
	_, err := s.db.Exec(
		`INSERT INTO tasks(id,seq,source,source_id,title,repo,pipeline_id,state,issue_url,issue_num,pr_url,pr_num,review_round,review_cursor,idem_key,error,created_at,updated_at)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		t.ID, t.Seq, t.Source, t.SourceID, t.Title, t.Repo, t.PipelineID, t.State, t.IssueURL, t.IssueNum, t.PRURL, t.PRNum, t.ReviewRound, t.ReviewCursor, t.IdemKey, t.Error, t.CreatedAt, t.UpdatedAt)
	return err
}

func scanTask(row interface{ Scan(...any) error }) (*Task, error) {
	var t Task
	err := row.Scan(&t.ID, &t.Seq, &t.Source, &t.SourceID, &t.Title, &t.Repo, &t.PipelineID, &t.State,
		&t.IssueURL, &t.IssueNum, &t.PRURL, &t.PRNum, &t.ReviewRound, &t.ReviewCursor, &t.IdemKey, &t.Error, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

const taskCols = `id,seq,source,source_id,title,repo,pipeline_id,state,issue_url,issue_num,pr_url,pr_num,review_round,review_cursor,idem_key,error,created_at,updated_at`

func (s *SQLite) GetTask(id string) (*Task, error) {
	row := s.db.QueryRow(`SELECT `+taskCols+` FROM tasks WHERE id=?`, id)
	t, err := scanTask(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return t, err
}

func (s *SQLite) FindByIdem(idem string) (*Task, error) {
	if idem == "" {
		return nil, nil
	}
	row := s.db.QueryRow(`SELECT `+taskCols+` FROM tasks WHERE idem_key=? ORDER BY created_at DESC LIMIT 1`, idem)
	t, err := scanTask(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return t, err
}

func (s *SQLite) FindBySource(source, sourceID string) (*Task, error) {
	row := s.db.QueryRow(`SELECT `+taskCols+` FROM tasks WHERE source=? AND source_id=? ORDER BY created_at DESC LIMIT 1`, source, sourceID)
	t, err := scanTask(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return t, err
}

func (s *SQLite) ListTasks() ([]*Task, error) {
	rows, err := s.db.Query(`SELECT ` + taskCols + ` FROM tasks ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *SQLite) UpdateTask(t *Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t.UpdatedAt = time.Now()
	_, err := s.db.Exec(
		`UPDATE tasks SET state=?,issue_url=?,issue_num=?,pr_url=?,pr_num=?,review_round=?,review_cursor=?,error=?,updated_at=? WHERE id=?`,
		t.State, t.IssueURL, t.IssueNum, t.PRURL, t.PRNum, t.ReviewRound, t.ReviewCursor, t.Error, t.UpdatedAt, t.ID)
	return err
}

func (s *SQLite) UpsertNodeRun(nr *NodeRun) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(
		`INSERT INTO node_runs(task_id,node_id,state,started_at,ended_at) VALUES(?,?,?,?,?)
		 ON CONFLICT(task_id,node_id) DO UPDATE SET state=excluded.state,started_at=excluded.started_at,ended_at=excluded.ended_at`,
		nr.TaskID, nr.NodeID, nr.State, nr.StartedAt, nr.EndedAt)
	return err
}

func (s *SQLite) ListNodeRuns(taskID string) ([]*NodeRun, error) {
	rows, err := s.db.Query(`SELECT task_id,node_id,state,started_at,ended_at FROM node_runs WHERE task_id=?`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*NodeRun
	for rows.Next() {
		var nr NodeRun
		if err := rows.Scan(&nr.TaskID, &nr.NodeID, &nr.State, &nr.StartedAt, &nr.EndedAt); err != nil {
			return nil, err
		}
		out = append(out, &nr)
	}
	return out, rows.Err()
}

func (s *SQLite) AppendEvent(ev *Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.Exec(
		`INSERT INTO events(task_id,node_id,type,level,message,ts) VALUES(?,?,?,?,?,?)`,
		ev.TaskID, ev.NodeID, ev.Type, ev.Level, ev.Message, ev.TS)
	if err != nil {
		return err
	}
	ev.ID, _ = res.LastInsertId()
	return nil
}

func (s *SQLite) ListEvents(taskID string, sinceID int64) ([]*Event, error) {
	rows, err := s.db.Query(
		`SELECT id,task_id,node_id,type,level,message,ts FROM events WHERE task_id=? AND id>? ORDER BY id`,
		taskID, sinceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Event
	for rows.Next() {
		var ev Event
		if err := rows.Scan(&ev.ID, &ev.TaskID, &ev.NodeID, &ev.Type, &ev.Level, &ev.Message, &ev.TS); err != nil {
			return nil, err
		}
		out = append(out, &ev)
	}
	return out, rows.Err()
}

func (s *SQLite) ReconcileInterrupted() (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.Exec(
		`UPDATE tasks SET state=?, error='服务重启中断', updated_at=?
		 WHERE state IN ('queued','running_b','running_c','running_d')`,
		StateAdjudication, time.Now())
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

func (s *SQLite) Close() error { return s.db.Close() }
