package api

import (
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"
)

// mcpReq 是一条 JSON-RPC 2.0 请求（MCP streamable HTTP 传输）。
type mcpReq struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

// POST /internal/mcp — Agent 会话的 MCP 工具端点（薄壳：JSON-RPC 编解码 → core/agent.Tools）。
// 租户由 X-WF-Tenant 头决定；该头由控制面固化进 mcp-config，Agent 子进程无法跨租户。
func (s *Server) handleMCP(c *gin.Context) {
	tenantID := c.GetHeader("X-WF-Tenant")
	if tenantID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing X-WF-Tenant"})
		return
	}
	var req mcpReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// 通知（无 id）：只需 202 确认，无响应体。
	if len(req.ID) == 0 || string(req.ID) == "null" {
		c.Status(http.StatusAccepted)
		return
	}

	reply := func(result any) {
		c.JSON(http.StatusOK, gin.H{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}
	replyErr := func(code int, msg string) {
		c.JSON(http.StatusOK, gin.H{"jsonrpc": "2.0", "id": req.ID,
			"error": gin.H{"code": code, "message": msg}})
	}

	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		if p.ProtocolVersion == "" {
			p.ProtocolVersion = "2025-03-26"
		}
		reply(gin.H{
			"protocolVersion": p.ProtocolVersion,
			"capabilities":    gin.H{"tools": gin.H{}},
			"serverInfo":      gin.H{"name": "wf-control-plane", "version": "1.0.0"},
		})
	case "ping":
		reply(gin.H{})
	case "tools/list":
		defs := s.mgr.Tools().List()
		tools := make([]gin.H, 0, len(defs))
		for _, d := range defs {
			tools = append(tools, gin.H{"name": d.Name, "description": d.Description, "inputSchema": d.Schema})
		}
		reply(gin.H{"tools": tools})
	case "tools/call":
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil || p.Name == "" {
			replyErr(-32602, "bad tools/call params")
			return
		}
		out, err := s.mgr.Tools().Call(c.Request.Context(), tenantID, p.Name, p.Arguments)
		if err != nil {
			// 工具级错误走 isError 内容（模型可读可恢复），不用 JSON-RPC error。
			reply(gin.H{"content": []gin.H{{"type": "text", "text": err.Error()}}, "isError": true})
			return
		}
		reply(gin.H{"content": []gin.H{{"type": "text", "text": out}}, "isError": false})
	default:
		replyErr(-32601, "method not found: "+req.Method)
	}
}
