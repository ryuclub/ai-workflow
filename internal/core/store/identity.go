package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"time"
)

// Role 是用户在某租户内的角色。
type Role string

const (
	RoleAdmin  Role = "admin"  // 租户管理员：管成员、改租户凭据/共享令牌
	RoleMember Role = "member" // 成员：开/看本司任务、管自己的个人令牌
)

// Tenant 是一个租户（公司）。
type Tenant struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

// User 是平台自管用户（邮箱+密码）。密码哈希不出 store 层。
type User struct {
	ID           string    `json:"id"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"-"`
	CreatedAt    time.Time `json:"created_at"`
}

// Membership 是用户在某租户内的成员关系与角色。
type Membership struct {
	UserID   string `json:"user_id"`
	TenantID string `json:"tenant_id"`
	Role     Role   `json:"role"`
}

// Session 是一次登录会话。TenantID 固定本会话的活跃租户（用户多租户时登录选定）。
type Session struct {
	Token     string    `json:"-"`
	UserID    string    `json:"user_id"`
	TenantID  string    `json:"tenant_id"`
	Role      Role      `json:"role"`
	ExpiresAt time.Time `json:"expires_at"`
}

// IdentityStore 是身份/会话持久化接口，由 *SQLite 实现。
type IdentityStore interface {
	CountUsers() (int, error)
	CreateTenant(t *Tenant) error
	ListTenants() ([]*Tenant, error)
	CreateUser(u *User) error
	GetUserByEmail(email string) (*User, error)
	CreateMembership(m *Membership) error
	GetMembership(userID, tenantID string) (*Membership, error)
	ListMembershipsByUser(userID string) ([]*Membership, error)
	ListTenantMembers(tenantID string) ([]*Member, error)
	DeleteMembership(userID, tenantID string) error
	CreateSession(sess *Session) error
	GetSession(token string) (*Session, error)
	DeleteSession(token string) error
	GetTenantConfigJSON(tenantID string) (string, error)
	PutTenantConfigJSON(tenantID, j string) error
}

// NewID 生成 16 字节随机十六进制标识（租户/用户 id、会话 token 共用）。
func NewID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *SQLite) CountUsers() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

func (s *SQLite) CreateTenant(t *Tenant) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`INSERT INTO tenants(id,name,created_at) VALUES(?,?,?)`, t.ID, t.Name, t.CreatedAt)
	return err
}

func (s *SQLite) ListTenants() ([]*Tenant, error) {
	rows, err := s.db.Query(`SELECT id,name,created_at FROM tenants ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Tenant
	for rows.Next() {
		var t Tenant
		if err := rows.Scan(&t.ID, &t.Name, &t.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &t)
	}
	return out, rows.Err()
}

func (s *SQLite) CreateUser(u *User) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`INSERT INTO users(id,email,password_hash,created_at) VALUES(?,?,?,?)`,
		u.ID, u.Email, u.PasswordHash, u.CreatedAt)
	return err
}

func (s *SQLite) GetUserByEmail(email string) (*User, error) {
	var u User
	err := s.db.QueryRow(`SELECT id,email,password_hash,created_at FROM users WHERE email=?`, email).
		Scan(&u.ID, &u.Email, &u.PasswordHash, &u.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &u, err
}

func (s *SQLite) CreateMembership(m *Membership) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(
		`INSERT INTO memberships(user_id,tenant_id,role) VALUES(?,?,?)
		 ON CONFLICT(user_id,tenant_id) DO UPDATE SET role=excluded.role`,
		m.UserID, m.TenantID, m.Role)
	return err
}

func (s *SQLite) GetMembership(userID, tenantID string) (*Membership, error) {
	var m Membership
	err := s.db.QueryRow(`SELECT user_id,tenant_id,role FROM memberships WHERE user_id=? AND tenant_id=?`, userID, tenantID).
		Scan(&m.UserID, &m.TenantID, &m.Role)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &m, err
}

// Member 是某租户内的成员视图（用户 + 角色），供成员管理列表。
type Member struct {
	UserID string `json:"user_id"`
	Email  string `json:"email"`
	Role   Role   `json:"role"`
}

// ListTenantMembers 返回某租户全部成员（join users 取邮箱），按邮箱排序。
func (s *SQLite) ListTenantMembers(tenantID string) ([]*Member, error) {
	rows, err := s.db.Query(
		`SELECT u.id, u.email, m.role FROM memberships m
		 JOIN users u ON u.id = m.user_id
		 WHERE m.tenant_id=? ORDER BY u.email`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Member
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.UserID, &m.Email, &m.Role); err != nil {
			return nil, err
		}
		out = append(out, &m)
	}
	return out, rows.Err()
}

// DeleteMembership 移除某用户在某租户的成员资格（不删用户本身）。
func (s *SQLite) DeleteMembership(userID, tenantID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`DELETE FROM memberships WHERE user_id=? AND tenant_id=?`, userID, tenantID)
	return err
}

func (s *SQLite) ListMembershipsByUser(userID string) ([]*Membership, error) {
	rows, err := s.db.Query(`SELECT user_id,tenant_id,role FROM memberships WHERE user_id=? ORDER BY tenant_id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Membership
	for rows.Next() {
		var m Membership
		if err := rows.Scan(&m.UserID, &m.TenantID, &m.Role); err != nil {
			return nil, err
		}
		out = append(out, &m)
	}
	return out, rows.Err()
}

func (s *SQLite) CreateSession(sess *Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`INSERT INTO sessions(token,user_id,tenant_id,role,expires_at) VALUES(?,?,?,?,?)`,
		sess.Token, sess.UserID, sess.TenantID, sess.Role, sess.ExpiresAt)
	return err
}

