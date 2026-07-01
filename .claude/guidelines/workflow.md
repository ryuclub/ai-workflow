# 开发工作流

## 完整流程

### 1. 确认/创建 JIRA 工单

- 已有工单：从 JIRA 读取工单内容，确认需求范围
- 需要新建：按照 [jira.md](jira.md) 的规范创建工单（类型、必填字段、担当）

### 2. 创建设计文档

在 `design/changes/<JIRA工单号>.md` 记录背景、分析与修改方案。
**方案确认后再开始实现。**

### 3. 创建分支

```bash
git fetch origin stage
git checkout -b <变更类型>/PROJ-XXXX-<描述> --no-track origin/stage
```

**必须以远端 `origin/stage` 为基准**，禁止从本地 `stage` 创建分支。
详细规则参考 [branch.md](branch.md)。

### 4. 实现

编码规约参考 [coding.md](coding.md)。

### 5. 提交前审查

执行 commit 前：

1. **pre-commit hook 自动检查**：`make auto-fix`（自动修复）→ `make fmt-check` → `make vet` → `make lint-strict`（hook 失败时禁止提交）
2. **审查基准文档检查**：按照[提交前审查基准](pre-commit-review.md)完成全部检查项，审查结果需提示给用户，获得用户确认后方可提交

### 6. 提交

```bash
git add <具体文件>
git commit -m "<变更类型>(PROJ-XXXX): <简述>

- 变更点1
- 变更点2"
```

### 7. 推送 & 创建 PR

```bash
git push -u origin <分支名>
```

PR 的 base 分支为 `stage`。

### 8. 回复 PR 指摘

PR 创建后，收到 Code Review 评论（Copilot、reviewer 等）时：

1. 评估每条指摘，确认是否需要修正
2. 修正后推送新 commit
3. **必须对每条指摘逐一回复**，说明修正内容或不修正的理由

```bash
# 回复 PR 指摘（inline comment）— 使用 in_reply_to 参数
curl -s -X POST \
  -H "Authorization: token $GITHUB_TOKEN" \
  -H "Content-Type: application/json" \
  -H "Accept: application/vnd.github.v3+json" \
  "https://api.github.com/repos/your-org/example-gateway/pulls/<PR号>/comments" \
  -d '{"body":"<回复内容>","in_reply_to":<comment_id>}'
```

### 9. 事后整理

每次完成开发（包含 PR 指摘对应）后，检查以下内容：

- **guidelines 更新**：本次遇到的规则、注意点，是否需要补充到 `.claude/guidelines/` 中（编码规约、工作流、提交审查基准等）
- **skill 新增**：本次操作中是否出现了可复用的步骤，考虑封装为新的 skill（`.claude/skills/<name>/SKILL.md`）
