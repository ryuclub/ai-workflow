# AI 工作流流水线 · AI 指南

本文件由 AI 助手自动读取，作为项目级别的配置入口。
开始工作前请参考仓根 [`CLAUDE.md`](../CLAUDE.md) 与以下规则文档。

## AI 行为规则

- **输出语言**：始终使用中文输出（包括 JIRA 评论、commit message、PR 内容、Code Review 评论）
- **JIRA 评论格式**：使用 JIRA Wiki 标记，禁止使用 Markdown（详细格式参照 [jira.md](.claude/guidelines/jira.md)）
- **commit message**：不添加 `Co-Authored-By` 等 AI 相关署名
- **PR 内容**：不添加 AI 工具相关说明或链接
- **Code Review 评论**：不添加「Generated with Claude Code」等 AI 工具相关署名或链接

## 规则文档

- [开发工作流](.claude/guidelines/workflow.md)
- [分支管理规则](.claude/guidelines/branch.md)
- [编码规约](.claude/guidelines/coding.md)
- [提交前审查基准](.claude/guidelines/pre-commit-review.md)
- [JIRA 工单规范](.claude/guidelines/jira.md)
