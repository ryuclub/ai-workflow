# AI 工作流流水线 · 重构方案

> 状态：**待确认**。本文是把本仓从「example-channel-service + 挂载的事件驱动工具」整个翻新成
> 一个**独立的 AI 工作流自动化产品**（多仓 + Dashboard + 实时进度）的落地方案。
> 当前阶段目标：**只做「工作流编排 + 可视化」**。参考 n8n 的运行视图外观，但流程固定。

## 0. 决策基线（已拍板）

| 维度 | 选择 |
|------|------|
| 可视化形态 | 用 **React Flow 完整可拖拽画布**（拖拽/缩放/自定义节点/连线/点开看详情，n8n 外观，白送）；当前**节点集合与连线语义固定**（改边暂不改变执行），任务/节点数据按 **DAG 建模**，预留补「通用节点执行引擎」后让编辑真正生效 |
| 前端栈 | **React + Vite SPA**，节点图用 React Flow |
| 后端栈 | **Go + Gin + swag(OpenAPI) + SQLite(纯 Go 驱动)** 单服务（与全线 Go 后端统一，团队长期维护）；`server.py` 的 worktree/claude 编排移植为 Go `os/exec`+goroutine。**JIRA 不在 Go 里重写**：把现有 `jira_api.py`（含 ADF 解析、`search/get/transition`）当外部工具 `subprocess` 调用 |
| 进度采集 | **阶段级**：把 skill 里现有 `notify.sh` 播报点改造成结构化事件 → 后端 → SSE 推前端 |
| 人审闸口 | **收进 Dashboard 审批按钮**：后端编排，流水线暂停在「等人审」节点，人在 UI 点「通过/打回」 |
| 目录布局 | **完全重构** |
| 触发模型 | 从「被动 webhook」改为「Dashboard 主动点开始」；webhook 接收器**退役** |
| 票源 | 抽象 `Provider` 接口 + 归一化 `Ticket`；**JIRA 与 Linear 二选一**（配置选活跃源，非同时）。JIRA 复用 `jira_api.py`，Linear 用 Go GraphQL。Task 以 `Source`+`SourceID` 标识，校验交各 provider。列表端点跨源 + 增强（建议仓 / 已起任务） |

## 1. 产品形态（一句话）

> 一个常驻 Web 服务：打开 Dashboard → 看到 **JIRA 票列表** → 选一张票 + 选目标仓 → 点**开始任务** →
> 后端在该仓 worktree 里编排「调查→建 Issue→**人审**→实装→测试→PR」全流程 →
> Dashboard 用**固定流水线节点图**实时显示每个任务推进到哪一阶段、点开看该阶段日志。

## 2. 控制面 / 执行面的分离（关键概念，不要混）

- **控制面（本仓 = 新产品）**：Go(Gin) 后端 + React 前端 + 任务编排 runner。它**不是**被自动化的业务仓。
- **执行面（各目标仓）**：真正被改代码的业务仓（如 Channel-Service / Group-Service）。
  它们各自 `.claude/` 里装着 B/C 两个 skill；后端用 `git worktree` 在目标仓里跑 `claude -p`。
- 两套 `.claude/` 不同：本仓的 `.claude/` 是「可安装工具单元 + 跑在本仓的 skill」；
  目标仓的 `.claude/` 是被 `claude -p` 实际读取执行的那份。

## 3. 链路与阶段（固定流水线，10 节点）

```
[1读JIRA]→[2调查]→[3建Issue]→⏸[4人审]→[5建分支+蓝图]→[6实装]→[7测试]→[8提PR]→⏸[9 PR审查]⇄[10 按意见修订]
└──── B：jira/linear-to-issue ────┘  gate  └───────── C：issue-to-pr ─────────┘   gate    D：pr-revise（循环）
```

