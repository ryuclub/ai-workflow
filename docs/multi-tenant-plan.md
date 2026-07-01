# 多租户设计草案（多公司 × 每公司多员工）

> 状态：**待确认**。本文把控制面从「单公司 / 单登录态」演进为「多公司、每公司多员工」，
> 与 [`ai-workflow-rebuild-plan.md`](./ai-workflow-rebuild-plan.md) 的流水线设计并行；本文只谈租户/身份/凭据/隔离，不改流水线语义。

## 1. 目标与硬约束

- **多公司**：少量、**手动开通**（非自助注册）。不必上「共享单实例 + 处处 tenant_id」的最重形态，走**每租户逻辑隔离**即可。
- **每公司多员工**：任务要能归属到人、可审计。
- **成本约束（决定性）**：**API 太贵，一律用登录态**（订阅 OAuth 令牌），不走按 token 计费的 API。
- **Claude 令牌两级**：公司设一个**公共令牌**默认全员用；员工可填**个人令牌**覆盖。

## 2. 现状（单租户假设埋点）

| 维度 | 现状 | 位置 |
|---|---|---|
| 鉴权 | 全局单个 `ADMIN_TOKEN`（Bearer / `?token=`），无用户无角色 | `internal/api/server.go` 鉴权中间件 |
| 数据 | `tasks / node_runs / events`，无 `tenant_id`/`user_id`，单个 `tasks.db` | `internal/core/store/sqlite.go` |
| 配置 | 单个 `config.json`(repos/source) + 单个 `.env`(全部凭据) | `internal/core/config/config.go` `Load()` |
| 凭据 | 明文 `.env`：`ATLASSIAN_*` / `LINEAR_*` / `GITHUB_TOKEN` / `SLACK_*` | `.claude/ai-workflow/.env` |
| 运行时 | 单 orchestrator 轮询、单 `repos/` 克隆根、单 `worktree` 根 | `internal/core/orchestrator`、`internal/core/runner` |
| Claude | runner 继承宿主 `os.Environ()`，靠 keychain 登录态 | `internal/core/runner/claude.go`（`cmd.Env` 注入处） |

## 3. Claude 登录态令牌模型（核心）

**机制**：runner 每次起 `claude` 时注入 `CLAUDE_CODE_OAUTH_TOKEN=<令牌>`（`claude setup-token` 生成的订阅 OAuth 长期令牌）。
这是登录态、非 API，不产生 token 计费；且**绕过 keychain**——控制面不再要求跑在图形登录会话内（宿主只需装 `claude` CLI）。

**两级解析（按任务归属人）**：

```
resolveClaudeToken(task):
    user := task.created_by
    if user.personal_claude_token != "":     # 员工个人令牌优先
        return user.personal_claude_token
    return tenant(task).shared_claude_token   # 回落公司共享令牌
    # 都为空 → 任务失败并提示「该租户/用户未配置 Claude 登录令牌」
```

落点：`runner/claude.go` 现有 env 注入处加一行
`env = append(env, "CLAUDE_CODE_OAUTH_TOKEN="+token)`。runner 需能拿到 task 的租户与归属人（见 §5）。

**取舍（须知）**：公司共享令牌背后是**一个订阅**，有速率窗口上限；多员工共用即共抢额度。想要独立额度就填个人令牌——两级设计正为此。

## 4. 数据模型

新增表：

```sql
CREATE TABLE tenants (
  id           TEXT PRIMARY KEY,      -- 租户（公司）
  name         TEXT NOT NULL,
  created_at   TIMESTAMP NOT NULL
);

CREATE TABLE users (
  id           TEXT PRIMARY KEY,
  email        TEXT NOT NULL UNIQUE,
  password_hash TEXT NOT NULL,        -- 平台自管（邮箱+密码+邀请）；SSO 以后再加
  created_at   TIMESTAMP NOT NULL
);

CREATE TABLE memberships (
  user_id      TEXT NOT NULL,
  tenant_id    TEXT NOT NULL,
  role         TEXT NOT NULL,         -- admin | member（viewer 以后）
  PRIMARY KEY (user_id, tenant_id)
);

-- 敏感凭据加密存储（§6）
CREATE TABLE tenant_secrets (
  tenant_id    TEXT NOT NULL,
  key          TEXT NOT NULL,         -- ATLASSIAN_API_KEY / GITHUB_TOKEN / CLAUDE_OAUTH_TOKEN / SLACK_WEBHOOK ...
  value_enc    BLOB NOT NULL,         -- 主密钥加密
  PRIMARY KEY (tenant_id, key)
);
CREATE TABLE user_secrets (
  user_id      TEXT NOT NULL,
  key          TEXT NOT NULL,         -- CLAUDE_OAUTH_TOKEN（个人令牌）
  value_enc    BLOB NOT NULL,
  PRIMARY KEY (user_id, key)
);
```

改造既有表：`tasks` 增列 `tenant_id`（非空）、`created_by`（用户 id）；`node_runs`/`events` 经 `task_id` 天然随任务归属，查询时按 `tenant_id` 过滤。
每租户配置（repos / source / status_map / default）从文件挪进 DB（如 `tenant_configs(tenant_id, json)`），`config.json` 仅留平台级默认。

## 5. 隔离

- **数据**：所有读写按 `tenant_id` 过滤；API 层从会话解析当前用户→其 membership→tenant，注入查询条件（防越权取他司任务）。
- **克隆/worktree**：`ReposDir`/`WorktreeBase` 按租户命名空间（`repos/<tenant_id>/...`、`/tmp/wf-worktrees/<tenant_id>/...`），避免跨司串仓。
- **运行时**：单轮询器遍历所有租户的 `AwaitingPRReview` 任务（PR 批量查询已支持跨仓）；runner 按 task 的 `tenant_id` 取该租户凭据/仓、按 `created_by` 解析 Claude 令牌。
- **并发**：`MAX_CONCURRENT` 从全局改为**每租户配额**，防一司占满。

