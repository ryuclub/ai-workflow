package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/ryuclub/ai-workflow/internal/core/store"
	"github.com/gin-gonic/gin"
)

// GET /api/v1/members — 列出当前租户成员（管理员）。
func (s *Server) listMembers(c *gin.Context) {
	members, err := s.ids.ListTenantMembers(c.GetString(ctxTenantID))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"members": members})
}

type addMemberReq struct {
	Email    string `json:"email"`
	Password string `json:"password"` // 新用户初始密码；用户已存在则忽略
	Role     string `json:"role"`     // admin | member（默认 member）
}

// POST /api/v1/members — 加成员到当前租户（管理员）。
// 邮箱已有用户则直接加成员资格；否则新建用户（需 password）再加。
func (s *Server) addMember(c *gin.Context) {
	var req addMemberReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	email := strings.TrimSpace(strings.ToLower(req.Email))
	if email == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "邮箱必填"})
		return
	}
	role := store.Role(req.Role)
	if role != store.RoleAdmin {
		role = store.RoleMember
	}
	tenantID := c.GetString(ctxTenantID)

	u, err := s.ids.GetUserByEmail(email)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if u == nil {
		if len(req.Password) < 6 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "新用户需设至少 6 位初始密码"})
			return
		}
		ph, err := hashPassword(req.Password)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		u = &store.User{ID: store.NewID(), Email: email, PasswordHash: ph, CreatedAt: time.Now()}
		if err := s.ids.CreateUser(u); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "建用户失败：" + err.Error()})
			return
		}
	}
	if err := s.ids.CreateMembership(&store.Membership{UserID: u.ID, TenantID: tenantID, Role: role}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "加成员失败：" + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "user_id": u.ID, "email": email, "role": role})
}

type roleReq struct {
	Role string `json:"role"`
}

// PUT /api/v1/members/:uid/role — 改成员角色（管理员）。
func (s *Server) setMemberRole(c *gin.Context) {
	var req roleReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	role := store.Role(req.Role)
	if role != store.RoleAdmin && role != store.RoleMember {
		c.JSON(http.StatusBadRequest, gin.H{"error": "角色仅支持 admin / member"})
		return
	}
	tenantID := c.GetString(ctxTenantID)
	uid := c.Param("uid")
	// 防自锁：管理员不可把自己降级（避免租户失去最后一个管理员）。
	if uid == c.GetString(ctxUserID) && role != store.RoleAdmin {
		c.JSON(http.StatusBadRequest, gin.H{"error": "不能降级自己的管理员角色"})
		return
	}
	m, err := s.ids.GetMembership(uid, tenantID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if m == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "该用户不是本租户成员"})
		return
	}
	if err := s.ids.CreateMembership(&store.Membership{UserID: uid, TenantID: tenantID, Role: role}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// DELETE /api/v1/members/:uid — 移出当前租户（管理员；不可移除自己）。
func (s *Server) removeMember(c *gin.Context) {
	uid := c.Param("uid")
	if uid == c.GetString(ctxUserID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "不能移除自己"})
		return
	}
	if err := s.ids.DeleteMembership(uid, c.GetString(ctxTenantID)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
