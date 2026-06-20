# PR 审查命令

接收 PR 号或 PR URL，结合 JIRA 工单进行代码审查。
在 `/pr-reviewer` 技能的视角基础上，追加工单文档一致性检查。

## 输入

`$ARGUMENTS` 传入以下任一:

- PR 号: `123`
- PR URL: `https://github.com/MosaviJP/Mosavi-Channel-Service/pull/123`
- 分支名: `feat/MOS-1234-add-user-auth`
- 省略时: 审查当前分支的本地变更

## 参照文档

| 文件 | 读取时机 |
|------|---------|
| `.claude/guidelines/coding.md` | Step 3 编码规约检查 |
| `.claude/guidelines/pre-commit-review.md` | Step 3 审查基准检查 |
| `.claude/guidelines/jira.md` | Step 1 工单规范确认 |
| `.claude/business-knowledge/` | Step 2 相关领域知识参照 |

## 执行步骤

### Step 1: 确定审查对象

1. 从 `$ARGUMENTS` 判定审查对象:
   - PR 号/URL → `gh pr view {pr_number} --json title,body,headRefName,baseRefName,state` 获取信息
   - 分支名 → 该分支的 diff 为对象
   - 省略时 → `git diff stage...HEAD` 的本地变更为对象
2. **分支 · 合并目标确认**:
   - PR 时: 确认 `headRefName`（源）、`baseRefName`（合并目标，应为 `stage`）
   - 分支时: 确认基础分支为 `stage`
3. 从 PR 或分支名提取工单号（`MOS-XXXX`）
4. **工单号存在时**: 用 `jira-manage-ticket` 技能获取工单信息，确认工单中的文档链接
5. 获取 diff:
   - PR 指定时: `gh pr diff {pr_number}`
   - 分支/本地: `git diff stage...HEAD`
   - 大规模 diff 时: 按 service 层 > controller 层 > infra 层 > model 层 的优先级确认

### Step 2: 上下文收集

1. 用 Agent(Explore) 并行调查:
   - 变更对象的现有代码和相关文件
   - 设计文档（`design/changes/` 下）
   - 工单中记载/链接的文档
   - `.claude/business-knowledge/` 下相关领域的业务知识

### Step 3: 执行审查

**预先读取**:
- `.claude/guidelines/coding.md` — 编码规约
- `.claude/guidelines/pre-commit-review.md` — 审查基准

用 Agent(general-purpose) 从以下视角审查:

1. **JIRA 一致性**: 与工单目的、成果物、完成条件的一致性
2. **一致性**: 命名规则、错误码、错误处理、日志、Swagger 注释
3. **代码质量**: 架构遵循、错误处理、DI、自动生成文件未编辑
4. **Lint 验证**: `make lint`
5. **通用质量**: 安全性、性能、测试覆盖
6. **破坏性变更**: 函数签名变更的影响范围
7. 各指摘注明「依据」（基于哪条规则/规约）

### Step 4: 审查结果输出

| # | 严重度 | 文件 | 行 | 指摘内容 | 修正方案 | 依据 |
|---|--------|------|-----|---------|----------|------|

严重度: CRITICAL / HIGH / MEDIUM / LOW / INFO

摘要: CRITICAL X 件 / HIGH X 件 / MEDIUM X 件 / LOW X 件 / INFO X 件

### Step 5: 用户确认

- 有指摘 → 「要在 PR 上发评论吗？（可逐条选择）」
- 无指摘 → 「审查通过，无指摘。」
- 在 PR 上发评论时，按以下规则投稿:

#### 投稿方法

**インラインコメント（diff 範囲内の行に対する指摘）**:
- `gh api` の `--input` で JSON ファイルを渡す（`--field` は複雑な JSON 配列を正しく処理できないため使用禁止）
- `line` には diff に含まれる行番号のみ指定可能。diff 範囲外の行を指定すると `"Line could not be resolved"` エラーになる

```bash
cat <<'PAYLOAD' > /tmp/pr_review.json
{
  "event": "COMMENT",
  "body": "",
  "comments": [
    {
      "path": "file/path.go",
      "line": 42,
      "side": "RIGHT",
      "body": "コメント内容"
    }
  ]
}
PAYLOAD
gh api repos/{owner}/{repo}/pulls/{pr_number}/reviews --input /tmp/pr_review.json
```

**通常コメント（diff 範囲外の指摘・ファイル全体・ブランチ名等）**:
- diff に含まれない行への指摘や、コード以外の指摘は `gh pr comment` で投稿

```bash
gh pr comment {pr_number} --body "コメント内容"
```

## 注意事项

- 自动生成文件不在审查范围内
- 禁止推测: 需要业务判断时向用户确认
- 同时参照 `/pr-reviewer` 技能的视角
