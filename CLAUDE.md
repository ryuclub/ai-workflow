# CLAUDE.md

> 本仓是 AI 协作工具集本体（被各产品仓 `install-into` 接入）。本文件只做**导航 + 行为合同**。

## 是什么 / 怎么用

- 总览与接入：[`README.md`](./README.md)
- 引擎手册 / 安全模型 / 运维：[`.claude/ai-workflow/README.md`](./.claude/ai-workflow/README.md)
- 工作流设计：[`.claude/ai-workflow/docs/`](./.claude/ai-workflow/docs/)

## 怎么写代码

- 编码 / 分支 / commit / JIRA / 提交前自审 / 测试规格：[`.claude/guidelines/`](./.claude/guidelines/)

## AI 行为合同（硬约束）

- ✅ 全程**中文**输出（代码注释 / commit / PR / JIRA 评论）
- ❌ 不署名（commit 不加 `Co-Authored-By`；PR/Review/JIRA 不提 AI 工具名）
- ❌ 不擅自 `push` / `merge` / 远程操作——须用户明确同意
- ❌ 提交任何密钥（`.env` / `config.json` 已 gitignore，改动 `*.example` 而非真值）

## 维护提醒

- 这里是工具集**真相源**。改了 skill/command/引擎后，已接入的产品仓需重新 `install-into` 或同步对应文件。
- 信号原则：状态只看 GitHub 标签、通知只走 Slack、Issue 标题+主贴=唯一真相、产物是 PR（`Closes #N`）。
