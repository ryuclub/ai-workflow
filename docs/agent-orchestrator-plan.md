# M7：AI 总调度 Agent + 对话窗口

> 状态：方案定稿（2026-07-03 讨论确定）。四个关键决策：**常驻 claude CLI 会话 / 全自动+白名单 / 每租户一个 Agent / 一期到位含写操作**。

## 1. 背景与定位

现状是「机械执行 + 移交」：`orchestrator` 是固定状态机（B→人审→C→PR审→D），触发靠 Dashboard 手点 + PR review 轮询；没有任何角色在**看全局、做决策、和人对话**。

M7 引入一个**每租户一个的常驻 AI Agent**，定位为「值班工程师」：

- **监控**：订阅本租户全局事件流，任务失败/卡住时主动分析并发话（聊天窗 + Slack）；
- **调度**：通过工具执行动作（起任务/取消/重跑/触发修订…），白名单内直接执行，白名单外生成待确认卡片由人点头；
- **对话**：Dashboard 常驻聊天窗，随时问现状、查原因、下指令。

**不改变的东西**：状态机本身、人审闸口语义、`/api/v1` 既有契约。Agent 的一切行动都经 `Orchestrator` 公开方法走原有路径，全程落事件可审计。

## 2. 执行通路：常驻 claude CLI 会话

复用现有令牌体系（`claude setup-token` OAuth 登录态令牌，vault 两级解析），**零新增计费体系**。

每租户一个常驻子进程（懒启动、空闲回收、可恢复）：

```
claude -p --input-format stream-json --output-format stream-json \
  --include-partial-messages \
  --session-id <uuid>            # 首次；恢复时改用 --resume <uuid>
  --tools "" --strict-mcp-config \
  --mcp-config '<内联 JSON：指向控制面 /internal/mcp>' \
  --allowedTools "mcp__wf__*" --permission-mode bypassPermissions \
  --system-prompt <值班调度角色提示（中文）>
```

- **内置工具全禁**（`--tools ""`），只暴露我们的 MCP 工具 → `bypassPermissions` 的风险面仅限我们自己实现的工具，权限控制在工具服务端做（白名单策略），不依赖 CLI 权限层。
- **工作目录**：`<DataDir>/agent/<tenantID>`（空目录，无 CLAUDE.md 干扰）。
- **令牌注入**：与 runner 相同——`filterEnv` 剔宿主 `CLAUDE_CODE_OAUTH_TOKEN`/`ANTHROPIC_API_KEY` 后注入租户共享令牌；无 vault 时回落宿主登录态（单机开发）。
- **生命周期**：首条消息/首次事件唤醒时启动；空闲 `AGENT_IDLE_MIN`（默认 30 分钟）后杀进程；`claude_session_id` 持久化，重启后 `--resume` 找回上下文。
- **stdin 协议**：`{"type":"user","message":{"role":"user","content":"..."}}`（一行一条）；stdout 解析 `system/init`（拿 session_id）、`assistant`（完整消息，落库）、`stream_event`（增量，转 SSE 不落库）、`result`（回合结束）。

## 3. 工具服务：`/internal/mcp`（Streamable HTTP MCP）

控制面内嵌一个极简 MCP 服务端（JSON-RPC：`initialize` / `tools/list` / `tools/call`，POST 单响应即可，无需 SSE）。鉴权：`X-Internal-Token`（沿用）+ `X-WF-Tenant`（由我们写进 mcp-config，子进程无法伪造他租户）。

### 工具清单

