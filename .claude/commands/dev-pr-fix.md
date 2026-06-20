# PR 审查指摘对应流程

接收 PR URL 或 PR 号，从审查指摘确认到修正、测试、推送一贯执行。
各阶段在承认点向用户确认后再继续。

## 输入

`$ARGUMENTS` 传入以下任一:

- PR 号: `123`
- PR URL: `https://github.com/MosaviJP/Mosavi-Channel-Service/pull/123`
- 省略时: 自动检测当前分支关联的 PR（`gh pr view --json number`）

## 参照文档

| 文件 | 读取时机 |
|------|---------|
| `.claude/guidelines/workflow.md` | Phase 0 全流程参照（特别是第 8 步"回复 PR 指摘"） |
| `.claude/guidelines/coding.md` | Phase 3 编码规约 |
| `.claude/guidelines/pre-commit-review.md` | Phase 3 自查、Phase 8 提交前审查 |
| `.claude/guidelines/jira.md` | Phase 0 工单规范 |
| `.claude/business-knowledge/` | Phase 1 相关领域知识参照、Phase 8 知识积累 |

## 执行步骤

### Phase 0: 初始化 · PR 信息获取

1. 读取 `.claude/guidelines/workflow.md`
2. 从 `$ARGUMENTS` 提取 PR 号（数字/URL/省略时自动检测）
3. 获取 PR 信息: `gh pr view {pr_number} --json title,body,headRefName,baseRefName,state`
4. 从分支名提取工单号（`MOS-XXXX`）
5. **工单号存在时**: 用 `jira-manage-ticket` 技能获取工单信息，确认工单中的文档链接
6. **分支确认**:
   - `git branch --show-current` — 当前分支名
   - 确认当前分支与 PR 的 `headRefName` 一致（不一致时向用户确认）
   - 确认 PR 的合并目标（`baseRefName`，应为 `stage`）
   - `git log --oneline -5` — 最新提交
   - `git status` — 是否有未提交的变更

### Phase 1: 审查指摘确认（= 需求确认）

1. 获取 PR 审查评论:
   ```bash
   gh api repos/MosaviJP/Mosavi-Channel-Service/pulls/{pr_number}/comments
   ```
2. 获取 PR Review（Approve/Request Changes）:
   ```bash
   gh api repos/MosaviJP/Mosavi-Channel-Service/pulls/{pr_number}/reviews
   ```
3. 参照工单文档和 `.claude/business-knowledge/` 下相关领域知识，理解指摘内容的背景和意图
4. 对评论分类:
   - **[must]**: 必须修正（逻辑错误、安全问题、规范违反）
   - **[should]**: 建议修正（代码质量、可读性）
   - **[nits]**: 细微建议（命名、格式）
   - **质疑/确认**: 需要讨论或说明
5. 向用户提示指摘一览和对应方针（草案）
6. **等待用户确认**: 「按此对应方针推进可以吗？」

### Phase 2: 对应方针确定

1. 基于确认的方针，整理修正文件一览、实现顺序、仅回复的项目
2. **等待用户确认**: 「按此计划推进吗？」

### Phase 3: 实现 + 自查循环（最多 3 次）

**预先读取**:
- `.claude/guidelines/coding.md` — 编码规约
- `.claude/guidelines/pre-commit-review.md` — 审查基准

每次循环:

1. **实现**: 按重要度顺序修正（[must] → [should] → [nits]），记录与评论 ID 的关联
2. **构建确认**: `make lint`
3. **自查**: 用 `/dev-review` 同等视角委托 Agent(general-purpose)（确认原指摘已解消、与工单文档的一致性）
4. 有指摘 → 自动修正后重新循环，无指摘 → 结束

结束条件: 指摘 0 件 或 达到 3 次上限

### Phase 4: 实现完成报告

1. 报告变更摘要、指摘对应状况、自查结果
2. **等待用户确认**: 「是否进入测试？（不需要测试可跳到 Phase 7）」

### Phase 5: 测试观点与用例整理

1. 基于修正内容整理测试观点（确认对现有测试的影响）
2. **等待用户确认**: 「按此测试设计推进吗？」

### Phase 6: 测试执行循环

1. 实现测试 → `go test -v ./internal/对象包/...` 执行
2. 结果判定: 通过 → Phase 7，轻微问题 → 修正后重新执行，需方案变更 → 由用户判断
3. 最多修正再执行 3 次

### Phase 7: 最终结果报告 + 评论回复准备

1. 整理全部对应结果（指摘对应结果、测试结果、变更摘要）
2. 准备回复一览（参照 `workflow.md` 第 8 步的回复方式）
3. **等待用户确认**: 「按此内容执行 commit / push / 评论回复吗？」

### Phase 8: Commit / Push / 评论回复

1. 按 `pre-commit-review.md` 完成提交前审查，向用户提示审查结果
2. `git add <具体文件>` → commit（按 `workflow.md` 第 6 步的格式）
3. `git push origin <分支名>`
4. 回复审查评论（按 `workflow.md` 第 8 步的 API 方式）:
   ```bash
   gh api repos/MosaviJP/Mosavi-Channel-Service/pulls/{pr_number}/comments \
     -X POST -f body="<回复内容>" -F in_reply_to=<comment_id>
   ```
5. 向用户报告完成
6. **规则更新检讨**: 基于审查指摘内容，判断以下文件是否需要更新并向用户提案:
   - `.claude/guidelines/coding.md` — 编码规约
   - `.claude/guidelines/pre-commit-review.md` — 审查基准
   - `.claude/guidelines/workflow.md` — 工作流
7. **业务知识积累**: 将本次对应中获得的领域知识按需追加到 `.claude/business-knowledge/`

## 注意事项

- 仓库: `MosaviJP/Mosavi-Channel-Service`
- 基础分支: `stage`
- Remote 名: `origin`
