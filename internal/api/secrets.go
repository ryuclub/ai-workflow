package api

import (
	"net/http"

	"github.com/ryuclub/ai-workflow/internal/core/secret"
	"github.com/gin-gonic/gin"
)

type tokenReq struct {
	Token string `json:"token"` // Claude 登录态令牌（claude setup-token 生成）；空串=清除
}

// vaultReady 校验凭据保险箱可用（配了 MASTER_KEY）。
func (s *Server) vaultReady(c *gin.Context) bool {
	if s.vault == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "未配置 MASTER_KEY，凭据存储不可用"})
		return false
	}
	return true
}

// PUT /api/v1/settings/claude-token — 设置本租户共享登录态令牌（租户管理员）。
func (s *Server) putTenantClaudeToken(c *gin.Context) {
	if !s.vaultReady(c) {
		return
	}
	var req tokenReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := s.vault.SetTenant(c.GetString(ctxTenantID), secret.KeyClaudeToken, req.Token); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "configured": req.Token != ""})
}

// GET /api/v1/settings/claude-token — 探测本租户是否已配共享令牌（不回明文）。
func (s *Server) getTenantClaudeToken(c *gin.Context) {
	if !s.vaultReady(c) {
		return
	}
	c.JSON(http.StatusOK, gin.H{"configured": s.vault.HasTenant(c.GetString(ctxTenantID), secret.KeyClaudeToken)})
}

// PUT /api/v1/me/claude-token — 设置本人个人登录态令牌（覆盖租户共享）。
func (s *Server) putMyClaudeToken(c *gin.Context) {
	if !s.vaultReady(c) {
		return
	}
	var req tokenReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := s.vault.SetUser(c.GetString(ctxUserID), secret.KeyClaudeToken, req.Token); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "configured": req.Token != ""})
}

// GET /api/v1/me/claude-token — 探测本人是否已配个人令牌（不回明文）。
func (s *Server) getMyClaudeToken(c *gin.Context) {
	if !s.vaultReady(c) {
		return
	}
	c.JSON(http.StatusOK, gin.H{"configured": s.vault.HasUser(c.GetString(ctxUserID), secret.KeyClaudeToken)})
}
