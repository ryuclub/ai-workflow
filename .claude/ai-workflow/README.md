# Mosavi AI Workflow（自包含工具集）

把工单（JIRA / Linear）**事件驱动**地自动跑成 GitHub Issue → 实装 → PR 的 AI 协作工具集。
单元 = `.claude/skills/jira-to-issue`（B）+ `.claude/commands/issue-to-pr`（C）+ `.claude/ai-workflow/`（本目录：JIRA/Linear 客户端 / Slack / 接收器 / 安装脚本 / 文档）。

```
JIRA 票打「AI处理」标签   ─webhook→ 接收器 → 跑 B → GitHub Issue + 待审核 → Slack
Linear issue 打「AI处理」 ─webhook→ 接收器 → 跑 B → GitHub Issue + 待审核 → Slack
人审 Issue 打「已审核」    ─webhook→ 接收器 → 跑 C → PR(Closes #N) + 已实装 → Slack
人 review PR → 合并
```
> 入口支持 **JIRA 与 Linear 双源并行**：JIRA 按触发状态触发、Linear 按触发标签触发，二者都读票标题走同一套多仓路由，落到同一个 `jira-to-issue`（B）。工单源由工单号前缀自动判定（`MOS-`→JIRA，`SUM-` 等 `TEAM-数字`→Linear）。
设计与完整手册见 [`docs/ai-workflow.md`](./docs/ai-workflow.md)、[`docs/ai-workflow-setup.md`](./docs/ai-workflow-setup.md)。

---

## 安装 A：新机器（跑工具的常开设备）

```bash
git clone git@github.com:ryuclub/ai-workflow.git
cd ai-workflow

# 1. 配置（幂等）：依赖检查 + 建标签 + 生成/填 .env + 验证（含 go build / docker 检测）
.claude/ai-workflow/setup.sh
#   - 自动生成 GITHUB_WEBHOOK_SECRET / JIRA_WEBHOOK_TOKEN
#   - JIRA 凭据：若 ~/work/MosaviJP/Mosavi-Channel-Service/.claude/config/claude.env 存在会自动合入；
#     否则手动把 ATLASSIAN_USERNAME/API_KEY/DOMAIN、JIRA_PROJECT 加到 .claude/ai-workflow/.env
#   - Slack：把 SLACK_WEBHOOK_URL 加到同一 .env

# 2. 固定具名隧道 + 服务化（开机自启），见 docs/ai-workflow-setup.md 第 7 节
sudo cloudflared service install        # cloudflared 隧道可安全服务化（不碰 claude）
# ⚠️ 接收器不要直接 systemd/launchd 服务化：后台环境够不到 keychain → claude「Not logged in」却退出 0（假成功，不建 Issue/PR）。
#    在图形登录会话里后台跑：nohup python3 .claude/ai-workflow/server.py >/tmp/wf-recv.log 2>&1 &
#    （要真服务化则先 `claude setup-token` 配 CLAUDE_CODE_OAUTH_TOKEN/ANTHROPIC_API_KEY 注入进程环境）

# 3. 注册 webhook（固定域名，一次）
PUBLIC_URL=https://hook.你的域名 .claude/ai-workflow/setup.sh --serve
#   起接收器 + 注册/更新 GitHub webhook，并打印 JIRA 规则填写清单
```
> 临时验证可省去隧道/服务化，直接 `.claude/ai-workflow/setup.sh --serve`（起临时 trycloudflare 隧道，URL 会变）。

---

## 安装 B：给一个新项目仓加 AI 能力

```bash
# 在工具仓里执行，把全套件复制进目标仓（skills/commands/guidelines/hooks/引擎，不带 .env 密钥）
.claude/ai-workflow/install-into.sh /path/to/目标仓

cd /path/to/目标仓
.claude/ai-workflow/setup.sh                       # 填/生成 .env、建标签、验证
git add .claude && git commit -m "chore: 接入 AI 工作流工具集"   # 必须提交：headless 在 worktree 跑，要能看到
.claude/ai-workflow/setup.sh --serve               # 起接收器 + webhook（或 PUBLIC_URL=… 固定隧道）
# 再在 JIRA 建 Automation 规则 和/或 Linear 建 Webhook（setup --serve 末尾会打印两者填写清单）
```

> 现状：每个装了的仓**各跑一个接收器**（self-contained）。"单例接收器服务多仓 + 登记表路由"为后续增强。

---

## ⚠️ 安全模型（务必理解）

接收器本质是「外网 webhook → 本机 `claude -p --dangerously-skip-permissions`」= **远程代码执行面**。缓解：
- **验签**：GitHub HMAC-SHA256（`X-Hub-Signature-256`）/ JIRA 共享 token（`X-Webhook-Token`）/ Linear HMAC-SHA256（`Linear-Signature`，无 `sha256=` 前缀）；密钥未配即 500 拒绝。
- **固定命令面**：只派发 `/issue-to-pr <整数>`、`/jira-to-issue <工单号>`（工单号正则 `[A-Z][A-Z0-9]*-\d+`，覆盖 JIRA `MOS-`/Linear `SUM-`）。
- **隔离**：每任务独立 `git worktree`，跑完清理；运行时注入合并 `.env`。
- **仅 127.0.0.1 监听**，只经隧道暴露。**去重**防重投。
- 残留风险：AI 在 skip-permissions 下自身可能做预期外操作 —— 受控环境、可信网络运行。

## 信号原则
- **状态只看 GitHub 标签**：`待审核 / 已审核 / 已实装 / 待裁决`（单值覆盖、不累积）。
- **通知只走 Slack**（带仓名+时间戳）；不静默退出。
- **Issue 标题 + 主贴 = 处理唯一真相**：B/C 不读不写 Issue 评论。
- **产物是 PR**（`Closes #N`）。

## 运维
- 接收器进程管理：在**图形登录会话**里 `.claude/ai-workflow/ensure-receiver.sh [start|stop|restart]`（`start` 幂等确保在跑，`restart` 不看状态先停再起）。须能访问 keychain 的 `claude` 登录态，否则 headless claude 会「Not logged in」假成功。
- B/C 的 skill 改动须 commit 到目标仓 `main`/默认分支（headless 在其 worktree 跑）。
- 隧道换 URL → 重跑 `setup.sh --serve` 自动更新 GitHub webhook；JIRA 规则 URL 需手动改（固定隧道则免）。