## 6. 凭据与鉴权

- **凭据加密**：`tenant_secrets`/`user_secrets` 用**主密钥加密列**起步（主密钥经环境变量/文件注入，如 `MASTER_KEY`），AES-GCM。不接外部 secrets manager（少量公司够用，日后可换）。
- **鉴权**：`ADMIN_TOKEN` 单口令 → **用户会话**（邮箱+密码登录发 session/JWT）。API 中间件解析用户与 tenant；`role=admin` 才能管成员、改租户凭据/共享令牌；`member` 只能开/看本司任务、管自己的个人令牌。
- **平台超管**：留一个平台级管理入口做「开通租户 / 建首个 admin」，对应现在的手动开通。

## 7. 分阶段路径

1. **M1 身份地基**：`users`/`tenants`/`memberships` + 登录会话替换 `ADMIN_TOKEN`；`tasks` 加 `tenant_id`/`created_by`；所有查询按 tenant 过滤。（先不动凭据存储，仍读 `.env`）
2. **M2 令牌两级**：`tenant_secrets`/`user_secrets` + 加密列；runner 注入 `CLAUDE_CODE_OAUTH_TOKEN`（两级解析）；设置页加「公司共享令牌 / 我的个人令牌」。
3. **M3 配置下沉**：per-tenant repos/source/凭据从 `.env`/`config.json` 迁进 DB；克隆/worktree 命名空间隔离；每租户并发配额。
4. **M4 打磨**：角色细化（viewer）、审计视图、SSO（可选）。

## 8. 待定 / 风险

- **令牌有效期与轮换**：`setup-token` 令牌过期/失效时的探测与提示（复用现有 `authFailed` 识别）。
- **员工离职**：撤 membership 即断其个人令牌与任务归属；共享令牌不受影响。
- **共享令牌速率窗口**：多员工争抢时的排队/降级策略（M4 再定）。
- **主密钥管理**：`MASTER_KEY` 丢失=全租户凭据不可解；需备份约定。

## 9. 部署形态

**决定性事实**：本系统**不是无状态 Web 应用**——要起 `claude` CLI 子进程、跑 `git`/`gh`、克隆仓、建 worktree、常驻轮询、SQLite 落盘、执行 shell/python skill。
因此必须跑在**有完整 OS + 文件系统 + 能起子进程 + 长驻进程的持久主机**上，serverless/edge 一律不行。

| 平台 | 能当计算主机 | 定位 |
|---|---|---|
| Cloudflare | ❌ Workers 无子进程/无文件系统/无长驻 | 入口层：Tunnel/Access（TLS + 零信任 + 不暴露公网 IP）、前端 Pages |
| AWS | ✅ **EC2 虚机 / ECS-Fargate**（**非 Lambda**：15 分钟上限、临时 FS、留不住登录态） | 计算主机 |
| GitHub | ❌ 不托管长驻服务；Actions 是临时 CI | 集成目标（目标仓 / PR），非部署目标 |

**推荐**：一台 **Linux 虚机**（EC2 或省钱 VPS）跑 Go 控制面 + 每租户逻辑隔离，前挂 **Cloudflare Tunnel + Access**。
关键前提：§3 的 `CLAUDE_CODE_OAUTH_TOKEN` 注入让**无头虚机成立**（不再需要图形登录会话 / keychain）。规模内**先一台 VM 足矣，别过早上 K8s**。

### EC2 规格（3–4 人 · 日 15–40 工单）

> **推理不在本机**：登录态订阅下，模型「思考」在 Anthropic 服务端；EC2 上 `claude` CLI 是编排工具调用 + 跑 git/gh/grep/编辑 + 等网络，**中等内存 + 突发 CPU**。别按「跑大模型」规格想。

- 峰值并发低，`MAX_CONCURRENT=3`（默认）够；**真正瓶颈是内存**：每并发 `claude`(Node) 约 0.5–1.5GB。

| 场景 | 实例 | 规格 | 说明 |
|---|---|---|---|
| **起步（推荐）** | `t4g.large` | 2 vCPU / 8 GB / ARM | 覆盖 3 并发 + 余量；突发型 CPU 配突发任务；约 $49/月常开，预留价再降 ~40% |
| 任务常跑 test/build 或提并发 | `t4g.xlarge` | 4 vCPU / 16 GB | 硬关卡吃 CPU+内存时上 |
| 要固定性能（不看 CPU 积分） | `m7g.large` | 2 vCPU / 8 GB | 持续并发跑构建时避免 t 系积分耗尽降速 |

- 别用 micro/small（1–2GB）：一个大上下文 `claude` 就可能 OOM。ARM(t4g/Graviton) 比 x86 省 ~20%，`claude`/`gh`/`git`/`node` 均支持。
- **磁盘**：EBS gp3 起步 **50–80 GB**（OS ~10 + 每租户仓克隆几×0.2–1GB + 临时 worktree 几 GB + SQLite/日志）；SQLite 与克隆盘做快照备份。
- 加 **2–4GB swap** 兜底 Node 内存尖峰防 OOM。
- **结论**：先 `t4g.large` + 80GB gp3 + 4GB swap + `MAX_CONCURRENT=3`，观察内存/CPU 积分，不够无缝升 `t4g.xlarge`。
- 依赖：宿主装 `claude` / `gh` / `git` / `python3`；Go 为单静态二进制。
