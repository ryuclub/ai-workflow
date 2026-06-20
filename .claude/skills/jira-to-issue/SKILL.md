---
name: jira-to-issue
description: "读取工单（JIRA 或 Linear），调查代码与设计现状并分析，落地为一份正式的 GitHub Issue（带「待审核」标签），作为人类审核闸口与下游实装的唯一依据。不是搬运，而是调查+分析。触发时机: 把PROJ-xxxx/ENG-xxxx整理成issue / 由JIRA或Linear创建issue / jira转issue / linear转issue / 起一个待审核issue"
user-invokable: true
---

# 工单（JIRA / Linear）→ GitHub Issue（调查·分析·落地）

## 概述

把一张工单（JIRA 或 Linear，业务 why）落成一份**代码级、可被 GitHub 原生闭环消费的正式 Issue**（what + 方向 + 验收）。
> 工单源由工单号前缀自动判定：`PROJ-` 等走 JIRA；其余形如 `TEAM-数字`（如 `ENG-123`）走 Linear。下文以 JIRA 为主线，Linear 差异在各步显式标注。
这不是文本搬运，而是「调查现状 → 分析 → 结构化落地」。产出的 Issue 是：

1. **人类唯一的需求审核闸口**（打 `待审核`，人审改后由人打 `已审核`）；
2. **下游实装的唯一依据**（实装阶段由它派生 `design/changes/PROJ-XXXX.md` 蓝图）。

整体工作流见 [`.claude/ai-workflow/docs/ai-workflow.md`](../../ai-workflow/docs/ai-workflow.md)。本 skill 只负责链路第 2 步。

## 输入

- 工单号：JIRA `PROJ-XXXX` 或 Linear `TEAM-XXXX`（如 `ENG-123`）。无则向用户索取。

## 真相源优先级（调查时遵循，冲突时由高到低）

参见根目录 `CLAUDE.md` 第 3 节：JIRA 工单 > <org>-Group-Service > <org>-docs 技术文档 > 产品文档 > 本仓 `design/` > 现状代码（不预设正确）。

## 进度通知（临时，后续移除）

为便于观察自动流程，每个步骤起止用 Slack 播报：
```bash
bash .claude/ai-workflow/notify.sh "<消息>"
```
播报点（B 全程）：
- 开始：`🔵 [B] PROJ-XXXX 开始：读取 JIRA`
- 调查完成：`🔍 [B] PROJ-XXXX 调查完成，开始分析`
- Issue 建好：`✅ [B] PROJ-XXXX → Issue #N（待审核）<url>`
- 失败：`⚠️ [B] PROJ-XXXX 失败：<原因>`

## 工作流程

### Step 0：确定工单号与工单源

从 `$ARGUMENTS` 提取工单号；缺失则向用户索取。按前缀判定工单源：

- `PROJ-XXXX`（及其它 JIRA 项目 key）→ **JIRA**，用 `jira_api.py`。
- 其余形如 `TEAM-数字`（如 `ENG-123`）→ **Linear**，用 `linear_api.py`。

### Step 1：读取工单

**JIRA：**
```bash
python3 .claude/ai-workflow/jira_api.py get <PROJ-XXXX>
```
- `description` 是 **ADF（Atlassian Document Format）JSON**，不是纯文本。需自行解析：
  - 正文 = 递归提取各节点的 `text`；
  - **链接** = ADF 节点 `marks` 中 `type: "link"` 的 `attrs.href`（设计书 / Confluence / 图等）。

**Linear：**
```bash
python3 .claude/ai-workflow/linear_api.py get <TEAM-XXXX>
```
- `description` 是 **Markdown 纯文本**，直接可用；链接为标准 Markdown `[text](url)`，无需 ADF 解析。
- 返回另带 `url`（工单页地址），用于 Issue「来源」。

**两者通用：**
- 把工单内所有链接文档**全部读取**（设计书、Confluence、外部 API 契约等），作为调查输入。
- 记录：`summary`、`status`、`issuetype`、`labels`、`parent`、`subtasks`。两个客户端 `get` 输出字段同构。

### Step 2：并行调查现状（Agent Explore，多路并发）

先读目标仓的 `CLAUDE.md` / `.claude/guidelines/` 了解其目录与约定，再据此并行调查、每条结论附 `file:line` 证据。**不预设具体目录名**，按本仓实际结构找：

