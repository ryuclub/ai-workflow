# AI 自动协作工作流 · 安装与部署手册（深度参考）

> 快速安装看 [`../README.md`](../README.md)（新机器 / 新项目两套流程）。本文是细节、多仓部署、服务化与排错的深度参考。
> 设计记录见 [`ai-workflow.md`](./ai-workflow.md)。

触发模型：**事件驱动**（JIRA / GitHub webhook → 固定隧道 → 单例接收器 → 目标仓 worktree 里跑 headless `claude -p`）。

```
JIRA 票打「AI处理」标签 ─webhook→ 接收器(按标题路由到目标仓) → 跑 B → Issue+待审核 → Slack
人审 Issue 打「已审核」  ─webhook→ 接收器(按 payload 仓名路由)  → 跑 C → PR(Closes #N)+已实装 → Slack
                                                                  └ 遇阻 → 待裁决 + Slack
```

## 1. 组件（自包含工具集 `.claude/ai-workflow/`）

| 路径 | 角色 |
|------|------|
| `.claude/skills/jira-to-issue/`（B） | 读 JIRA→调查分析→建/重建 Issue（入口，Claude 加载） |
| `.claude/commands/issue-to-pr.md`（C） | 非交互编排：已审核 Issue→实装→PR→已实装（入口，Claude 加载） |
| `.claude/ai-workflow/jira_api.py` | 自带 JIRA 客户端（不依赖目标仓自有 skill） |
| `.claude/ai-workflow/notify.sh` | Slack 通知器 |
| `.claude/ai-workflow/server.py` | 单例 webhook 接收器（多仓路由） |
| `.claude/ai-workflow/setup.sh` | 安装/配置脚本 |
| `.claude/ai-workflow/install-into.sh` | 把工具集装进目标仓 |
| `.claude/ai-workflow/config.json` | 登记表：default + repos{github,path,match}（gitignore） |
| `.claude/ai-workflow/.env` | 合并凭据（gitignore） |
| `.claude/ai-workflow/docs/` | 本手册 + 设计记录 |

**安装单元** = `.claude/skills/jira-to-issue` + `.claude/commands/issue-to-pr` + `.claude/ai-workflow/`。

## 2. 前置依赖

```bash
python3 git gh cloudflared claude openssl   # 基础
# C 实装还需：go 工具链、GOPRIVATE=github.com/<org>/*、能拉私有仓的凭据（SSH key 或 HTTPS token）、docker（ita 测试）
```
- `gh auth login`（对目标仓有 `repo` 权限）、`claude` 已登录。
- `setup.sh` 第 1 步会逐项检测（含 `go build ./...` 权威验证私有依赖可拉）。

## 3. 凭据（单一合并 `.env`，gitignore）

`.claude/.gitignore` 含 `.env`，凡名为 `.env` 者不入库。`setup.sh` 自动生成缺失项：
```bash
.claude/ai-workflow/setup.sh   # 自动：生成 GITHUB_WEBHOOK_SECRET/JIRA_WEBHOOK_TOKEN + 设默认 + 验证
```
`.claude/ai-workflow/.env` 关键键：
```bash
ATLASSIAN_USERNAME=  ATLASSIAN_API_KEY=  ATLASSIAN_DOMAIN=<org>.atlassian.net  JIRA_PROJECT=PROJ   # JIRA
SLACK_WEBHOOK_URL=                                                                                  # Slack
GITHUB_WEBHOOK_SECRET=  JIRA_WEBHOOK_TOKEN=  JIRA_TRIGGER_STATUS=待AI处理  APPROVED_LABEL=已审核  PORT=8787  # 接收器
```
- 验证 JIRA：`python3 .claude/ai-workflow/jira_api.py get PROJ-3245`；验证 Slack：`bash .claude/ai-workflow/notify.sh 测试`。

## 4. 状态标签（`setup.sh` 自动建，幂等）
`待审核`(FBCA04) → `已审核`(0E8A16) → `已实装`(1D76DB)；遇阻 → `待裁决`(D93F0B)。
```
待审核 ─人审→ 已审核 ─C成功→ 已实装 ─人合并→ 关单
                  └─C遇阻→ 待裁决 ─人补Issue后重打已审核→ 重试
JIRA票重触发：任意态 ─B upsert(重建主贴)→ 待审核
```

