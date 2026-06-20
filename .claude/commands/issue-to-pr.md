# issue-to-pr（非交互编排：已审核 Issue → PR → 已实装）

把一个**已审核**的 GitHub Issue（链路第 3 步产物）一路推进到 **Draft PR**，并将 Issue 标记 `已实装`。
这是 `dev-flow` 的**非交互变体**：中途**不设人类确认点**，人类只在 Issue 闸口（前）和 PR review（后）把关。

整体工作流见 [`.claude/ai-workflow/docs/ai-workflow.md`](../ai-workflow/docs/ai-workflow.md)。本命令负责链路第 4 步。

## 输入

`$ARGUMENTS`：Issue 编号（如 `1`）。缺省时扫描 `已审核` 标签的开放 Issue 取最早一个。

## 关键约定（项目无关 —— 运行时读目标仓自己的约定）

本命令不预设任何具体仓的结构。**Phase 0 先读目标仓的 `CLAUDE.md` 与 `.claude/guidelines/`**，据此确定下列项：

- **base 分支**：优先取目标仓 `.claude/guidelines/branch.md` 的规定；无则 `gh repo view --json defaultBranchRef -q .defaultBranchRef.name`（默认分支）。**不写死 main/stage**。
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

## 进度通知（临时，后续移除）

为便于观察自动流程，每个 Phase 起止用 Slack 播报：
```bash
bash .claude/ai-workflow/notify.sh "<消息>"
```
播报点（C 全程）：
- 开始：`🟢 [C] Issue #N（MOS-XXXX）开始实装`
- 蓝图派生：`📐 [C] MOS-XXXX 蓝图 design/changes/MOS-XXXX.md 完成`
- 实装+自查过：`🔧 [C] MOS-XXXX 实装完成，构建/自查通过`
- 测试过：`🧪 [C] MOS-XXXX 测试通过`
- PR 建好：`✅ [C] MOS-XXXX → PR <url>，Issue 已标「已实装」`
- 失败即停：`⚠️ [C] MOS-XXXX 在 <Phase> 失败：<原因>，已停，分支保留`

## 执行步骤

### Phase 0：取 Issue 与幂等检查

1. 取 Issue：
   ```bash
   gh issue view <N> --json number,title,body,labels,url
   ```
   - 校验含 `已审核` 标签；否则中止并报告（非本命令处理对象）。
2. 从标题 `[<工单号>]` 提取工单号。工单号通用格式 `[A-Z][A-Z0-9]*-\d+`，兼容 JIRA（`MOS-1234`）与 Linear（`SUM-123`）；下文凡 `MOS-XXXX` 均为占位，以实际工单号替换。
3. **幂等检查**（防 /loop 重复触发）：
   - 已存在 `*/<工单号>-*` 分支或关联 PR → 不重复开工，报告现状后退出。
   - Issue 已带 `已实装` → 跳过。

### Phase 1：建分支

`<BASE>` = Phase 0 判定的 base 分支（目标仓 branch.md 或默认分支）。
```bash
git fetch origin <BASE>
git checkout -b <类型>/MOS-XXXX-<描述> --no-track origin/<BASE>
```
- `<类型>` 由 Issue 性质推定（bug→`fix`，新功能→`feat`，性能→`perf`…，参照目标仓 `branch.md`）。
- 工作树须干净；不干净则中止报告。

### Phase 2：派生实装蓝图

蓝图落在**目标仓的设计文档目录**（按本仓约定，常见 `design/changes/MOS-XXXX.md`；无此约定则放约定位置或 PR 描述）。

1. 以 **Issue 正文为唯一依据**（背景 / 需求拆解 / 现状调查 / 方案概要 / 验收）。
2. 用 `Agent(Plan)` 把 Issue 的「方案概要」下沉为**逐文件 / 逐函数变更清单 + 实装顺序 + 风险**，按目标仓的设计文档模板（如 `design/changes/TEMPLATE.md`，若有）落成蓝图文件。
   - Issue 若标注了**方案分歧点**：已审核即视为人类已选定方向；正文未明示选择时，取风险最小者并在蓝图「待确认」记录假设。
3. 蓝图是实装的施工依据，且随代码一并进 PR。

### Phase 3：实装 + 自查循环（自动，最多 3 次）

预读 `.claude/guidelines/coding.md`。每轮：

1. **实装**：按蓝图改代码。
2. **构建/静态检查**（硬关卡，用 Phase 0 判定的命令）：
   ```bash
   # Go 项目示例；有 Makefile 则用 make lint：
   go build ./... && go vet ./... && gofmt -l <改动文件>
   ```
3. **自查**：以 `/dev-review` 同等视角委托 `Agent(general-purpose)`，核对与 Issue 契约、蓝图的一致性。
4. 有指摘 → 自动修正后重入循环；无指摘 → 出循环。
5. 3 次仍有硬关卡失败 → **失败即停**（见末尾「失败处理」）。

### Phase 4：测试 case（自动）

1. 参照 `dev-test` / `dev-test-gen` 的设计观点，但**跳过其确认门**：直接按 Issue 验收标准与现有测试风格生成用例。
2. 执行：
   ```bash
   go test ./<相关包>/...
   ```
3. 全绿 → Phase 5；失败且非方案问题 → Phase 4 内修正重跑（≤3 次）；属方案问题 → 失败即停，交还人类。

### Phase 5：Commit / Push / PR

预读 `.claude/guidelines/pre-commit-review.md`，逐项自查（hook 仅输出清单，不阻塞）。

1. `git status` 确认变更（含蓝图文件）。
2. `git add <具体文件>`（排除自动生成 / 机密文件）。
3. commit message 按目标仓 `workflow.md` 格式：`<类型>(MOS-XXXX): <简述>`（**不加 AI 署名**）。
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

`/loop` 扫 `已审核` Issue → 对每个调用 `/issue-to-pr <N>`。Phase 0 的幂等检查确保重复触发不重复开工。
