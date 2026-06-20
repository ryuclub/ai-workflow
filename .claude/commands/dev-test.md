# 测试设计与执行命令

对变更对象进行测试观点整理、用例设计、测试实现与执行。
指定工单号时作为上下文参照。

## 输入

`$ARGUMENTS` 可传入工单号（例: `MOS-1234`）。省略时从当前分支名推定。

## 参照文档

| 文件 | 读取时机 |
|------|---------|
| `.claude/guidelines/coding.md` | Step 4 编码规约 |
| `.claude/guidelines/pre-commit-review.md` | Step 4 审查基准中的测试相关项 |
| `.claude/business-knowledge/` | Step 1 相关领域知识参照、Step 2 测试观点参照 |

## 执行步骤

### Step 1: 上下文收集

1. 获取工单号（`$ARGUMENTS` 或从分支名推定）
2. **分支确认**:
   - `git branch --show-current` — 当前分支名
   - `git log --oneline -5` — 最新提交
   - 确认基础分支为 `stage`
   - `git status` — 是否有未提交的变更
3. **工单号存在时**: 用 `jira-manage-ticket` 技能获取工单信息，确认工单中的文档（设计书、测试规格等）
4. 确认变更对象:
   - `git diff stage...HEAD --name-only` — 变更文件一览
   - 确定对象包
5. 现有测试调查（用 Agent(Explore) 并行执行）:
   - 变更文件对应的现有测试文件
   - 同包的其他测试模式
   - `_test.go` 文件的有无

### Step 2: 测试观点整理

基于工单文档、设计书，分析变更内容，向用户提示测试观点（正常系/异常系/边界值/错误处理）。

**等待用户确认**: 「按此测试观点推进可以吗？」

### Step 3: 测试用例设计

基于确认的测试观点，向用户提示具体测试用例一览。

**等待用户确认**: 「按此测试用例进入实现吗？」

### Step 4: 测试实现

1. 读取 `.claude/guidelines/coding.md`
2. 创建/修改测试文件
3. 按 `pre-commit-review.md` 的测试相关检查项确认

### Step 5: 测试执行

1. `make lint`
2. `go test -v ./internal/对象包/...`
3. 结果判定:
   - 全部通过 → Step 6
   - 轻微问题 → Step 5 内修正后重新执行（最多 3 次）
   - 需方案变更 → 向用户报告，征求判断

### Step 6: 结果报告

向用户报告测试执行结果（用例数、成功/失败、执行时间）和修正历史。

## 注意事项

- 不直接编辑自动生成文件
- 基础分支为 `stage`