- 相关现有源码与现状行为（本仓的源码目录，如 `internal/`、`src/` 等）；
- 设计基线 / 详设文档（如 `design/base/`，若有）；
- 既有相关变更设计（如 `design/changes/`，若有）；
- 领域知识（如 `.claude/business-knowledge/`，若有）；
- 相关 API / 契约 / 真相源（按本仓 `CLAUDE.md` 列出的真相源优先级）。

### Step 3：分析（Agent Plan）

综合 JIRA 需求 + 链接文档 + 调查结论，产出：

- **需求拆解**：功能点清单；
- **现状调查结论**：相关代码现状（带 `file:line`）、现有行为；
- **影响范围**：受影响模块 / API / 文件清单；
- **方案概要**：实现方向（**不含逐行代码**）；
- **方案分歧点**：当存在多个可行方案时，**必须**列出并标注权衡，留给人类在审核时拍板（这是闸口的关键——不要替人单方面选方案）；
- **风险与未决问题**；
- **验收标准 / 测试观点**。

粒度把握：Issue 写「概要 / 方向」，够人类判断「该做吗 / 方向对吗」即可；逐文件详设留到实装阶段的 `design/changes/PROJ-XXXX.md`。

### Step 4：落地 GitHub Issue（upsert：新建 或 重建+重置）

JIRA 票可能被多次触发（票内容更新后重发）。**先查重，再决定新建还是重建**：
```bash
gh issue list --search "[PROJ-XXXX] in:title" --state all --json number,state,title
```

**情况 A — 不存在同名 Issue**：新建，打 `待审核`：
```bash
gh issue create \
  --title "[PROJ-XXXX] <简明需求标题>" \
  --label "待审核" \
  --body-file <临时正文文件>
```

**情况 B — 已存在同名 Issue #N（无论开放/关闭）**：视为「重新发起」，**用最新调查重建,而非跳过**：
```bash
gh issue reopen <N> 2>/dev/null   # 若已关闭则重开
gh issue edit <N> --body-file <临时正文文件>          # 覆盖正文为最新分析
gh issue edit <N> --add-label "待审核" \
  --remove-label "已审核" --remove-label "待裁决" --remove-label "已实装"   # 标签重置回待审核
```
即：**内容重建 + 标签重置**，丢弃旧的已审核/待裁决/已实装状态，重新过人审闸口。

- 标签始终归位到 `待审核`；类型标签（`enhancement`/`bug`）可保留或一并加。
- 完成后向用户报告 Issue URL（注明是新建还是重建）。

### Step 5：回写工单关联（可选）

- **JIRA**：`jira_api.py` 目前**无评论 API**（仅字段更新 / 状态转换）。默认**跳过**，仅在报告里给出 Issue URL 供人工回填。
- **Linear**：`linear_api.py comment <TEAM-XXXX> "<正文>"` 可写评论。如需 Linear↔Issue 双向关联，可回写 Issue URL；当前默认仍**跳过**（保持与 JIRA 一致的最小副作用），仅在报告里给出 URL。

## Issue 正文模板

```markdown
# [PROJ-XXXX] <简明需求标题>

## 来源
- 工单: <JIRA: https://<domain>/browse/PROJ-XXXX ｜ Linear: get 返回的 url>
- 类型 / 状态: <issuetype> / <status>
- 原始摘要: <summary 一句话>

## 背景与目标
为什么做、要达成什么（来自 JIRA + 链接文档）。

## 需求拆解
- [ ] 功能点 1 …
- [ ] 功能点 2 …

## 现状调查结论
- `internal/.../xxx.go:NN` 现状：…
- 相关 API / 行为：…

## 影响范围
- 模块 / API / 文件清单：…

## 方案概要
实现方向（不含逐行代码）。

## 方案分歧点（需人类拍板）
- 方案 A：… ｜ 优 … ｜ 劣 …
- 方案 B：… ｜ 优 … ｜ 劣 …
（无分歧则写「无，方向单一」）

## 风险与未决问题
- …

## 验收标准 / 测试观点
- …

---
> 本 Issue 由 jira-to-issue 自动整理，待人类审核。审核通过请将 `待审核` 换为 `已审核`。
```

## 注意事项

- **不臆造**：调查不足时在「未决问题」里显式标注，不要编造结论填满模板。
- **附证据**：现状结论尽量带 `file:line`，这是 Issue 作为「代码级契约」的价值所在。
- **项目无关**：不预设目标仓结构/分支；按其 `CLAUDE.md`/guidelines 适配（下游实装时 C 再据 branch.md/默认分支定 base）。
- **输出语言**：全程中文。Issue 正文 / 报告均不加 AI 工具署名。