- 节点 1–3 = skill `jira-to-issue` 或 `linear-to-issue`（B，**按活跃票源二选一**）的内部阶段；产物 = GitHub Issue「待审核」。两个 skill 结构同构，仅取数换源（JIRA `jira_api.py` / Linear GraphQL）；phase 标识一致（`B.read/B.investigate/B.issue`），故与源无关地映射到同一批节点。
- 节点 4 = **人审闸口（Issue）**（gate）：流水线暂停。Dashboard **就地渲染并编辑该 GitHub Issue**（markdown 编辑器 + 预览），保存即写穿透回 GitHub，再点「通过/打回」（后端切 `已审核`/`待裁决` label；`已审核` 等状态 label 若目标仓尚不存在会先幂等创建）。**人不必打开 github.com**；但 Issue 仍存在于 GitHub 作后端存储。GitHub token 只待后端。末次写入覆盖（暂不做强冲突检测）。
- 节点 5–8 = command `issue-to-pr`（C）的 Phase；产物 = Draft PR + Issue「已实装」。
- 节点 9 = **PR 审查闸口**（gate）：出 PR 后暂停等人在 GitHub 上 review。控制面**后台轮询 `gh reviewDecision`**（默认 30s），Dashboard 亦提供「确认通过 / 去修订」按钮兜底：
  - `APPROVED` → 完成（终态 `done`）；
  - `CHANGES_REQUESTED` → 进节点 10 修订；
  - **幂等游标**只对「新于上轮的决定性 review」反应，纯 `COMMENTED` 评论不触发，避免重复起修订。
- 节点 10 = skill `pr-revise`（D）：checkout PR 分支 → 读未处理 review threads → 按意见改 → 构建/测试硬关卡 → push 回同分支 → 逐条回复/resolve → 回到节点 9 等**再次 review**。**可多轮**（9⇄10，本仓第一个有环流水线）；**超 5 轮或修订失败 → 待裁决**（沿用「失败即停」哲学）。
- 每个节点状态：`待跑 / 运行中 / 成功 / 失败 / 等待人工`。失败即停（保留分支），整任务转「待裁决」。
- **票源状态联动**：任务态经 `status_map` 流转票源状态（`orchestrator.syncJira`）；JIRA 走 `jira_api.py transition`，Linear 走 GraphQL `issueUpdate`（按 team 工作流状态名解析 `stateId`）。`status_map` 默认空 = 不联动（防污染真实票）。

## 4. 目标目录布局（完全重构后）

```
workflow-demo/
├── README.md                     # 产品说明（替换原 Channel-Service README）
├── CLAUDE.md                     # 精简为新产品导航 + 行为合同
├── config.example.json           # 多仓登记表（default + repos[name]{github,path,match}）
├── .env.example                  # JIRA 凭据 / SLACK / 内部事件 token / PORT
├── go.mod / go.sum               # 后端依赖（gin, swag, modernc.org/sqlite…）
├── cmd/
│   └── server/main.go            # 启动 Gin app（挂 v1 + internal 路由）+ 静态托管前端构建产物
├── internal/
│   ├── api/                      # —— 薄 HTTP 适配层（handler 只校验→调 service→序列化），无业务逻辑 ——
│   │   ├── middleware.go         # 认证 seam（公共 API 的 API-Key/Bearer；本阶段 localhost 放行）+ CORS
│   │   ├── dto.go                # 请求/响应 DTO + swag 注解 = 对外契约（不直吐内部存储结构）
│   │   ├── v1_jira.go            # GET /api/v1/jira/issues（列票，支持 JQL/项目过滤）
│   │   ├── v1_repos.go           # GET /api/v1/repos（读 config.json）
│   │   ├── v1_tasks.go           # POST /api/v1/tasks（202+任务资源，支持 Idempotency-Key）/
│   │   │                         #   GET 列表/详情 / GET {id}/issue + PUT {id}/issue（就地渲染/编辑）/
│   │   │                         #   POST {id}/approve|reject（人审）/
│   │   │                         #   GET {id}/events（SSE）/ POST {id}/callbacks（注册出站回调）
│   │   └── internal_ingest.go    # POST /internal/tasks/{id}/event（skill 回传阶段事件，token+localhost）
│   └── core/                    # —— 纯领域层：禁止 import gin（可被 CLI/队列/gRPC 复用）——
│       ├── config/               # 读 config.json + .env
│       ├── jira/                 # 薄封装：subprocess 调 jira_api.py（search/get/transition），解析 JSON
│       ├── github/               # 薄封装：gh/REST 取改 Issue（GetIssue/UpdateIssue/SetLabels），供人审就地编辑
│       ├── pipeline/             # 流水线/阶段定义（DAG 数据模型，当前线性）
│       ├── orchestrator/         # 状态机：B → 等人审 → C；失败→待裁决
│       ├── runner/               # worktree + claude -p（os/exec，注入 WF_TASK_ID/事件URL）
│       ├── events/               # 事件总线 + 可 fan-out 的 sink（SSE/Slack/HTTP 回调），goroutine+channel
│       └── store/                # 接口 + SQLite 实现（modernc.org/sqlite，纯 Go 无 cgo；可换 Postgres）
├── docs/swagger/                 # swag init 产物（OpenAPI 契约）
├── frontend/                     # React + Vite SPA
│   ├── package.json / vite.config.ts / index.html
│   └── src/
│       ├── App.tsx
│       ├── api/                  # REST 客户端 + SSE 订阅
│       └── components/
│           ├── JiraTicketList.tsx   # 入口：JIRA 票列表 + 选仓下拉 + 「开始任务」
│           ├── TaskBoard.tsx        # 任务列表/卡片
│           ├── PipelineView.tsx     # 固定流水线节点图（React Flow，n8n 外观）
│           ├── StageNode.tsx        # 单节点：状态灯 + 名称
│           ├── ReviewGate.tsx       # 人审节点：看 Issue 摘要 + 通过/打回按钮
│           └── StageLogPanel.tsx    # 点节点看该阶段日志
└── .claude/                      # 保留：被 claude -p 调用的可安装工具单元
    ├── ai-workflow/
    │   ├── jira_api.py           # **JIRA 唯一逻辑源**：Go 后端（列票/读票/回写）+ worktree 内 skill 共用，均 subprocess 调用
    │   ├── emit-event.sh         # 新：结构化阶段事件上报（读 WF_TASK_ID + 事件URL + token）
    │   ├── notify.sh             # 改造：既发 Slack 也调用 emit-event（双写，过渡期）
    │   └── install-into.sh       # 保留：把 B/C + 工具装进新目标仓
    ├── skills/jira-to-issue/     # B（JIRA 源）；播报点从「只发 Slack」改为「发结构化阶段事件」
    ├── skills/linear-to-issue/   # B（Linear 源）；与 jira-to-issue 同构，取数走 GraphQL
    ├── skills/pr-revise/         # D：按 PR review 意见修订（checkout PR 分支→改→push→回复/resolve）
    ├── commands/issue-to-pr.md   # C；同上
    #  注：runner 每次跑任务把 B/C/D skill 包临时注入目标仓 worktree，跑完随 worktree 清理
    └── guidelines/               # 保留通用项（branch/coding/jira/pre-commit-review）
```

