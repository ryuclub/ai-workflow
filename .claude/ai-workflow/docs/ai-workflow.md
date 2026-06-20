# AI 自动协作工作流（设计记录）

> 状态：**设计中，未实装**。本文记录与人类收敛出的工作流模型，作为后续落地各「件」的依据。
> 触发载体（最终）：**webhook 事件驱动** —— JIRA/Linear/GitHub webhook → 固定隧道 → **单例接收器**（多仓路由），非轮询。
> 工单入口双源并行：**JIRA**（触发状态）与 **Linear**（触发标签）经各自端点进同一套标题路由，落到同一 `jira-to-issue`（B）。
> base 分支：**项目无关**，C 运行时按目标仓 `branch.md`/默认分支判定，不写死。
> （早期曾设想本地半自动 `/loop` 轮询；后改为事件驱动接收器。下文凡提 `/loop` 为历史，以本注为准。）

## 1. 目标

人类只在**两端**把关——起票（提需求）与最终合并；中间「调查 → 需求落地 → 实装 → 测试 → PR」由 AI 全自动完成。
人类对需求的唯一审核闸口前置到**写代码之前**，避免 AI 误解需求后才在 PR 阶段返工。

## 2. 四层工件模型（递进，不复述）

每层只写自己那一层，下层由上层派生：

| 层 | 工件 | 装什么 | 粒度 | 谁产出 | 谁消费 |
|----|------|--------|------|--------|--------|
| why | **工单（JIRA/Linear）** | 业务诉求、背景、为什么做 | 业务 | 人类（PM） | AI 调查输入 |
| what | **GitHub Issue** | 做什么 + 实现方向 + 验收标准 | 粗（够人判断「该做吗 / 方向对吗」） | AI 调查分析 | **人类审核**（label 闸口） |
| how | **需求文档** `design/changes/MOS-XXXX.md` | 逐文件 / 逐函数变更、实装顺序、风险 | 细（够 AI 照着敲代码） | AI（由 Issue 派生） | AI 实装指导；随 PR 顺带 review |
| code | **代码 + 测试** | 实现 | — | AI | 人类 PR review |

要点：
- **Issue ≠ JIRA 搬运**。Issue 是「带 file:line 的代码级契约」，属于代码所在地（GitHub），是 JIRA 业务诉求落成的工程化契约。
- **Issue 须显式抛出方案分歧点**。方案选择（原 dev-flow Phase 2）已并入这唯一闸口；当一个工单有多个可行方案时，Issue 必须把分歧点列给人类选，否则等于 AI 单方面替人拍方案。
- **需求文档不是审核闸口**。它是仓内「本地可追踪副本」+「实装蓝图」：随代码走（同一 PR/commit），engineer 读代码就地可查，且 AI 实装时照它敲。它在第 5 步 PR review 里被顺带看，不单设关卡。
- **需求文档是「蓝图」不是「施工图」**。对齐现有 `design/changes/*.md` 粒度（技术决策 + 影响范围 + 文件名级），给方向；逐行/接口签名级细节仍由 AI 实装时补，别指望它消除全部实装歧义。
- 三处不复述：Issue 写「概要/方向」，需求文档写「逐文件详设」，层层递进。

## 3. 链路（6 步）

```
1. 人    工单（JIRA / Linear）                            = 业务 why
2. AI    调查 + 分析 → GitHub Issue(what+方向+验收) +「待审核」  ← 唯一审核闸口
3. 人    审核 / 修改 Issue →「已审核」                        ← /loop 监到「已审核」即开工
4. AI    ① 由 Issue 派生 design/changes/MOS-XXXX.md（逐文件实装蓝图）
         ② 照蓝图实装
         ③ 测试 case
         ④ 提交 PR（Closes #Issue，含蓝图文档）
         ⑤ Issue 打「已实装」
5. 人    PR review（代码 + 需求文档一起看）
6. 人    合并
```

## 4. 标签状态机（GitHub Issue）

