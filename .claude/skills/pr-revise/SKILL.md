---
name: pr-revise
description: "按 GitHub PR 的 review 意见修订：checkout PR 分支、读未处理的 review threads、改代码、跑硬关卡、push、逐条回复并尽量 resolve。触发时机: 按review意见改PR / 修订PR / pr-revise <PR号>"
user-invokable: true
---

# PR 修订（按 review 意见改代码 → push → 回复）

## 概述

把一个**已被 request changes 的 PR**按 reviewer 意见改好并 push 回同一分支，逐条回复/resolve review thread。
这是流水线的**第 D 步（PR 审查循环的修订段）**：人在 GitHub 上 review 提出 changes，控制面轮询到后调用本 skill 修订；
改完 push，等人**再次 review**，如此可多轮，直到 approved。

整体工作流见仓根 [`docs/ai-workflow-rebuild-plan.md`](../../../docs/ai-workflow-rebuild-plan.md)。

## 输入

- `$ARGUMENTS`：PR 编号（如 `123`）。缺省时报错退出（本 skill 不猜 PR）。

## 关键约定（项目无关 —— 运行时读目标仓自己的约定）

本 skill 不预设任何具体仓的结构。**Phase 0 先读目标仓的 `CLAUDE.md` 与 `.claude/guidelines/`**，据此确定：

- **构建 / 测试命令**：按目标仓技术栈判定（Go 通常 `go build ./...`、`go vet ./...`、`gofmt -l`、`go test`；有 `Makefile` 则优先 `make lint`/`make test`）。
- **编码规约 / 提交前自查**：读 `.claude/guidelines/coding.md`、`pre-commit-review.md`。
- **非交互**：所有「等待用户确认」一律跳过，以自动阈值替代。
- **失败即停**：任一硬关卡（build/test）重试上限仍失败 → 停止、不强推、Slack 播报原因，由控制面转「待裁决」。
- **不署名**：commit / PR 回复均不加 AI 工具相关署名。
- **只认 review 意见 + 代码**：依据是 PR 的 review comments 与 diff，不臆造需求。

## 进度上报（结构化事件 → 控制面）

阶段边界调用 `emit-event.sh` 回传控制面（点亮「按意见修订」节点、经 SSE 推 Dashboard、经 sink 转 Slack）。**非编排环境（人手直接跑）下静默跳过。**

```bash
bash .claude/ai-workflow/emit-event.sh D.revise start "读取 review 意见并修订"
bash .claude/ai-workflow/emit-event.sh D.revise ok   "修订已 push，等待再次审查"
bash .claude/ai-workflow/emit-event.sh D.revise fail "<原因>，已停"
```

- **区分原则**：能不能改完 vs 意见是否合理。改的过程坏了（build/test 超限、push 失败）= `fail`；意见本身是方向性分歧、超出本 PR 范围 = 也按 `fail` 上报并说明，交人裁决（本 skill 不擅自扩大改动范围）。

## 执行步骤

### Phase 0：切到 PR 分支 + 幂等/前置检查

1. 取 PR 基本信息与分支：
   ```bash
   gh pr view <N> --json number,headRefName,baseRefName,state,url,title
   ```
   - PR 非 `OPEN`（已合并/关闭）→ 无需修订，`emit-event.sh D.revise ok "PR 已 <state>，无需修订"` 后退出。
2. checkout 到 PR 的 head 分支（在控制面提供的干净 worktree 内）：
   ```bash
   gh pr checkout <N>
   ```
   - 失败（如 PR 来自 fork 无写权限）→ `fail` 上报并退出。
3. 读目标仓 `CLAUDE.md` / `.claude/guidelines/` 确定构建/测试命令与编码规约。

### Phase 1：收集未处理的 review 意见

1. **review 决议与逐条 comment**（并行取）：
   ```bash
   gh pr view <N> --json reviews
   gh api repos/{owner}/{repo}/pulls/<N>/comments --paginate
   ```
2. 解析每条 review comment：作者、文件/行、内容、`in_reply_to_id`、是否已 resolved。
3. 只挑**未妥善处理**的：未回复、或标注 changes requested 且代码未改的。已 resolve / 已合理答复的跳过。
4. 若**没有**任何待处理意见（都已处理）→ `emit-event.sh D.revise ok "无待处理 review 意见"` 后退出（幂等，防重复触发）。

### Phase 2：按意见修订 + 自查循环（自动，最多 3 次）

每轮：

1. **改代码**：逐条落实 review 意见；**严格限定在意见涉及的范围**，不顺手重构无关代码。
2. **构建/静态检查**（硬关卡，用 Phase 0 判定的命令）：
   ```bash
   go build ./... && go vet ./... && gofmt -l <改动文件>
   ```
3. **测试**：跑相关测试；review 若指出测试缺口则补测试。
4. 有失败 → 自动修正重入循环；全绿 → 出循环。
5. 3 次仍有硬关卡失败 → **失败即停**（见末尾）。

### Phase 3：Commit / Push（回同一 PR 分支）

预读 `.claude/guidelines/pre-commit-review.md`，逐项自查。

1. `git add <具体改动文件>`（排除自动生成/机密文件）。
2. commit message 按目标仓 `workflow.md` 格式，简述本轮针对的 review 意见（**不加 AI 署名**）。
3. push 回 PR 的 head 分支：
   ```bash
   git push origin HEAD
   ```

### Phase 4：逐条回复并 resolve review thread

对每条本轮已落实的 review comment：

1. 回复说明「已按意见如何修改」（引用 commit / 文件行），**中文、不署名**：
   ```bash
   gh api repos/{owner}/{repo}/pulls/<N>/comments/<comment_id>/replies -f body="已按意见修改：……"
   ```
2. 尽量 resolve 对应 thread（GraphQL `resolveReviewThread`，若权限/接口不可用则只回复不 resolve）。
3. 合理拒绝的意见：回复给出理由，不改代码。

### Phase 5：收尾上报

```bash
bash .claude/ai-workflow/emit-event.sh D.revise ok "第 N 轮修订已 push，逐条回复完成，等待再次审查"
```

- 报告：本轮处理了哪些意见、push 的 commit、仍待人确认的点。
- **不改 PR 状态标签、不 approve**（是否通过由人在 GitHub 上再次 review 决定，控制面轮询感知）。

## 失败处理（硬关卡超限 / 意见需人裁决 / push 失败）

1. **不强推、不硬改**。
2. `emit-event.sh D.revise fail "<Phase X 遇阻原因>"`；控制面据此把任务转「待裁决」并 Slack 通知。
3. 若属**方向性分歧或超出本 PR 范围**：不擅自扩大改动，如实回复该 review thread 说明需人决策，并按 `fail` 上报。

## 注意事项

- **范围克制**：只改 review 意见涉及的部分，避免顺手重构导致 review 面扩大、循环收不敛。
- **输出语言**：全程中文。commit / PR 回复 / 报告均不加 AI 工具署名。
- **多轮幂等**：每次触发先查「有无未处理意见」，无则直接 ok 退出，确保重复触发不重复改。
