---
name: gin-api-docs
description: Use when extracting, generating, or updating API documentation from this Gin project. Triggers when asked to generate API docs, add Swagger annotations, run swag init, or document endpoints.
---

# Gin API 文档提取（Mosavi-Channel-Service）

从本项目提取 API 文档，生成 Swagger/OpenAPI 规格文件，补全缺失注释。

> **注意**：示例中的路径/包名以本工程为准（module `mosavi.space/mosavi-channel-service`，HTTP 层 `internal/server/http`），实际生成时仍需对照当前代码结构核对。

## 生成文档命令

```bash
# 在项目根目录执行
swag init \
  --generalInfo internal/server/http/create.go \
  -o ./docs \
  --parseInternal \
  --parseDependency
```

产物：`docs/swagger.json` / `docs/swagger.yaml` / `docs/docs.go`

## 扫描注释覆盖率

```bash
# 找出缺少 @Router 的 controller
grep -rL "@Router" internal/controller/*.go | grep -v "_test.go\|0module.go"

# 确认路由路径（查看 Register* 函数）
grep -A 10 "func Register" internal/controller/<file>.go
```

## Handler 注释模板

```go
// MethodName godoc
// @Summary 简短描述
// @Description 详细描述
// @Tags tagname
// @Accept json
// @Produce json
// @Param pubkey header string true "Public Key"
// @Param body body req.XxxRequest true "请求参数"
// @Success 200 {object} res.Result[res.XxxResponse] "Success"
// @Failure 4604 {object} xerror.Error "错误描述（示例错误码，请替换为实际 4xxx 错误码）"
// @Router /api/v1/path [post]
func (c *XxxController) MethodName(ctx *gin.Context) {
```

## 本项目关键规则

| 项目 | 说明 |
|------|------|
| **认证** | `pubkey` header，非 JWT |
| **成功响应** | `res.Result[T]`，code=0 表示成功 |
| **错误码** | `internal/model/xerror/error.go`，形如 4xxx |
| **路由组** | 频道服务具体路由组待确认 |

## 常见陷阱

### ⚠️ res 类型在注释中不可见

**症状**：`swag init` 报 `cannot find type definition: res.Result[res.XxxType]`

**原因**：handler 文件未 import `res` 包（handler 代码只用 service 返回值，不直接引用 res 类型）

**解决**：添加空导入：

```go
import (
    "mosavi.space/mosavi-channel-service/internal/model/req"
    _ "mosavi.space/mosavi-channel-service/internal/model/res"  // swag 类型解析用
    ...
)
```

### ⚠️ 其他常见问题

| 问题 | 解决 |
|------|------|
| 路由显示为空 | `--generalInfo` 须指向含 `// @host` 的文件（`internal/server/http/create.go`） |
| 内部包找不到 | 确认使用了 `--parseInternal` flag |
| 泛型类型不生成 | `swag --version` 确认 v1.8+ |

## 导出路由摘要

```bash
cat docs/swagger.json | python3 -c "
import json,sys
d=json.load(sys.stdin)
for p in sorted(d.get('paths',{}).keys()): print(p)
"
```
