---
name: linear-to-issue
description: "读取 Linear 工单，调查代码与设计现状并分析，落地为一份正式的 GitHub Issue（带「待审核」标签），作为人类审核闸口与下游实装的唯一依据。不是搬运，而是调查+分析。触发时机: 把ENG-xxx整理成issue / 由Linear创建issue / linear转issue / linear-to-issue <标识>"
user-invokable: true
---

# Linear → GitHub Issue（调查·分析·落地）

## 概述

把一张 Linear 工单（业务 why）落成一份**代码级、可被 GitHub 原生闭环消费的正式 Issue**（what + 方向 + 验收）。
这不是文本搬运，而是「调查现状 → 分析 → 结构化落地」。产出的 Issue 是：

1. **人类唯一的需求审核闸口**（打 `待审核`，人审改后由人打 `已审核`）；
2. **下游实装的唯一依据**（实装阶段由它派生蓝图）。

本 skill 与 `jira-to-issue` 同构，仅**票源换成 Linear**（GraphQL API）。链路第 2 步（节点 1–3）。

## 输入

- Linear 工单标识（`<TEAM>-<NUM>`，如 `ENG-12`、`ENG-289`）。无则向用户索取。

## 进度上报（结构化事件 → 控制面）

阶段边界调用 `emit-event.sh` 回传控制面（phase 值与 jira-to-issue **一致**，因流水线节点按 phaseKey 映射，与源无关）：

```bash
bash .claude/ai-workflow/emit-event.sh B.read start "读取 Linear 工单"
bash .claude/ai-workflow/emit-event.sh B.read ok    "Linear 读取完成"
bash .claude/ai-workflow/emit-event.sh B.investigate start "并行调查"
bash .claude/ai-workflow/emit-event.sh B.investigate ok    "调查完成"
bash .claude/ai-workflow/emit-event.sh B.issue start "分析并落地 Issue"
# 完成时带 Issue 编号/URL：
WF_ISSUE_NUM=<N> WF_ISSUE_URL=<url> bash .claude/ai-workflow/emit-event.sh B.issue ok "Issue #<N> 已建（待审核）"
```

- **正常判定无需处理**：`emit-event.sh B.issue skip "无需处理：<原因>"`（任务转「已跳过」，非失败）。
- **异常遇阻**（认证失败 / API 出错 / 无法判定）：`emit-event.sh B.<当前phase> fail "<原因>"`（任务转「待裁决」）。
- 区分原则：**该不该做**的否定结论 = `skip`；**做的过程坏了** = `fail`。

## 工作流程

### Step 0：确定工单标识

从 `$ARGUMENTS` 提取 `<TEAM>-<NUM>`（如 `ENG-289`）；缺失则向用户索取。拆出 `TEAM=ENG`、`NUM=289` 备查询用。

### Step 1：读取 Linear 工单

Linear 无既有脚本，直接调 GraphQL（API Key 从注入的 `.claude/ai-workflow/.env` 的 `LINEAR_API_KEY` 读）：

```bash
set -a; . .claude/ai-workflow/.env; set +a
curl -s -X POST https://api.linear.app/graphql \
  -H "Authorization: $LINEAR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"query":"{ issues(first:1, filter:{ team:{key:{eq:\"'"$TEAM"'\"}}, number:{eq:'"$NUM"'} }) { nodes { identifier title description url state{name} assignee{displayName} labels{nodes{name}} comments{nodes{body createdAt user{displayName}}} } } }"}'
```

- `description` 是 **Markdown 文本**（不同于 JIRA 的 ADF），可直接阅读。
- 把工单描述内的**所有链接文档**（设计书 / 外部契约等）尽量读取，作为调查输入。
- **评论/回复必读**：`comments.nodes`（`user.displayName` / `createdAt` / `body`，body 为 Markdown）常含澄清、追加需求、方案决策与**上游文档 PR 链接**（含未加超链接的裸 URL，都要抓），遗漏会导致 Issue 分析失真。
- 记录：`identifier`、`title`、`state.name`、`labels`、`assignee`、`comments`。

### Step 2：并行调查现状（Agent Explore，多路并发）