| label | 含义 | 谁打 | 触发 |
|-------|------|------|------|
| `待审核` | AI 整理的需求待人类审核 | AI（建 Issue 时） | — |
| `已审核` | 人类已审核，AI 可开始实装 | 人类 | `/loop` 监到 → 进入第 4 步 |
| `已实装` | AI 已完成实装并提交 PR | AI（提交 PR 后） | 人类转入 PR review |

流转：`待审核` →(人审)→ `已审核` →(AI 实装)→ `已实装`。同一时刻一个 Issue 只持一个状态 label。

## 5. 件映射（最终实现）

| 环节 | 件 |
|------|----|
| 读工单 | `.claude/ai-workflow/jira_api.py`（JIRA）/ `linear_api.py`（Linear）—— 工具集自带，`get` 输出同构，不依赖目标仓 skill |
| 第 2 步 调查→Issue | `jira-to-issue`（B）：`Agent(Explore/Plan)` + `gh issue create`/upsert |
| label / 闸口 | `gh label`（待审核/已审核/已实装/待裁决） |
| 触发 | `webhook-receiver/server.py`（单例，事件驱动 + 多仓路由），非轮询 |
| 第 4 步 实装→PR | `issue-to-pr`（C）：非交互编排（项目无关，读目标仓约定） |
| 通知 / 裁决 | `notify-slack` + 接收器终态裁决（未出 PR→待裁决） |
| 第 5 步 PR review | 人工 |

## 6. 人类把关点（仅两处）

- **闸口 A**：Issue `待审核` → `已审核`（审需求与方向，写代码前）。
- **闸口 B**：PR review → 合并（审实现，受 branch protection 保护）。

## 7. 可行性核查结论（2026-06-18）

调查结论：**模型合理、技术可行，无硬墙**。瓶颈不在技术，而在「业务理解确认点」的设计——已通过把 Phase 1/2 前置进 Issue 闸口解决。

已查实并解决：

| 项 | 结论 |
|----|------|
| JIRA 读取 | ✅ 已配 `.env`（自 Mosavi-Channel-Service/.claude/config/claude.env 复制）。修复 `jira_api.py` 的 `load_env` 不去引号 bug 后，`get MOS-3245` 读通 |
| base 分支 | ✅ 本仓定 `main`（无 `stage`） |
| pre-commit hook | ✅ **不阻塞**：纯 `cat` 输出审查清单后 exit 0，无 `read`/`exit≠0`，不拦自动 commit |
| gh 权限 | ✅ token 含 `repo/workflow/admin:org`，建 issue/label/PR 足够；`main` 无 branch protection，闸口 B 靠人工手动合并 |

待办 / 仍需注意：

- **JIRA `description` 是 ADF（Atlassian Document Format）JSON**，非纯文本 → `jira-to-issue` 须解析 ADF 提取正文与链接。（**Linear `description` 是 Markdown 纯文本**，无此负担，直接可用。）
- **`dev-flow` 有 5 处「等待用户确认」**（Phase 1/2/4/5/7）→ 第 4 步需非交互变体；Phase 1/2 的业务判断已前移至 Issue 闸口，4/7 可改自动阈值。
- **JIRA 无评论 API**（`jira_api.py` 仅字段更新/状态转换）→ 可选的「回写 JIRA 链接」需补 `add_comment`，非阻塞。（**Linear 有评论 API**：`linear_api.py comment`，可回写 Issue 链接，当前默认仍跳过以保持最小副作用。）
- AI 调查质量 = 整条链上限：闸口 A 之前的 Issue 若调查不足，下游全偏。
- `/loop` 轮询的并发与去重（同一 Issue 重复触发）需幂等保护。
- 第 4 步全自动跨度大（实装+测试+PR），失败回退与中断恢复策略待定。
- **接收器依赖 `claude` 登录态**：headless `claude -p` 的 OAuth 登录在 keychain/登录会话；后台服务（launchd/systemd）环境够不到时会「Not logged in」却退出 0 → 静默假成功。对策：登录会话内后台跑接收器，或配长期 token（`CLAUDE_CODE_OAUTH_TOKEN`/`ANTHROPIC_API_KEY`）；接收器已加未登录显式告警。（2026-06-18 实测踩坑）
