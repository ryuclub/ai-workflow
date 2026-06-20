# 开发流程编排器

接收工单号或任务描述，从需求确认到 PR 提交一贯执行。
各阶段在承认点向用户确认后再继续。

## 输入

`$ARGUMENTS` 传入工单号（例: `MOS-1234`）或任务描述。

## 参照文档

以下文件在各 Phase 指定时机读取。不嵌入内容，必须从文件读取最新版本。

| 文件 | 读取时机 |
|------|---------|
| `.claude/guidelines/workflow.md` | Phase 0 全流程参照 |
| `.claude/guidelines/branch.md` | Phase 0 分支命名规范 |
| `.claude/guidelines/coding.md` | Phase 3 编码规约 |
| `.claude/guidelines/pre-commit-review.md` | Phase 3 自查、Phase 8 提交前审查 |
| `.claude/guidelines/jira.md` | Phase 0 工单规范 |
| `design/changes/{工单号}.md` | Phase 1 设计文档确认、Phase 2 方案制定 |
| `.claude/business-knowledge/` | Phase 1 相关领域知识参照、Phase 2 方案制定参照、Phase 8 知识积累 |

## 执行步骤

### Phase 0: 初始化

1. 读取 `.claude/guidelines/workflow.md` — 确认完整开发流程
2. 读取 `.claude/guidelines/branch.md` — 确认分支命名规范
3. 从 `$ARGUMENTS` 提取工单号
   - `MOS-XXXX` 格式 → JIRA 工单号
   - 其他 → 任务描述
4. 工单号存在时:
   - 用 `jira-manage-ticket` 技能获取工单信息
   - 确认工单描述栏中的文档链接（设计书、Confluence 等）并全部读取
   - 从分支名推定工单号（`git branch --show-current`）
5. **分支确认**（必须在开始前确认）:
   - `git branch --show-current` — 当前分支名
   - `git log --oneline -5` — 最新提交
   - 基础分支确认（本项目始终为 `stage`）
   - 确认当前分支是否与工单对应（不一致时向用户确认）
   - `git status` — 是否有未提交的变更

### Phase 1: 需求确认

1. 用 Agent(Explore) 并行调查:
   - JIRA 工单需求（有工单号时）
   - 工单中记载/链接的文档（设计书、Confluence 等）
   - 相关现有代码（`internal/` 下）
   - 设计文档（`design/changes/` 下）
   - `.claude/business-knowledge/` 下相关领域的业务知识
   - 当前分支的 diff（`git diff stage...HEAD`）— 已有变更时
2. 向用户提示调查结果（工单信息、参照文档一览、相关代码、影响范围、确认事项）
3. **等待用户确认**: 「这个理解正确吗？有修正请指出」
4. 确认后进入 Phase 2

### Phase 2: 方案制定

1. 用 Agent(Plan) 设计实现方案（基于工单文档、设计书，包含变更文件一览、实现顺序、风险点）
   1. 设计文档参考 /design/base
   2. 各个API详细设计参考 /design/base/API/API-*
2. 如 `design/changes/{工单号}.md` 不存在，创建设计文档
3. 向用户提示方案
4. **等待用户确认**: 「按此方案推进可以吗？」
5. 确认后进入 Phase 3

### Phase 3: 实现 + 自查循环（最多 3 次）

**预先读取**:
- `.claude/guidelines/coding.md` — 编码规约
- `.claude/guidelines/pre-commit-review.md` — 审查基准

每次循环:

1. **实现**: 按方案修改代码
2. **构建确认**: `make lint`
3. **自查**: 用 `/dev-review` 同等视角委托 Agent(general-purpose)（确认与工单文档、设计书的一致性）
4. **有指摘** → 自动修正后重新循环，**无指摘** → 结束

结束条件: 指摘 0 件 或 达到 3 次上限

### Phase 4: 审查结果报告

1. 整理自查结果（循环次数、变更摘要、审查历史）
2. **等待用户确认**: 「实现完成。是否进入测试设计？」

### Phase 5: 测试观点与用例整理

1. 用 Agent(Explore) 调查现有测试和测试设计文档
2. 整理测试观点和用例，向用户提示
3. **等待用户确认**: 「按此测试设计推进可以吗？」
4. 确认后进入 Phase 6

### Phase 6: 测试执行循环

1. 实现测试 → `go test -v ./internal/对象包/...` 执行
2. 结果判定:
   - 全部通过 → Phase 7
   - 轻微问题 → Phase 6 内修正后重新执行
   - 需要变更方案 → 向用户报告，征求是否回到 Phase 2
3. 最多修正再执行 3 次

### Phase 7: 最终结果报告

1. 整理全阶段成果（变更摘要、实现内容、测试结果、审查结果）
2. **等待用户确认**: 「是否进入 commit / push / PR 创建？」

### Phase 8: Commit / Push / PR 提交

**预先读取**: `.claude/guidelines/pre-commit-review.md` 提交前审查基准

1. 按 `pre-commit-review.md` 逐项完成全部检查，向用户提示审查结果
2. `git status` 确认变更文件
3. `git add <具体文件>`（排除自动生成文件、机密文件）
4. 按 `workflow.md` 第 6 步的格式创建 commit message
5. `git push origin <分支名>`
6. 调用 `/pr-creator` 技能创建 PR
7. 向用户报告 PR URL
8. **业务知识积累**: 将本次开发中获得的领域知识按需追加到 `.claude/business-knowledge/`

## 注意事项

- 基础分支始终为 `stage`（本项目无 `main` / `release/*` 分支策略）
- Remote 名为 `origin`
- 无需 `cd src`，Makefile 在项目根目录
