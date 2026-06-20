# ai-workflow

> JIRA 工单 **事件驱动**自动跑成 GitHub Issue → 实装 → PR 的 AI 协作工具集（Claude Code 编排栈）。
> 本仓 = 该工具集的**唯一真相源**；各产品仓通过 `install-into.sh` 接入，自身只留项目专属配置。

```
JIRA 票打「AI处理」标签 ─webhook→ 接收器 → 跑 jira-to-issue → GitHub Issue + 待审核 → Slack
人审 Issue 打「已审核」  ─webhook→ 接收器 → 跑 issue-to-pr  → PR(Closes #N) + 已实装 → Slack
人 review PR → 合并
```

## 构成

| 单元 | 位置 | 职责 |
|---|---|---|
| 引擎 | [`.claude/ai-workflow/`](./.claude/ai-workflow/) | JIRA 客户端 / Slack 通知 / webhook 接收器 / 安装·配置脚本 / 设计文档 |
| 入口 skills | [`.claude/skills/`](./.claude/skills/) | `jira-to-issue` `pr-creator` `pr-reviewer` `jira-manage-ticket` `gin-api-docs` |
| 编排命令 | [`.claude/commands/`](./.claude/commands/) | `dev-flow` `dev-review` `dev-test*` `dev-pr-*` `issue-to-pr` |
| 规约 | [`.claude/guidelines/`](./.claude/guidelines/) | <org> 编码 / 分支 / JIRA / 提交前自审 / 测试规格（各产品仓可裁剪） |
| 钩子·配置 | [`.claude/hooks/`](./.claude/hooks/) · [`.claude/settings.json`](./.claude/settings.json) | 提交前审查钩子 / 权限与 hook 注册 |

## 用法

- **在常开设备上跑这套工具**（接收 webhook、起接收器）：见引擎 README 的「安装 A」。
- **给一个新产品仓加 AI 能力**：在本仓执行 `./.claude/ai-workflow/install-into.sh /path/to/目标仓`，把全套件复制进去（不带 `.env` 密钥），再到目标仓 `setup.sh`。见「安装 B」。

完整手册、安全模型、信号原则、运维：[`.claude/ai-workflow/README.md`](./.claude/ai-workflow/README.md) 及其 `docs/`。

## 边界

- **内容是 <org> 风味**（`jira_api`、`PROJ-` 工单、<org>-docs 链接、<org> 规约）。沿用于其他 <org> 仓即可；用于非 <org> 项目需替换 `guidelines/` 与 JIRA 配置。
- 不含任何密钥：`.env` / `config.json` 均 gitignore，按机器/按仓现配。
