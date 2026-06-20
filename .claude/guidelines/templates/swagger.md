# 注释模板

```go
// MethodName godoc
// @Summary 简短描述
// @Description 详细描述
// @Tags tagname
// @Accept json
// @Produce json
// @Param pubkey header string true "Public Key"
// @Param body body req.XxxParams true "请求参数"
// @Success 200 {object} res.Result[res.XxxResponse] "成功"
// @Failure <业务码> {object} xerror.Error "错误描述（占位符，码值见 04-响应码定义.md / xerror，如 7404）"
// @Router /v1/xxx [post]
func (c *XxxController) MethodName(ctx *gin.Context) {
```