| 工具 | 类型 | 说明 |
|---|---|---|
| `list_tasks` | 读 | 本租户任务列表（可按状态过滤），返回 #seq/票号/标题/状态 |
| `get_task` | 读 | 任务详情（含 Issue/PR 链接、错误、轮次） |
| `list_task_events` | 读 | 任务事件流（尾部 N 条） |
| `read_task_log` | 读 | B/C/D 阶段 claude 全量日志尾部（排障用） |
| `list_tickets` | 读 | 票源候选票 |
| `get_health` | 读 | 健康面板快照 |
| `start_task` | 写 | 起新任务（票号+仓） |
| `restart_task` | 写 | 对已结束任务的票重新起任务 |
| `cancel_task` | 写 | 取消运行中任务 |
| `approve_issue` | 写 | 人审通过（**硬性仅确认制**，见下） |
| `reject_issue` | 写 | 人审打回 |
| `approve_pr` | 写 | PR 审查通过（**硬性仅确认制**） |
| `request_revise` | 写 | 立即触发一轮 PR 修订 |

### 权限策略（全自动 + 白名单）

- 读工具：直通。
- 写工具：查租户配置 `agent_auto_actions`（白名单，存租户配置 JSON）。
  - **命中白名单** → 直接执行，结果回给 agent，同时落 `agent.action` 事件（审计）。
  - **未命中** → 落 `agent_actions` 表（pending），工具立即返回「已创建待确认动作 #n」，聊天窗渲染确认卡片；人点「确认」后执行，结果以系统消息注入会话，agent 续答。
- **硬性例外**：`approve_issue` / `approve_pr` 是人审闸口的语义本体，**默认走确认制**，白名单配置无法放行——确认动作本身即人审。
  - **设置门控的例外**（后续追加）：租户开启「塔台自动审核」（`agent_auto_review`）后，`approve_issue` 与 `update_issue` 对塔台直通——Issue 产出即唤醒塔台审核，无阻碍且选项明确时自动通过继续实装；有真阻碍/选项不明确时调 `escalate_review` 点亮人审节点并 Slack 提醒（此模式下原始「等待审核」Slack 提醒被抑制）。`approve_pr` 不受该开关影响，恒需人工。流程图对启用租户多一个「塔台审核」节点（`pipeline.ForTenant`）。
- 默认白名单：`["cancel_task","restart_task","request_revise"]`（失败重跑、卡死取消、催修订这类低风险恢复动作）。

## 4. 数据模型（SQLite 增量迁移）

```sql
-- 事件表补租户列（全局订阅/按租户拉取用）
ALTER TABLE events ADD COLUMN tenant_id TEXT DEFAULT '';
CREATE INDEX idx_events_tenant ON events(tenant_id, id);

CREATE TABLE agent_sessions (          -- 租户 ↔ claude 会话（resume 用）
  tenant_id TEXT PRIMARY KEY,
  claude_session_id TEXT NOT NULL,
  updated_at TIMESTAMP NOT NULL
);
CREATE TABLE agent_messages (          -- 对话历史；id 作 SSE 游标
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  tenant_id TEXT NOT NULL,
  role TEXT NOT NULL,                  -- user / assistant / system
  kind TEXT NOT NULL,                  -- chat / wake / action_request / action_result
  content TEXT NOT NULL,
  user_id TEXT DEFAULT '',             -- role=user 时的发言人
  created_at TIMESTAMP NOT NULL
);
CREATE TABLE agent_actions (           -- 待确认写操作
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  tenant_id TEXT NOT NULL,
  tool TEXT NOT NULL,
  args_json TEXT NOT NULL,
  summary TEXT NOT NULL,               -- 给人看的一句话
  status TEXT NOT NULL,                -- pending / executed / denied / failed
  result TEXT DEFAULT '',
  decided_by TEXT DEFAULT '',
  created_at TIMESTAMP NOT NULL,
  decided_at TIMESTAMP
);
```

`store.Event` 增加 `TenantID`；`events.Bus` 增加 `SubscribeTenant(tenantID)`（nudge 语义同现有按 task 订阅）；`store` 增加 `ListTenantEvents(tenantID, sinceID)` 与 agent 三表 CRUD。

## 5. 事件唤醒（core/agent watcher）

每租户 watcher 订阅全局事件流，规则命中即以 `[系统事件]` 前缀注入会话唤醒 agent：