### 删除清单（Channel-Service 相关，全清）

`cmd/`、`design/`、`deploy/`、`Dockerfile`、`.dockerignore`、`ARCHITECTURE.md`、`go.mod`、`go.sum`、
`.github/workflows/deploy.yml`、所有 `*.go`、频道专属的 `guidelines/directory-structure.md`、
`guidelines/templates/swagger.md`、`guidelines/test-spec.md`、`skills/gin-api-docs/` 等只服务于
频道 Go 服务的内容。`server.py`（旧 webhook 接收器）逻辑被 `runner.py` 吸收后删除。

## 5. 数据模型（DAG-ready）

```
Repo      { name, github, path, match[], base? }                  ← 来自 config.json
Pipeline  { id, nodes[], edges[] }                                ← 内置流水线；含 9⇄10 回边（有环）
Node      { id, name, kind: auto|gate|terminal, skill?, phaseKeys[] }  ← 阶段定义
Task      { id, source, source_id, repo, pipeline_id, state,
            issue_url?, issue_num?, pr_url?, pr_num?,             ← PR 审查循环用 pr_num
            review_round?, review_cursor?, created_at }           ← 修订轮次 + 幂等游标
NodeRun   { task_id, node_id, state: pending|running|ok|fail|waiting, started_at, ended_at }
Event     { task_id, node_id?, ts, level, message }               ← skill 回传 + 后端编排日志

任务状态机：queued → running_b → awaiting_review → running_c
            → awaiting_pr_review ⇄ running_d → done
            旁路终态：skipped（无需处理）/ adjudication（待裁决）/ rejected / canceled
```

阶段事件如何映射到节点：skill 上报时带 `phase` 标识（如 `B.investigate`、`C.test`），
后端按 `Node.phaseKeys` 把事件归到对应节点并更新 `NodeRun.state`。

## 6. 实时进度链路（阶段级）

```
claude -p（worktree 内跑 skill）
  └─ 阶段边界调用 emit-event.sh
       └─ POST http://127.0.0.1:PORT/internal/tasks/{WF_TASK_ID}/event  (X-Internal-Token 校验)
            └─ 后端 core/events 落库 + 广播
                 └─ SSE GET /api/v1/tasks/{id}/events
                      └─ 前端 PipelineView 节点亮灯 / StageLogPanel 追加日志
```