## 5. 登记表 `config.json`（多仓路由核心）

```jsonc
{
  "default": "channel",                       // 标题无信号时的默认仓
  "repos": {
    "channel": { "github": "<org>/<repo>", "path": "/abs/本机/<repo>", "match": [] },
    "group":   { "github": "<org>/<repo>",   "path": "/abs/本机/<org>-Group-Service",   "match": ["group","群组"] }
  }
}
```
- `path` = **该机器上**目标仓的绝对 clone 路径（接收器据此建 worktree）。机器相关 → gitignore。
- **路由**：
  - GitHub(C)：payload `repository.full_name` 反查 `repos[*].github`。未登记 → Slack 忽略。
  - JIRA(B)：读票**标题** → ① 含 `[<repo-key>]` 显式标记优先；② 否则各仓 `match` 关键词子串；③ 否则 `default`；命中冲突 → Slack 不派。

## 6. 多仓部署（在常开机器上，逐目标仓）

```bash
# 0. 工具仓与各目标仓都 clone 到本机；工具仓的提交需已 push 到 origin
# 1. 把工具集装进每个目标仓，并在其默认分支提交
cd <工具仓>
.claude/ai-workflow/install-into.sh /abs/<repo>
cd /abs/<repo>        # 干净的默认分支(main)上
.claude/ai-workflow/setup.sh           # 验证(go build/docker)、建标签
git add .claude && git commit -m "chore: 接入 AI 工作流工具集"   # 提交到默认分支，worktree 才看得到
# 2. 工具仓里写 config.json：把该目标仓加进 repos（github + 本机 path）
# 3. 给该目标仓注册 GitHub webhook（见第 8 节），都指向同一接收器
```
> base 分支：worktree 检出目标仓默认分支（origin/HEAD）；C 在 Phase 1 再据目标仓 `branch.md`/默认分支定 PR 的 base（不写死）。

## 7. 固定具名隧道 + 服务化（生产形态）

```bash
cloudflared tunnel login
cloudflared tunnel create wf-hook
cloudflared tunnel route dns wf-hook hook.example.com
# ~/.cloudflared/config.yml: tunnel: wf-hook / credentials-file / ingress hostname→http://127.0.0.1:8787
sudo cloudflared service install
```
接收器 systemd（`/etc/systemd/system/wf-receiver.service`）：
```ini
[Unit]
Description=AI workflow webhook receiver
After=network-online.target
[Service]
WorkingDirectory=/abs/工具仓
ExecStart=/usr/bin/python3 /abs/工具仓/.claude/ai-workflow/server.py
Restart=always
RestartSec=3
User=<youruser>
[Install]
WantedBy=multi-user.target
```
```bash
sudo systemctl daemon-reload && sudo systemctl enable --now wf-receiver
```
> macOS 用 launchd（`KeepAlive=true`）同理。URL 固定后，webhook 与 JIRA 规则只注册一次。

> ⚠️ **接收器必须跑在能访问 `claude` 登录态的环境里**（2026-06-18 实测踩坑，务必看）。
> 接收器会以 `claude -p --dangerously-skip-permissions` 在 worktree 跑 headless 任务，而 `claude` 的 OAuth 登录态存在 **macOS keychain / 用户登录会话**里。**launchd/systemd 这类后台服务的运行环境通常够不到登录会话的 keychain** → `claude` 实际「Not logged in」却仍以**退出码 0** 退出 → 接收器误判成功、只发「🏁 结束」却没建出任何 Issue/PR（**静默假成功**）。两个解法：
> - **本地登录会话后台跑接收器（推荐、最省事）**：在有图形登录的会话里
>   `nohup python3 .claude/ai-workflow/server.py >/tmp/wf-recv.log 2>&1 &`，继承登录态即可。
>   代价：登出/重启后需重起（**cloudflared 隧道仍可交给 launchd/systemd 自启，它不碰 claude**）。
> - **配长期 token（要真正服务化时）**：`claude setup-token` 生成 `CLAUDE_CODE_OAUTH_TOKEN`（或用 `ANTHROPIC_API_KEY`），
>   注入接收器进程环境（plist/unit 的环境变量，或 `.env` 后由进程继承），不依赖 keychain。
> 接收器已对「Not logged in / Invalid API key」显式识别并 Slack 告警（不再静默假成功），见排错表。