先读目标仓 `CLAUDE.md` / `.claude/guidelines/` 了解目录与约定，再据此并行调查、每条结论附 `file:line` 证据。**不预设具体目录名**：

- 相关现有源码与现状行为；
- 设计基线 / 详设文档（若有）；
- 既有相关变更设计（若有）；
- 相关 API / 契约 / 真相源（按本仓 `CLAUDE.md` 列出的优先级）。

**真相源读取方式（务必取最新且取对版本）**：真相源多为 GitHub 仓（如 example-group-service、example-docs）。**不得依赖本地陈旧克隆或模型记忆**，按链接类型取：
- **PR 链接**（`.../pull/N`）：`gh pr view N -R <owner>/<repo> --json state,headRefName`；未合并（OPEN）读其 head 分支（`gh api repos/<owner>/<repo>/contents/<路径>?ref=<headRefName>`）或 `gh pr diff N`，**别读默认分支**；已合并读默认分支最新。
- **带 ref 的 blob/tree 链接**：按该 ref 读。
- **纯仓/目录链接**：读默认分支最新（`gh api .../contents/<路径>?ref=<默认分支>`）；若用本地克隆先 `git fetch origin` 再读 `origin/<默认分支>`。

### Step 3：分析（Agent Plan）

综合 Linear 需求 + 链接文档 + 调查结论，产出：需求拆解、现状调查结论（带 `file:line`）、影响范围、方案概要（不含逐行代码）、**方案分歧点（多方案时必列，留人拍板）**、风险与未决问题、验收标准 / 测试观点。

粒度：Issue 写「概要 / 方向」，够人类判断「该做吗 / 方向对吗」即可；逐文件详设留到实装阶段。

### Step 4：落地 GitHub Issue（upsert：新建 或 重建+重置）

Linear 票可能被多次触发。**先查重，再决定新建还是重建**：
```bash
gh issue list --search "[<TEAM>-<NUM>] in:title" --state all --json number,state,title
```

**情况 A — 不存在同名 Issue**：新建，打 `待审核`：
```bash
gh issue create --title "[<TEAM>-<NUM>] <简明需求标题>" --label "待审核" --body-file <临时正文文件>
```

**情况 B — 已存在同名 Issue #N**：视为「重新发起」，用最新调查**重建 + 标签重置**：
```bash
gh issue reopen <N> 2>/dev/null
gh issue edit <N> --body-file <临时正文文件>
gh issue edit <N> --add-label "待审核" --remove-label "已审核" --remove-label "待裁决" --remove-label "已实装"
```

- 标签始终归位到 `待审核`。
- 完成后报告 Issue URL（注明新建/重建）。

## Issue 正文模板

```markdown
# [<TEAM>-<NUM>] <简明需求标题>

## 来源
- Linear: <工单 url>
- 状态: <state.name>
- 原始摘要: <title 一句话>

## 背景与目标
为什么做、要达成什么（来自 Linear + 链接文档）。

## 需求拆解
- [ ] 功能点 1 …

## 现状调查结论
- `internal/.../xxx.go:NN` 现状：…

## 影响范围
- 模块 / API / 文件清单：…

## 方案概要
实现方向（不含逐行代码）。

## 方案分歧点（需人类拍板——审核时勾选选定项）
- [ ] 方案 A：… ｜ 优 … ｜ 劣 …
- [ ] 方案 B：… ｜ 优 … ｜ 劣 …
（多方案**必须**用 `- [ ]` 复选框列出，供人审直接勾选；无分歧则写「无，方向单一」）

## 风险与未决问题
- …

## 验收标准 / 测试观点
- …

---
> 本 Issue 由 linear-to-issue 自动整理，待人类审核。审核通过请将 `待审核` 换为 `已审核`。
```

## 注意事项

- **不臆造**：调查不足时在「未决问题」显式标注，不编造。
- **附证据**：现状结论尽量带 `file:line`。
- **项目无关**：按目标仓 `CLAUDE.md`/guidelines 适配，不预设结构/分支。
- **输出语言**：全程中文。Issue 正文 / 报告均不加 AI 工具署名。
