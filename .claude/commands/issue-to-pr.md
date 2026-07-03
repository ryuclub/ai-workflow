# issue-to-pr（非交互编排：已审核 Issue → PR → 已实装）

把一个**已审核**的 GitHub Issue（链路第 3 步产物）一路推进到 **Draft PR**，并将 Issue 标记 `已实装`。
这是一条**非交互实装流程**：中途**不设人类确认点**，人类只在 Issue 闸口（前）和 PR review（后）把关。

整体工作流见仓根 [`docs/ai-workflow-rebuild-plan.md`](../../docs/ai-workflow-rebuild-plan.md)。本命令负责链路第 4 步（节点 5–8）。

## 输入

`$ARGUMENTS`：Issue 编号（如 `1`）。缺省时扫描 `已审核` 标签的开放 Issue 取最早一个。

## 关键约定（项目无关 —— 运行时读目标仓自己的约定）

本命令不预设任何具体仓的结构。**Phase 0 先读目标仓的 `CLAUDE.md` 与 `.claude/guidelines/`**，据此确定下列项：

- **base 分支**：**若环境变量 `WF_PR_BASE` 非空，优先用它**（控制面按仓配置注入，如 `stage`）；否则取目标仓 `.claude/guidelines/branch.md` 的规定；再无则 `gh repo view --json defaultBranchRef -q .defaultBranchRef.name`（默认分支）。**不写死 main/stage**。
- **构建 / 测试命令**：按目标仓技术栈判定（读 `CLAUDE.md`/`Makefile`）。Go 项目通常 `go build ./...`、`go vet ./...`、`gofmt -l`、`go test`；有 `Makefile` 则优先 `make lint`/`make test`。
- **代码结构 / 设计文档位置**：按目标仓约定（读其目录与 guidelines），不预设 `internal/`、`design/changes/`、`ita/` 等具体路径。
- **非交互**：所有「等待用户确认」一律跳过，以自动阈值替代。
- **失败即停**：任一硬关卡（build/test）重试上限仍失败 → 停止、不 push、不建 PR、不设「已实装」；Slack 播报原因，由接收器终态裁决转「待裁决」。
- **不署名**：commit / PR 均不加 AI 工具相关署名。
- **Issue 标题 + 主贴 = 唯一真相入口**：只读标题与正文，**不读不写 Issue 评论**；状态只看 label、通知只走 Slack、产物是 PR（`Closes #N`）。

## 参照文档（运行时从**目标仓**读取，不嵌副本）

| 文件（目标仓内） | 时机 |
|------|------|
| `CLAUDE.md` / `.claude/CLAUDE.md` | Phase 0 确定本仓约定（base/结构/真相源） |
| `.claude/guidelines/branch.md` | Phase 1 分支命名 + base |
| `.claude/guidelines/coding.md` | Phase 3 编码规约 |
| `.claude/guidelines/pre-commit-review.md` | Phase 5 提交前自查 |
| 设计文档模板（如 `design/changes/TEMPLATE.md`，若存在） | Phase 2 蓝图模板 |
| `.claude/skills/pr-creator/SKILL.md`（若目标仓有；否则用 `gh pr create`） | Phase 5 PR 创建 |

## 进度上报（结构化事件 → 控制面）

每个 Phase 边界调用 `emit-event.sh` 把进度回传控制面后端（后端点亮流水线节点、经 SSE 推 Dashboard、经 sink 转 Slack）。**非编排环境（人手直接跑）下静默跳过。**

```bash
bash .claude/ai-workflow/emit-event.sh <phase> <status> "<消息>"
```

约定：**每个 Phase 成对上报 `start` → `ok`**（phase 须用下列值）：
- 建分支+蓝图：`emit-event.sh C.blueprint start "建分支、派生蓝图"` → `emit-event.sh C.blueprint ok "蓝图 design/changes/PROJ-XXXX.md 完成"`
- 实装+自查：`emit-event.sh C.implement start "按蓝图实装"` → `emit-event.sh C.implement ok "实装完成，构建/自查通过"`
- 测试：`emit-event.sh C.test start "生成并跑测试"` → `emit-event.sh C.test ok "测试通过"`
- 提 PR+回写：`emit-event.sh C.pr start "提交 PR"` → **完成时带 PR URL**：
  ```bash
  WF_PR_URL=<url> bash .claude/ai-workflow/emit-event.sh C.pr ok "PR 已建，Issue 标记已实装"
  ```
- 任一 Phase 失败即停：`emit-event.sh C.<当前phase> fail "<原因>，已停，分支保留"`

## 执行步骤

### Phase 0：取 Issue 与幂等检查

1. 取 Issue：
   ```bash
   gh issue view <N> --json number,title,body,labels,url
   ```
   - 校验含 `已审核` 标签；否则中止并报告（非本命令处理对象）。
2. 从标题 `[PROJ-XXXX]` 提取工单号。
3. **幂等/对齐检查**：
   - 已存在 `*/PROJ-XXXX-*` 分支或关联的**开放 PR** → 进入**对齐模式（Phase 0.5）**，不重新开工也不直接退出。
   - 关联 PR 均已合并/关闭且 Issue 已带 `已实装` → 无事可做，报告后退出。

### Phase 0.5：对齐模式（重跑 / Issue 更新后）

> 场景：任务重跑后 Issue 可能被重建、人工或塔台的审核决策（方案分歧点勾选、验收条件）可能已变化。
> 此时**不能**因为「PR 已存在、实装已完成」就跳过——必须以**最新 Issue 为准**核对既有 PR 并修正。

