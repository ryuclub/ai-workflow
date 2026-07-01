package api

import (
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/ryuclub/ai-workflow/internal/core/config"
	"github.com/ryuclub/ai-workflow/internal/core/secret"
	"github.com/ryuclub/ai-workflow/internal/core/store"
	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

// sessionTTL 是登录会话有效期。
const sessionTTL = 30 * 24 * time.Hour

// ctx 键：requireAuth 解析会话后注入，handler 据此归属/过滤。
const (
	ctxUserID   = "user_id"
	ctxTenantID = "tenant_id"
	ctxRole     = "role"
)

// hashPassword / checkPassword 用 bcrypt；哈希不出 store 层。
func hashPassword(pw string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	return string(b), err
}
func checkPassword(hash, pw string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}

// dummyHash 用于用户不存在时仍走一次 bcrypt，抹平「存在/不存在」的时延差，防邮箱枚举。
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("x"), bcrypt.DefaultCost)

// requireAuth 校验会话 token（Bearer 或 ?token=，后者供 SSE），解析 user+tenant+role 注入 ctx。
func (s *Server) requireAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		tok := strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
		if tok == "" {
			tok = c.Query("token")
		}
		if tok == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		sess, err := s.ids.GetSession(tok)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if sess == nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "会话失效，请重新登录"})
			return
		}
		c.Set(ctxUserID, sess.UserID)
		c.Set(ctxTenantID, sess.TenantID)
		c.Set(ctxRole, string(sess.Role))
		c.Next()
	}
}

// requireAdminRole 在 requireAuth 之后，进一步要求租户管理员角色。
func (s *Server) requireAdminRole() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetString(ctxRole) != string(store.RoleAdmin) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "需要租户管理员权限"})
			return
		}
		c.Next()
	}
}

type loginReq struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	TenantID string `json:"tenant_id"` // 多租户成员时指定活跃租户；单租户可空
}

// POST /api/v1/auth/login — 邮箱+密码登录，发会话 token。
func (s *Server) login(c *gin.Context) {
	var req loginReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	u, err := s.ids.GetUserByEmail(req.Email)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if u == nil {
		bcrypt.CompareHashAndPassword(dummyHash, []byte(req.Password)) // 抹平时延，防枚举
		c.JSON(http.StatusUnauthorized, gin.H{"error": "邮箱或密码错误"})
		return
	}
	if !checkPassword(u.PasswordHash, req.Password) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "邮箱或密码错误"})
		return
	}
	mems, err := s.ids.ListMembershipsByUser(u.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if len(mems) == 0 {
		c.JSON(http.StatusForbidden, gin.H{"error": "该用户未加入任何租户"})
		return
	}
	// 选活跃租户：指定则校验归属；未指定取第一个（前端可据 tenants 列表再切）。
	active := mems[0]
	if req.TenantID != "" {
		matched := false
		for _, m := range mems {
			if m.TenantID == req.TenantID {
				active = m
				matched = true
				break
			}
		}
		if !matched {
			c.JSON(http.StatusForbidden, gin.H{"error": "无该租户成员资格"})
			return
		}
	}
	sess := &store.Session{
		Token:     store.NewID() + store.NewID(), // 32 字节
		UserID:    u.ID,
		TenantID:  active.TenantID,
		Role:      active.Role,
		ExpiresAt: time.Now().Add(sessionTTL),
	}
	if err := s.ids.CreateSession(sess); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"token":     sess.Token,
		"user":      u,
		"tenant_id": active.TenantID,
		"role":      active.Role,
		"tenants":   mems, // 供多租户切换
	})
}

// POST /api/v1/auth/logout — 注销当前会话。
func (s *Server) logout(c *gin.Context) {
	tok := strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
	if tok == "" {
		tok = c.Query("token")
	}
	if tok != "" {
		_ = s.ids.DeleteSession(tok)
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// GET /api/v1/auth/me — 当前用户/租户/角色。
func (s *Server) me(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"user_id":   c.GetString(ctxUserID),
		"tenant_id": c.GetString(ctxTenantID),
		"role":      c.GetString(ctxRole),
	})
}

// SeedBootstrap 在无任何用户时，据环境变量建首个租户 + 管理员（平台手动开通入口），
// 并把现有全局配置（config.json/.env）迁进该租户，使既有单租户部署平滑过渡。
// WF_BOOTSTRAP_EMAIL / WF_BOOTSTRAP_PASSWORD / WF_BOOTSTRAP_TENANT。
func SeedBootstrap(ids store.IdentityStore, base *config.Config, vault *secret.Vault, email, password, tenantName string) error {
	n, err := ids.CountUsers()
	if err != nil {
		return err
	}
	if n > 0 {
		return nil // 已初始化
	}
	if email == "" || password == "" {
		log.Printf("⚠ 尚无用户，且未提供 WF_BOOTSTRAP_EMAIL/WF_BOOTSTRAP_PASSWORD，无法登录；请设置后重启以创建首个管理员")
		return nil
	}
	if tenantName == "" {
		tenantName = "默认租户"
	}
	now := time.Now()
	tenant := &store.Tenant{ID: store.NewID(), Name: tenantName, CreatedAt: now}
	if err := ids.CreateTenant(tenant); err != nil {
		return err
	}
	ph, err := hashPassword(password)
	if err != nil {
		return err
	}
	user := &store.User{ID: store.NewID(), Email: strings.ToLower(email), PasswordHash: ph, CreatedAt: now}
	if err := ids.CreateUser(user); err != nil {
		return err
	}
	if err := ids.CreateMembership(&store.Membership{UserID: user.ID, TenantID: tenant.ID, Role: store.RoleAdmin}); err != nil {
		return err
	}
	// 迁移：把现有全局 config.json 存为该租户配置；有 vault 时把 .env 凭据迁进该租户密钥。
	if base != nil {
		if j, err := base.ToTenantJSON(); err == nil {
			_ = ids.PutTenantConfigJSON(tenant.ID, j)
		}
		if vault != nil {
			for _, k := range config.CredentialKeys {
				if v := base.Env[k]; v != "" {
					_ = vault.SetTenant(tenant.ID, k, v)
				}
			}
		}
	}
	log.Printf("已创建首个租户 %q 与管理员 %s（已迁移现有配置）", tenantName, email)
	return nil
}
