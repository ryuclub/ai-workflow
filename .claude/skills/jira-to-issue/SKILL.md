---
name: jira-to-issue
description: "读取 JIRA 工单，调查代码与设计现状并分析，落地为一份正式的 GitHub Issue（带「待审核」标签），作为人类审核闸口与下游实装的唯一依据。不是搬运，而是调查+分析。触发时机: 把PROJ-xxxx整理成issue / 由JIRA创建issue / jira转issue / 起一个待审核issue"
user-invokable: true
---

# JIRA → GitHub Issue（调查·分析·落地）

## 概述

把一张 JIRA 工单（业务 why）落成一份**代码级、可被 GitHub 原生闭环消费的正式 Issue**（what + 方向 + 验收）。
这不是文本搬运，而是「调查现状 → 分析 → 结构化落地」。产出的 Issue 是：

1. **人类唯一的需求审核闸口**（打 `待审核`，人审改后由人打 `已审核`）；
2. **下游实装的唯一依据**（实装阶段由它派生 `design/changes/PROJ-XXXX.md` 蓝图）。

整体工作流见仓根 [`docs/ai-workflow-rebuild-plan.md`](../../../docs/ai-workflow-rebuild-plan.md)。本 skill 只负责链路第 2 步（节点 1–3）。

## 输入

- JIRA 工单号（`PROJ-XXXX`）。无则向用户索取。

## 真相源优先级（调查时遵循，冲突时由高到低）

参见根目录 `CLAUDE.md` 第 3 节：JIRA 工单 > example-group-service > example-docs 技术文档 > 产品文档 > 本仓 `design/` > 现状代码（不预设正确）。

**读取方式（务必取最新且取对版本，防旧版本 / 漏未合并变更）**：真相源多为 GitHub 仓（example-group-service、example-docs）。**不得依赖本地陈旧克隆或模型记忆**。工单描述/评论里的真相源链接按类型分别取：
- **PR 链接**（如评论给出 `github.com/your-org/example-docs/pull/N`）：文档可能尚未合并进默认分支，**必须按 PR 实际状态取版本**：
  ```bash
  gh pr view N -R your-org/example-docs --json state,headRefName,files
  ```
  - 未合并（OPEN）：读该 PR **head 分支**的文档（`gh api repos/your-org/example-docs/contents/<路径>?ref=<headRefName> --jq .content | base64 -d`）或 `gh pr diff N` 看改动；**不要读默认分支**（那里还没有）。
  - 已合并（MERGED）：文档已进默认分支，读默认分支最新即可。
  - **多个 PR 参考**（常跨多条评论、已合并 + 未合并混合）：**逐一全部读取**，并按评论时间理解演进——**较晚的未合并 PR 往往是最新方向，可能覆盖较早已合并的版本**，切勿只取其一。（本例 PROJ-3377：回复1→#100 已合并；更晚的回复2→#101 未合并且是对前者的修订，须以 #101 head 分支为准。）
- **带 ref 的 blob/tree 链接**（`/blob/<ref>/...`）：按该 `ref` 读，不擅自换成默认分支。
- **纯仓/目录链接**：读默认分支最新 —— 列文件 `gh api repos/your-org/<repo>/git/trees/<默认分支>?recursive=1 --jq '.tree[].path'`；读文件 `gh api repos/your-org/<repo>/contents/<路径>?ref=<默认分支> --jq .content | base64 -d`。
- 若确要用本地克隆：先 `git -C <克隆> fetch origin` 再读 `origin/<默认分支>`，**绝不读可能落后的本地分支/工作树**。

## 进度上报（结构化事件 → 控制面）

每个阶段边界调用 `emit-event.sh` 把进度回传控制面后端（后端据此点亮流水线节点、经 SSE 推 Dashboard、经 sink 转 Slack）。**非编排环境（人手直接跑）下该脚本静默跳过，不影响使用。**

```bash
bash .claude/ai-workflow/emit-event.sh <phase> <status> "<消息>"
```

约定：**每个节点成对上报 `start` → `ok`**（phase 须用下列值）：
- 读 JIRA：`emit-event.sh B.read start "读取 JIRA"` → `emit-event.sh B.read ok "JIRA 读取完成"`
- 调查现状：`emit-event.sh B.investigate start "并行调查"` → `emit-event.sh B.investigate ok "调查完成"`
- 分析建 Issue：`emit-event.sh B.issue start "分析并落地 Issue"` → **完成时带 Issue 编号/URL**：
  ```bash
  WF_ISSUE_NUM=<N> WF_ISSUE_URL=<url> bash .claude/ai-workflow/emit-event.sh B.issue ok "Issue #<N> 已建（待审核）"
  ```
  （`WF_ISSUE_NUM` 让控制面记下编号，供人审通过后实装阶段 C 使用——**务必上报**。）
- **正常判定无需处理**（如票为「无需处理」状态、纯文档无代码改动、调查后确认无需落 Issue）：
  `emit-event.sh B.issue skip "无需处理：<原因>"` —— 这是**正常终止**（任务转「已跳过」），**不要**当失败上报。
- 任一步**异常**遇阻（认证失败 / gh 出错 / 无法判定）：`emit-event.sh B.<当前phase> fail "<原因>"` —— 任务转「待裁决」。
- 区分原则：**能不能做完 vs 该不该做**。该不该做的否定结论 = `skip`；做的过程中坏了 = `fail`。

## 工作流程

### Step 0：确定工单号

从 `$ARGUMENTS` 提取 `PROJ-XXXX`；缺失则向用户索取。

### Step 1：读取 JIRA 工单

```bash
python3 .claude/ai-workflow/jira_api.py get <PROJ-XXXX>
```

- `description` 是 **ADF（Atlassian Document Format）JSON**，不是纯文本。需自行解析：
  - 正文 = 递归提取各节点的 `text`；
  - **链接** = ADF 节点 `marks` 中 `type: "link"` 的 `attrs.href`（设计书 / Confluence / 图等）。
- 把工单内所有链接文档**全部读取**（设计书、Confluence、外部 API 契约等），作为调查输入。
- **评论/回复必读**：`get` 返回的 `comments`（作者 / 时间 / `body`）里常有澄清、追加需求、方案决策与**上游文档 PR 链接**；`body` 同为 ADF，需递归解析文本 + 链接。**链接既可能是 ADF `link` mark，也可能是纯文本 URL（未加超链接）——两者都要抓**（例：评论里 `github.com/your-org/example-docs/pull/101` 可能是裸 URL），漏了会丢真相源。评论较多时可另取全量：`GET /rest/api/2/issue/<KEY>/comment`。
- 记录：`summary`、`status`、`issuetype`、`labels`、`parent`、`subtasks`、`comments`。

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

### Step 5：回写 JIRA 关联（可选）

`jira_api.py` 可**读**评论（`get` 已返回 `comments`），但暂**无写评论 API**（仅字段更新 / 状态转换）。如需 JIRA↔Issue 双向关联回写，需先补 `add_comment`；当前默认**跳过**，仅在报告里给出 Issue URL 供人工回填。

## Issue 正文模板

```markdown
# [PROJ-XXXX] <简明需求标题>

## 来源
- JIRA: https://your-domain.atlassian.net/browse/PROJ-XXXX
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

## 方案分歧点（需人类拍板——审核时勾选选定项）
- [ ] 方案 A：… ｜ 优 … ｜ 劣 …
- [ ] 方案 B：… ｜ 优 … ｜ 劣 …
（多方案**必须**用 `- [ ]` 复选框列出，供人审直接勾选；无分歧则写「无，方向单一」）

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