## 8. 注册 webhook + JIRA 规则

**GitHub（每个目标仓各一个，都指同一接收器）：**
```bash
gh api repos/<org>/<目标仓>/hooks -X POST -f name=web -F active=true \
  -f 'events[]=issues' -f "config[url]=https://hook.example.com/github" \
  -f 'config[content_type]=json' -f "config[secret]=<本机 GITHUB_WEBHOOK_SECRET>"
```
> `setup.sh --serve` 会自动注册/更新**它所在工具仓**的 webhook；其余目标仓用上面命令各注册一次。

**JIRA Automation（共享一条，company-managed 项目需 project-admin）：**
- 入口：项目 → ⚡ → Create rule，或 `https://<domain>/jira/software/c/projects/PROJ/settings/automation`
- Trigger：`字段值已更改` → 字段 `标签`
- Condition：`高级比较条件` `{{issue.labels}}` 包含 `AI处理`
- Action：`发送 web 请求` → URL `https://hook.example.com/jira`，Header `X-Webhook-Token: <本机 JIRA_WEBHOOK_TOKEN>`，Body `{"key":"{{issue.key}}","status":"待AI处理"}`（`status` 写字面值）
- 路由到哪个仓由**票标题**决定（`[channel]` 标记 / 关键词 / 默认）。

## 9. 日常使用（人只碰两处）
1. JIRA 给票打 `AI处理` 标签（标题带 `[channel]` 最稳）→ Slack 收到「Issue 建/重建（待审核）」。
2. 审 Issue（**只编辑标题/主贴**）→ 打 `已审核` → C 跑 → ✅ 已实装+PR / ⚖️ 待裁决。
3. review PR → 合并。

## 10. 安全模型
外网 webhook → 本机 `claude -p --dangerously-skip-permissions` = **RCE 面**。缓解：HMAC/token 验签（未配即 500）；只派两个固定命令 + 参数正则；worktree 隔离 + 运行时注入 `.env`；仅 127.0.0.1 监听；去重防重投。**受控环境、可信网络运行，密钥妥善保管。**

## 11. 排错
| 现象 | 排查 |
|------|------|
| 打标签后无 Slack | 隧道活否（`curl https://hook/health`）；接收器活否；webhook/规则 URL/secret 是否对 |
| GitHub 投递 401 | webhook secret 与本机 `.env` 不一致 |
| JIRA 401 | `X-Webhook-Token` 与本机 `.env` 不一致 |
| 收到但 ignored | JIRA body `status` 非 `待AI处理`，或 Issue 标签非 `已审核` |
| 「未登记仓」忽略 | 该 GitHub 仓未进 `config.json` repos |
| 「无法判定路由」 | 标题命中多仓（冲突）→ 标题加 `[<repo>]` 显式标记 |
| headless 跑了却失败 | worktree 缺工具集（未 commit 到目标仓默认分支）/ 缺 `.env`（接收器注入失败）/ `go build` 环境缺 → 看 Slack ⚠️ |
| C 跑完转「待裁决」 | 正常：未出 PR/未标已实装即裁决；Slack 有原因（需求被阻塞 / 构建测试失败 等） |
| 收到并派发了（Slack 有「启动 B/结束」）却没建 Issue/PR、headless 秒退 | `claude` 在接收器环境**未登录**——后台服务（launchd/systemd）够不到 keychain。改在**图形登录会话内后台跑接收器**，或配 `CLAUDE_CODE_OAUTH_TOKEN`/`ANTHROPIC_API_KEY`（见第 7 节⚠️）。现接收器会对此显式 Slack 告警 |
