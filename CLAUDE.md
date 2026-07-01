# CLAUDE.md

> AI 工作流流水线（控制面 + Dashboard）的协作入口。本文件**只做导航 + 行为合同**，不抄规范正文。

## 1. 是什么 / 怎么跑

- 产品简介、架构、启动：[`README.md`](./README.md)
- 完整设计与决策记录：[`docs/ai-workflow-rebuild-plan.md`](./docs/ai-workflow-rebuild-plan.md)

## 2. 怎么写代码

- 分支 / commit：[`.claude/guidelines/branch.md`](./.claude/guidelines/branch.md)
- 编码规约：[`.claude/guidelines/coding.md`](./.claude/guidelines/coding.md)
- 工作流：[`.claude/guidelines/workflow.md`](./.claude/guidelines/workflow.md)
- JIRA：[`.claude/guidelines/jira.md`](./.claude/guidelines/jira.md)
- 提交前自审：[`.claude/guidelines/pre-commit-review.md`](./.claude/guidelines/pre-commit-review.md)

### 分层硬约束
- `internal/core/` 是纯领域层，**禁止 import gin / 任何 web 依赖**；HTTP 只存在于 `internal/api/`。
- 对外只认 `/api/v1` 契约；`/internal` 不公开、不版本化（仅 skill 事件回传）。
- 票源经 `core/source.Provider` 抽象，新增源 = 加一个 provider，不改上层。

## 3. AI 行为合同（硬约束）

- ✅ 全程**中文**输出（代码注释 / commit / PR / JIRA·Linear 评论 / Review）
- ❌ 不署名（commit 不加 `Co-Authored-By`；PR/Review 不提 AI 工具名）
- ❌ 不擅自 `push` / `merge` / 远程操作——须用户明确同意
- ❌ 不动 `.idea/`、`config.json`、`.claude/ai-workflow/.env`（机器相关/含密钥）
- ⚠️ 控制面须跑在能访问 `claude` 登录态的图形登录会话内（headless 假成功坑）

## 4. 关键概念

- **控制面 / 执行面分离**：本仓是控制面（不被自动化）；目标仓才是被改代码的执行面。
- **信号模型**：GitHub label = 状态机（待审核/已审核/已实装/待裁决）；事件经 `/internal` 回传点亮流水线、经 SSE 推 Dashboard、经 sink 转 Slack。