1. `emit-event.sh C.blueprint start "对齐模式：按最新 Issue 核对既有 PR"`
2. checkout 既有分支并同步：`git fetch origin && git checkout <既有分支> && git merge origin/<BASE>`（冲突则报告遇阻）。
3. **逐条核对**最新 Issue（正文、勾选的方案分歧点、验收条件）与 PR 现有实现的差异：
   - 方案分歧点勾选变化 → 对应实现改动；
   - 需求/验收条件增删 → 对应补齐/回退；
   - 完全一致 → 报告「PR 与最新 Issue 一致，无需变更」，`emit-event.sh C.pr ok`，退出（任务回到 PR 审查）。
4. 有差异 → 按 Phase 3/4 的标准修正实现并跑硬关卡（构建/测试全绿）。
5. push 到既有分支（更新既有 PR，**不新开 PR**）；更新 PR 描述说明「按最新 Issue（第 N 版）对齐」；
   `emit-event.sh C.pr ok "PR 已按最新 Issue 对齐"`（带 `WF_PR_URL`）。
6. 之后跳过 Phase 1（分支已在），按需执行 Phase 5 的回写收尾。

### Phase 1：建分支

`<BASE>` = Phase 0 判定的 base 分支（目标仓 branch.md 或默认分支）。
```bash
git fetch origin <BASE>
git checkout -b <类型>/PROJ-XXXX-<描述> --no-track origin/<BASE>
```
- `<类型>` 由 Issue 性质推定（bug→`fix`，新功能→`feat`，性能→`perf`…，参照目标仓 `branch.md`）。
- 工作树须干净；不干净则中止报告。

### Phase 2：派生实装蓝图

蓝图落在**目标仓的设计文档目录**（按本仓约定，常见 `design/changes/PROJ-XXXX.md`；无此约定则放约定位置或 PR 描述）。

1. 以 **Issue 正文为唯一依据**（背景 / 需求拆解 / 现状调查 / 方案概要 / 验收）。
2. 用 `Agent(Plan)` 把 Issue 的「方案概要」下沉为**逐文件 / 逐函数变更清单 + 实装顺序 + 风险**，按目标仓的设计文档模板（如 `design/changes/TEMPLATE.md`，若有）落成蓝图文件。
   - Issue 的**方案分歧点**用 `- [ ]` / `- [x]` 复选框标注：**取被勾选（`- [x]`）的方案为选定方向**；若无任何勾选（都是 `- [ ]`）视为正文未明示，取风险最小者并在蓝图「待确认」记录假设；勾选多项冲突时也转「待确认」交人。
3. 蓝图是实装的施工依据，且随代码一并进 PR。

### Phase 3：实装 + 自查循环（自动，最多 3 次）

预读 `.claude/guidelines/coding.md`。每轮：

1. **实装**：按蓝图改代码。
2. **构建/静态检查**（硬关卡，用 Phase 0 判定的命令）：
   ```bash
   # Go 项目示例；有 Makefile 则用 make lint：
   go build ./... && go vet ./... && gofmt -l <改动文件>
   ```
3. **自查**：委托 `Agent(general-purpose)` 核对实现与 Issue 契约、蓝图的一致性（代码审视视角）。
4. 有指摘 → 自动修正后重入循环；无指摘 → 出循环。
5. 3 次仍有硬关卡失败 → **失败即停**（见末尾「失败处理」）。

### Phase 4：测试 case（自动）

1. **无确认门**：直接按 Issue 验收标准与目标仓现有测试风格生成用例。
2. 执行：
   ```bash
   go test ./<相关包>/...
   ```
3. 全绿 → Phase 5；失败且非方案问题 → Phase 4 内修正重跑（≤3 次）；属方案问题 → 失败即停，交还人类。

### Phase 5：Commit / Push / PR

预读 `.claude/guidelines/pre-commit-review.md`，逐项自查（hook 仅输出清单，不阻塞）。

1. `git status` 确认变更（含蓝图文件）。
2. `git add <具体文件>`（排除自动生成 / 机密文件）。
3. commit message 按目标仓 `workflow.md` 格式：`<类型>(PROJ-XXXX): <简述>`（**不加 AI 署名**）。
4. `git push -u origin <分支名>`。
5. 调用 `pr-creator`（若目标仓有，否则 `gh pr create`）创建 **Draft PR**，**base = `<BASE>`**：
   - 正文末尾加 `Closes #<Issue 编号>`（合并即自动关单）。
   - 标签按分支前缀（feat→feature / fix→bugfix…）。

### Phase 6：回写 Issue 标签

PR 创建成功后（只动 label，不评论 Issue）：
```bash
gh issue edit <N> --add-label "已实装" --remove-label "已审核"
```
Slack 播报 `✅ [C] Issue #N → PR <url>，已标『已实装』`，并报告 PR URL。

## 失败处理（任一硬关卡超限 / 遇阻不具备开工条件）

1. **不 push、不建 PR、不设『已实装』**。
2. **不评论 Issue**；改为 Slack 播报：`⚠️ [C] Issue #N 在 <Phase> 遇阻：<原因>`。
3. 退出即可——接收器的**终态裁决**会发现本 Issue 未标『已实装』，自动把它转『待裁决』并 Slack 通知（无需 C 自己改标签）。

## 与 /loop 协同

`/loop` 扫 `已审核` Issue → 对每个调用 `/issue-to-pr <N>`。Phase 0 的幂等/对齐检查确保重复触发不重复开工：
已有 PR 时走对齐模式——与最新 Issue 一致则空跑退出，有差异则修正既有 PR（不重开）。