// GetSession 返回未过期的会话；过期或不存在返回 nil。
func (s *SQLite) GetSession(token string) (*Session, error) {
	var sess Session
	err := s.db.QueryRow(`SELECT token,user_id,tenant_id,role,expires_at FROM sessions WHERE token=?`, token).
		Scan(&sess.Token, &sess.UserID, &sess.TenantID, &sess.Role, &sess.ExpiresAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if time.Now().After(sess.ExpiresAt) {
		_ = s.DeleteSession(token)
		return nil, nil
	}
	return &sess, nil
}

func (s *SQLite) DeleteSession(token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`DELETE FROM sessions WHERE token=?`, token)
	return err
}

// ── 加密凭据（密文块，明文加解密在 secret.Vault） ───────────────────────

func (s *SQLite) PutTenantSecret(tenantID, key string, enc []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(
		`INSERT INTO tenant_secrets(tenant_id,key,value_enc) VALUES(?,?,?)
		 ON CONFLICT(tenant_id,key) DO UPDATE SET value_enc=excluded.value_enc`,
		tenantID, key, enc)
	return err
}

func (s *SQLite) GetTenantSecret(tenantID, key string) ([]byte, error) {
	var enc []byte
	err := s.db.QueryRow(`SELECT value_enc FROM tenant_secrets WHERE tenant_id=? AND key=?`, tenantID, key).Scan(&enc)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return enc, err
}

func (s *SQLite) PutUserSecret(userID, key string, enc []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(
		`INSERT INTO user_secrets(user_id,key,value_enc) VALUES(?,?,?)
		 ON CONFLICT(user_id,key) DO UPDATE SET value_enc=excluded.value_enc`,
		userID, key, enc)
	return err
}

func (s *SQLite) GetUserSecret(userID, key string) ([]byte, error) {
	var enc []byte
	err := s.db.QueryRow(`SELECT value_enc FROM user_secrets WHERE user_id=? AND key=?`, userID, key).Scan(&enc)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return enc, err
}

// ── per-tenant 配置（非机密：source/default/status_map/repos 的 JSON） ──────

// GetTenantConfigJSON 返回某租户的配置 JSON；未设置返回 ("", nil)。
func (s *SQLite) GetTenantConfigJSON(tenantID string) (string, error) {
	var j string
	err := s.db.QueryRow(`SELECT json FROM tenant_configs WHERE tenant_id=?`, tenantID).Scan(&j)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return j, err
}

func (s *SQLite) PutTenantConfigJSON(tenantID, j string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(
		`INSERT INTO tenant_configs(tenant_id,json) VALUES(?,?)
		 ON CONFLICT(tenant_id) DO UPDATE SET json=excluded.json`,
		tenantID, j)
	return err
}
