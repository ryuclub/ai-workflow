package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/ryuclub/ai-workflow/internal/core/store"
	"github.com/gin-gonic/gin"
)

// GET /api/v1/platform/tenants — 列出所有租户（平台超管）。
func (s *Server) listTenants(c *gin.Context) {
	tenants, err := s.ids.ListTenants()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"tenants": tenants})
}

type createTenantReq struct {
	Name          string `json:"name"`
	AdminEmail    string `json:"admin_email"`
	AdminPassword string `json:"admin_password"`
}

// POST /api/v1/platform/tenants — 开通新租户 + 其首个管理员（平台超管）。
// 管理员邮箱已有用户则直接授予该租户 admin 成员资格，否则用初始密码建用户。
func (s *Server) createTenant(c *gin.Context) {
	var req createTenantReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	name := strings.TrimSpace(req.Name)
	email := strings.TrimSpace(strings.ToLower(req.AdminEmail))
	if name == "" || email == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "公司名与管理员邮箱必填"})
		return
	}
	now := time.Now()
	tenant := &store.Tenant{ID: store.NewID(), Name: name, CreatedAt: now}
	if err := s.ids.CreateTenant(tenant); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "建租户失败：" + err.Error()})
		return
	}
	u, err := s.ids.GetUserByEmail(email)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if u == nil {
		if len(req.AdminPassword) < 6 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "新管理员需设至少 6 位初始密码"})
			return
		}
		ph, err := hashPassword(req.AdminPassword)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		u = &store.User{ID: store.NewID(), Email: email, PasswordHash: ph, CreatedAt: now}
		if err := s.ids.CreateUser(u); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "建管理员失败：" + err.Error()})
			return
		}
	}
	if err := s.ids.CreateMembership(&store.Membership{UserID: u.ID, TenantID: tenant.ID, Role: store.RoleAdmin}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "授予管理员失败：" + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "tenant": tenant, "admin_email": email})
}
