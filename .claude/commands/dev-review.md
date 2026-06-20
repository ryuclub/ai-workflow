# 自查命令

对当前分支的变更执行自查。
指定工单号时作为上下文参照。

## 输入

`$ARGUMENTS` 可传入工单号（例: `MOS-1234`）。省略时从当前分支名推定。

## 参照文档

| 文件 | 读取时机 |
|------|---------|
| `.claude/guidelines/coding.md` | Step 2 编码规约检查 |
| `.claude/guidelines/pre-commit-review.md` | Step 2 审查基准检查 |
| `.claude/business-knowledge/` | Step 2 相关领域知识参照 |

## 执行步骤

### Step 1: 上下文收集

1. 获取工单号:
   - `$ARGUMENTS` 指定时 → 直接使用
   - 省略时 → 从 `git branch --show-current` 提取工单号
2. **分支确认**:
   - `git branch --show-current` — 当前分支名
   - `git log --oneline -5` — 最新提交
   - 确认基础分支为 `stage`
   - `git status` — 是否有未提交的变更
3. 获取当前变更:
   - `git diff stage...HEAD` — 从 stage 分支起的全部变更
   - `git diff` — 未暂存的变更
   - `git diff --cached` — 已暂存的变更
   - `git status` — 变更文件一览

### Step 2: 执行审查（Agent 委托）

1. 读取 `.claude/guidelines/coding.md`（编码规约）
2. 读取 `.claude/guidelines/pre-commit-review.md`（审查基准）
3. **工单号存在时**: 用 `jira-manage-ticket` 技能获取工单信息，确认工单中的文档链接
4. 用 Agent(general-purpose) 执行:
   - 确认与工单文档、设计书的一致性
   - 按 `pre-commit-review.md` 的检查项逐项检查
   - 按 `coding.md` 的编码规约检查
   - 各指摘注明「依据」（基于哪条规则）

### Step 3: 指摘整理

| # | 严重度 | 文件 | 行 | 指摘内容 | 修正方案 | 依据 |
|---|--------|------|-----|---------|----------|------|

摘要: [must] X 件 / [should] X 件 / [nits] X 件

### Step 4: 用户确认

- 有指摘 → 「要自动修正吗？（可逐条跳过）」
- 无指摘 → 「审查通过，无指摘。」

### Step 5: 修正 + 重新审查循环（最多 3 次）

1. 基于指摘修正代码
2. `make lint`
3. `go test -v ./internal/对象包/...`
4. 重新执行 Step 2 的审查
5. 指摘 0 件 或 达到 3 次上限时结束

### Step 6: 最终报告

报告循环次数、最终结果、修正历史、lint/测试结果。

## 注意事项

- 自动生成文件不在审查范围内
- 禁止推测: 需要判断时向用户确认