| 规则 | 触发 | 去重 |
|---|---|---|
| 任务失败 | `task.failed`（→ 待裁决） | 每任务每次失败一次 |
| 人审滞留 | `awaiting_review` 超 `AGENT_REVIEW_REMIND_H`（默认 24h，定时扫描） | 每任务每状态一次 |
| PR 审查滞留 | `awaiting_pr_review` 超 48h | 同上 |

Agent 被唤醒后按系统提示：先用读工具（`read_task_log`/`list_task_events`）查明原因，产出**简短结论 + 建议动作**；白名单内可自行恢复。主动消息落 `agent_messages` 推聊天窗，同时以 `agent.notice`（level=warn）事件走既有 SlackSink 转 Slack。

Token 控制：**不灌全量事件流**，只在规则命中时注入摘要；空闲回收 + resume 保上下文。

## 6. API（`/api/v1/agent/*`，requireAuth，租户隔离）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/agent/messages?since=<id>` | 历史消息（增量） |
| POST | `/agent/messages` | 发消息 `{content}` → 落库、转会话（未启动则拉起） |
| GET | `/agent/stream` | 租户级 SSE：`agent.message`（落库消息，含 id 游标）+ `agent.delta`（流式增量，不落库）+ `agent.action`（确认卡状态变化） |
| GET | `/agent/actions?status=pending` | 待确认动作 |
| POST | `/agent/actions/:id/confirm` | 确认执行（成员均可；确认即该动作的人审） |
| POST | `/agent/actions/:id/deny` | 拒绝（可带理由，注入会话） |

## 7. 前端：ChatPanel

- 顶栏新增入口，右侧抽屉式聊天窗（宽度可拖、状态本地保存，沿用侧栏习惯）。
- SSE 订阅 `/agent/stream`：整条消息按游标增量拉齐（复用现有「nudge+拉库」模式）；`agent.delta` 做打字机效果。
- 消息渲染 markdown（复用 react-markdown）；`action_request` 渲染确认卡片（工具+参数摘要+确认/拒绝按钮）；主动消息（wake 产出）未读角标。

## 8. 配置增量

| 键 | 位置 | 默认 | 说明 |
|---|---|---|---|
| `AGENT_ENABLED` | env/运行期 | `true` | 总开关 |
| `AGENT_MODEL` | env | 空（CLI 默认） | agent 会话模型 |
| `AGENT_IDLE_MIN` | env | `30` | 空闲回收分钟数 |
| `AGENT_REVIEW_REMIND_H` | env | `24` | 人审滞留提醒小时数 |
| `agent_auto_actions` | 租户配置 JSON | `["cancel_task","restart_task","request_revise"]` | 写操作自动执行白名单 |

## 9. 分层与安全

- 新包 `internal/core/agent`（纯领域，禁 gin）：会话管理器、watcher、工具执行策略；MCP 的 HTTP 端点薄壳放 `internal/api`（JSON-RPC 编解码 → 调 core）。
- 子进程令牌注入 = runner 同款两级隔离；MCP 请求带租户头由控制面固化在 mcp-config，agent 无法跨租户。
- 所有写操作（无论直通或确认后）都发布 `agent.action` 事件，任务详情与 Slack 可见，全程可审计。

## 10. 验收（端到端）

1. 聊天窗问「现在有哪些任务卡着」→ agent 用读工具汇总回答；
2. 让 agent 起一个新任务 → 白名单外 → 出确认卡 → 确认后任务真实创建；
3. 白名单内动作（如取消运行中任务）→ 直接执行并回报；
4. 人为制造 C 段失败 → agent 主动发话：失败原因分析（引用日志）+ 建议，Slack 收到通知；
5. 重启控制面 → 聊天历史完整、会话 resume 上下文不丢；
6. `go build ./... && go vet ./...`、`cd frontend && npm run build` 通过。