- `WF_TASK_ID` / 事件 URL / token 由 `core/runner` 在 spawn `claude -p` 时通过环境变量注入 worktree。
- 过渡期 `notify.sh` 双写（Slack + 内部事件），稳定后可只留内部事件。

## 6b. 面向集成的 API-first 设计（Dashboard 只是第一个消费者）

> 原则：业务逻辑全在 `core/` 服务层，`api/` 只做 HTTP 适配。Dashboard、外部系统、未来的 CLI/队列
> 都是同一 service 的平等消费者，不给任何客户端开后门。净增成本只是目录纪律 + 少量 schema。

- **分层硬约束**：`internal/core/` 禁止 `import gin`/任何 web 依赖；HTTP 仅存在于 `internal/api/`。
- **公共 vs 内部分离**：
  - 公共 API `/api/v1/*`：版本化，经 **swag 注解**生成 `/swagger`（OpenAPI），作为对外契约。
  - 内部 API `/internal/*`：skill 事件回传，token + localhost，**不公开、不版本化、不进契约**。
- **认证 seam**：公共 API 预留 API-Key/Bearer 依赖 + CORS 白名单；**本阶段 localhost 开发放行**，开外网时启用中间件即可，不动业务码。
- **异步作业语义**：`POST /api/v1/tasks` → **202 + 任务资源**（不阻塞）；支持 `Idempotency-Key` 防外部重试重复起活。
- **双消费模式**：
  - 拉：`GET /api/v1/tasks/{id}`、SSE `events`。
  - 推：外部系统注册**出站回调 webhook**，任务到「等人审/完成/失败」时回调。
- **可 fan-out 的通知 sink**：同一事件多路分发——SSE（前端）/ Slack / HTTP 回调（外部系统）。Slack 不再是特例，只是一种 sink。
- **稳定事件 schema**：typed 事件（`task.created / node.started / node.completed / task.awaiting_review / task.completed / task.failed`，Go struct + swag）作为对外契约，不直吐内部存储结构。
- **存储隔离**：`store` 接口化，现 SQLite，量大可换 Postgres，不动上层。
- **现在不做（分层不挡，留待真有外部接入）**：OAuth2 / 多租户 / RBAC、gRPC、消息中间件、API 网关。

## 7. 落地阶段（实现顺序）

> 每阶段自成可验证单元；遵循「没端到端跑通前不提交」。

- **P0 清理**：删除 Channel-Service 相关全部内容（§4 删除清单）。*破坏性，执行前再确认一次。*
- **P1 后端骨架**：Go+Gin + config + JIRA（subprocess 调 jira_api.py 列票/读票）+ repos + task store(SQLite) + SSE 事件总线 + 内部事件 ingest + swag。
- **P2 编排 + runner**：orchestrator 状态机（B→等人审→C→待裁决）+ runner（os/exec worktree + claude -p + 注入）。
- **P3 skill 改造**：`emit-event.sh` + B/C 播报点改结构化事件 + 注入变量读取。
- **P4 前端**：React+Vite SPA —— JIRA 列表入口 + 任务流水线节点图 + SSE 实时 + 审批按钮。
- **P5 串通**：本地端到端跑一条真实 PROJ 票（选仓→开始→看流水线推进→人审→出 PR），补 README/config/.env.example。

## 8. 风险与注意

- **claude 登录态**：`claude -p` 的 headless 认证依赖 keychain/登录会话，后台服务环境够不到会「假成功」。
  后端须跑在图形登录会话，或配 `CLAUDE_CODE_OAUTH_TOKEN`/`ANTHROPIC_API_KEY`。沿用现有告警。
- **安全面**：内部事件端点仅监听 `127.0.0.1` + token 校验；起任务命令面固定（只派 `/jira-to-issue`、`/linear-to-issue`、`/issue-to-pr N`、`/pr-revise N`，正则校验）。
- **PR 审查循环收敛**：修订循环靠「幂等游标（只对新决定性 review 反应）+ 轮次上限（默认 5 轮）」双保险防死循环；`pr-revise` 须**范围克制**（只改意见涉及处，不顺手重构），否则 review 面扩大、循环难收敛。超限即转「待裁决」交人。
- **生产保护**：本产品不触碰任何 prod；C 的 PR base 仍按目标仓 `branch.md`/默认分支，不写死。
- **不提交/不推送**：端到端验证通过前不 commit；push/merge 须用户明确同意。
```
