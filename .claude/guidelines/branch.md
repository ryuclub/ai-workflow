# 分支管理规则

## 基本规则

- 以 `stage` 分支为基点创建新分支
- 完成开发后，向 `stage` 分支提交 Pull Request

## 创建分支的正确方式

```bash
# 先拉取最新 stage
git fetch origin stage
# 从 origin/stage 创建新分支，但不设 upstream（不加 --track）
git checkout -b <分支名> --no-track origin/stage
# 首次推送时设置 upstream 为远端同名分支
git push -u origin <分支名>
```

**禁止事项**:
- 使用 `git checkout -b <分支名> origin/stage`（不带 `--no-track`）— 会自动将 `origin/stage` 设为 upstream，导致 push 行为异常
- 从本地 `stage` 分支创建新分支 — 本地分支可能未同步远端最新代码，必须始终以 `origin/stage` 为基准

## 分支命名规范

格式：`<变更类型>/<JIRA工单号>-<描述>`

| 变更类型   | 用途               |
|------------|--------------------|
| `feat`     | 新功能开发         |
| `fix`      | Bug 修复           |
| `hotfix`   | 紧急 Bug 修复      |
| `perf`     | 性能优化           |
| `refactor` | 代码重构           |
| `chore`    | 构建/配置变更      |
| `docs`     | 文档变更           |

## 示例

```
feat/MOS-1234-add-user-auth
fix/MOS-2345-fix-login-bug
perf/MOS-2569-optimize-log-output
```
