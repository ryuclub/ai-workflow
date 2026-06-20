# 编码规约

## 技术栈

| 维度 | 选型 | 备注 |
| --- | --- | --- |
| 语言 | Go 1.25.5 | `module mosavi.space/mosavi-channel-service` |
| Web 框架 | Gin | 中间件：cors / gzip / requestid |
| 依赖注入 | Uber-go/fx | 按层 Module 装配（架构见 [`ARCHITECTURE.md`](../../ARCHITECTURE.md)） |
| 数据库 | PostgreSQL 15.10（GORM + pgx 驱动） | 单一 `infra.ChannelClient`，**无读写分离**；连接池 MaxOpen=100 / MaxIdle=50 |
| 配置 | spf13/viper | `ENV_CONTENTS`（JSON）优先，回退 `env.toml`；敏感字段不入库 |
| 日志 | zap + OTel（经 go-common otelzap 桥接） | 规范见下「日志规范」 |
| 参数校验 | go-playground/validator/v10 | 统一经 `internal/util/request` |
| API 文档 | swaggo（swag / gin-swagger） | 非 release 暴露 Swagger UI |
| 私有库 | `Mosavi-go-common` | 需 `GOPRIVATE=github.com/MosaviJP/*` + SSH key |

## 通用原则

- 代码注释和文档使用中文
- 遵循 Go 官方编码规范（`gofmt` 格式化）
- 错误处理必须显式处理，禁止忽略错误

## 错误码管理

- 错误码定义在 `internal/model/xerror/error.go`
- **新增或修改错误码时，必须同步更新 文档**：[Mosavi-docs 频道 ](https://github.com/MosaviJP/Mosavi-docs/blob/main/%E9%A2%91%E9%81%93/%E6%8A%80%E6%9C%AF%E6%96%87%E6%A1%A3/04-%E5%93%8D%E5%BA%94%E7%A0%81%E5%AE%9A%E4%B9%89.md)

## 日志规范

- 统一 `log.WithTraceIds(apiCtx.GetTraceId(), apiCtx.GetRootTraceId())` 取 logger；前缀用 `internal/constants` 的 `LogPrefix*`（Controller/Service/Repo）
- 生产日志级别 `Info`，调试用 `Debug`
- **必要日志（Info）**：请求入口（方法、路径）、跳过签名验证的路径、签名验证成功
- **调试日志（Debug）**：中间处理步骤、参数内容、数据库操作详情
- 禁止输出敏感信息（密钥、Token、签名参数、pubkey 私有部分等）

## 数据库操作

本工程**无 ReadDB/WriteDB 读写分离**，单一 `infra.ChannelClient`：

- **数据迁移**：遵循gorm迁移
- **单表读写**：在 `internal/repo`，用 `r.db.DB.WithContext(...)`；查询未找到返回 `(nil, nil)`，不当错误
- **跨表写**：**只**在 `internal/service` 经 `s.db.DoTx(apiCtx, func(tx *gorm.DB) error {...})`；repo 的 `*WithTx` 方法接收传入的 `tx *gorm.DB`，不自起事务
- **错误归类**：service 中 `*xerror.Error` 原样回吐；其余底层错误归 `xerror.DatabaseExecutionError` 后返回
- 唯一键冲突用 `repo.IsDuplicateKeyErr(err)`（GORM v2 `ErrDuplicatedKey`）判定

## Swagger 注释规范

**凡是新增或修改 `internal/controller/` 下的 handler 函数，必须同步添加或更新 Swagger 注释。**
注释模板 [`templates/swagger.md`](./templates/swagger.md)

### 关键规则

| 项目 | 说明 |
| ---- | ---- |
| **认证参数** | `pubkey` header 是主要认证方式，**非 JWT** |
| **成功响应** | 泛型 `res.Result[T]`（列表 `res.ListResult[T]` / 分页 `res.PageResult[T]`），`code` 为业务码（0=成功） |
| **错误码** | 定义在 `internal/model/xerror/`，形如 **7xxx**，注释用占位符不硬编码 |
| **路由路径** | 从 `internal/server/http/router` 的 `Register*` / 路由组确认，不得凭猜测 |
| **路由组前缀** | `api-v1` → `/v1`，`internal-api-v1` → `/internal/v1`（@BasePath 为 `/`，本工程**无** `/api/v1`、无 group 前缀） |

### HTTP 状态码与业务码的约定

`response.Success` / `response.Error` **始终返回 HTTP 200**，结果通过 JSON 体 `code` 字段传递。故 Swagger 注释中：

- `@Success 200` → HTTP 200，正常响应
- `@Failure <业务码>` → **业务错误码**（非 HTTP 状态码），用于文档化可能的业务错误

这不符合 Swagger 2.0 规范（要求 3 位 HTTP 状态码），但是项目既定设计，**不要改为 HTTP 400/500**。

### 注释添加后验证

修改注释后必须执行，确认生成无报错（generalInfo 入口为根 `main.go`，注解 `@title/@version/@BasePath` 在此）：

```bash
swag init \
  --generalInfo main.go \
  -o ./docs \
  --parseInternal \
  --parseDependency
```
