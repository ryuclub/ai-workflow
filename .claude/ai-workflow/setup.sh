#!/bin/bash
# AI 自动协作工作流 · 安装脚本
#
#   ./setup.sh                       仅配置（幂等）：前置检查 + 建标签 + 生成密钥/.env + 验证 JIRA/Slack
#   ./setup.sh --serve               配置 + 起接收器&临时隧道 + 注册 GitHub webhook（会武装 RCE 面）
#   PUBLIC_URL=https://h.x.com \
#     ./setup.sh --serve             固定隧道场景：用已有固定 URL，跳过临时隧道，只起接收器+注册 webhook
#
# 只能人工完成的部分（JIRA Automation 规则、填 JIRA/Slack 凭据、具名隧道与服务化）脚本会打印/见手册。
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"   # .claude/ai-workflow
ROOT="$(cd "$HERE/../.." && pwd)"                       # 仓库根
ENV_FILE="$HERE/.env"                                   # 自包含工具集的合并 .env（gitignore）
JIRA_API="$HERE/jira_api.py"
LINEAR_API="$HERE/linear_api.py"
NOTIFY="$HERE/notify.sh"
PORT=8787

# 取/补 .env 中某键（append-if-missing），用于合并 .env 的幂等维护
getenv(){ [ -f "$ENV_FILE" ] && grep "^$1=" "$ENV_FILE" | head -1 | cut -d= -f2- ; }
setdefault(){ grep -q "^$1=" "$ENV_FILE" 2>/dev/null || echo "$1=$2" >> "$ENV_FILE"; }

ok(){   printf '  \033[32m✓\033[0m %s\n' "$*"; }
warn(){ printf '  \033[33m!\033[0m %s\n' "$*"; }
err(){  printf '  \033[31m✗\033[0m %s\n' "$*"; }
step(){ printf '\n\033[1m== %s ==\033[0m\n' "$*"; }

# ── 1. 前置依赖 ──────────────────────────────────────────────
step "1. 前置依赖检查"
MISS=0
for c in python3 git gh cloudflared claude openssl; do
  if command -v "$c" >/dev/null 2>&1; then ok "$c"; else err "缺少 $c"; MISS=1; fi
done
if gh auth status >/dev/null 2>&1; then ok "gh 已登录"; else err "gh 未登录（gh auth login）"; MISS=1; fi
[ "$MISS" = 1 ] && { err "请先补齐缺失依赖再重跑"; exit 1; }
REPO="$(gh repo view --json nameWithOwner -q .nameWithOwner)"
ok "目标仓库：$REPO"

# C（实装）所需 —— 缺只警告（B 不受影响），但安装机应全绿
printf '  \033[2m— C 实装环境 —\033[0m\n'
if command -v go >/dev/null 2>&1; then
  ok "go $(go version | awk '{print $3}')"
  # 权威判定：能构建即证明 GOPRIVATE + 私有依赖拉取（SSH/HTTPS）都通；首次可能稍慢
  if (cd "$ROOT" && go build ./... >/tmp/wf-gobuild.log 2>&1); then
    ok "go build ./... 通过（私有依赖可拉、C 可构建）"
  else
    warn "go build 失败 → C 实装会失败：看 /tmp/wf-gobuild.log（常见 GOPRIVATE / GitHub 私有仓凭据未配）"
  fi
else
  warn "缺 go（C 实装/构建需要）"
fi
if command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then ok "docker 守护进程在（ita 测试）"; else warn "docker 不可用（ita testcontainers 测试会失败）"; fi

# ── 2. GitHub 标签（幂等）────────────────────────────────────
step "2. 创建状态标签"
mklabel(){ gh label create "$1" --color "$2" --description "$3" >/dev/null 2>&1 && ok "建 $1" || ok "$1（已存在）"; }
mklabel "待审核" FBCA04 "AI 整理的需求待人类审核"
mklabel "已审核" 0E8A16 "人类已审核，AI 可开始实装"
mklabel "已实装" 1D76DB "AI 已完成实装并提交 PR，待人类审核合并"
mklabel "待裁决" D93F0B "AI 自动处理遇阻，需人工裁决"

# ── 3. 合并 .env：密钥/默认值（自动生成缺失项）──────────────
step "3. 工具集 .env（密钥/默认值）"
touch "$ENV_FILE"
setdefault GITHUB_WEBHOOK_SECRET "$(openssl rand -hex 32)"
setdefault JIRA_WEBHOOK_TOKEN    "$(openssl rand -hex 32)"
setdefault JIRA_TRIGGER_STATUS   "待AI处理"
setdefault LINEAR_WEBHOOK_SECRET "$(openssl rand -hex 32)"
setdefault LINEAR_TRIGGER_LABEL  "AI处理"
setdefault APPROVED_LABEL        "已审核"
setdefault PORT                  "$PORT"
ok "$ENV_FILE 就绪（缺失的密钥已生成）"
GH_SECRET="$(getenv GITHUB_WEBHOOK_SECRET)"
JIRA_TOKEN="$(getenv JIRA_WEBHOOK_TOKEN)"
LINEAR_SECRET="$(getenv LINEAR_WEBHOOK_SECRET)"
LINEAR_LABEL="$(getenv LINEAR_TRIGGER_LABEL)"

# ── 4. Slack ────────────────────────────────────────────────
step "4. Slack 通知"
if [ -n "$(getenv SLACK_WEBHOOK_URL)" ]; then
  ok "SLACK_WEBHOOK_URL 已配"
  if bash "$NOTIFY" "🔧 setup.sh 自检：Slack 通道可用" >/dev/null 2>&1; then ok "Slack 发送成功"; else warn "Slack 发送失败，检查 webhook URL"; fi
else
  warn "缺 SLACK_WEBHOOK_URL —— 加到 ${ENV_FILE}："
  echo "      SLACK_WEBHOOK_URL=https://hooks.slack.com/services/XXX/YYY/ZZZ"
fi

# ── 5. JIRA 凭据 ────────────────────────────────────────────
step "5. JIRA 凭据"
JIRA_SRC="${JIRA_ENV_SRC:-$HOME/work/MosaviJP/Mosavi-Channel-Service/.claude/config/claude.env}"
if [ -z "$(getenv ATLASSIAN_API_KEY)" ] && [ -f "$JIRA_SRC" ]; then
  grep -hE '^(ATLASSIAN_|JIRA_PROJECT)' "$JIRA_SRC" >> "$ENV_FILE" && ok "从 $JIRA_SRC 合入 JIRA 凭据"
fi
if [ -n "$(getenv ATLASSIAN_API_KEY)" ]; then
  if python3 "$JIRA_API" get MOS-3245 2>/dev/null | grep -q '"key"'; then ok "JIRA 读取验证通过"; else warn "JIRA .env 在但读取失败，检查凭据/网络"; fi
else
  warn "缺 ATLASSIAN_* —— 加到 ${ENV_FILE}：ATLASSIAN_USERNAME/API_KEY/DOMAIN、JIRA_PROJECT=MOS"
fi

# ── 5b. Linear 凭据（可选入口）──────────────────────────────
step "5b. Linear 凭据（可选）"
if [ -n "$(getenv LINEAR_API_KEY)" ]; then
  # viewer 查询验证 key 有效（不依赖具体工单号）
  if curl -s -X POST https://api.linear.app/graphql \
       -H "Authorization: $(getenv LINEAR_API_KEY)" -H "Content-Type: application/json" \
       -d '{"query":"{ viewer { id } }"}' 2>/dev/null | grep -q '"id"'; then
    ok "Linear 读取验证通过"
  else
    warn "LINEAR_API_KEY 在但验证失败，检查凭据/网络"
  fi
else
  warn "缺 LINEAR_API_KEY（不接 Linear 可忽略）—— 加到 ${ENV_FILE}：LINEAR_API_KEY=lin_api_xxx"
fi

# ── 6. 配置完成 ─────────────────────────────────────────────
if [ "${1:-}" != "--serve" ]; then
  step "配置完成（未起服务）"
  echo "  下一步起服务 + 注册 webhook：./setup.sh --serve"
  echo "  JIRA Automation 规则仍需手动建（见 .claude/ai-workflow/docs/ai-workflow-setup.md 第 8 节）"
  exit 0
fi

# ── 7. 起接收器 + 隧道 + 注册 webhook（--serve）─────────────
step "7. 启动服务（⚠️ 武装外网→本机 RCE 面）"
pkill -f "ai-workflow/server.py" 2>/dev/null; sleep 1
nohup python3 "$HERE/server.py" >/tmp/wf-recv.log 2>&1 &
sleep 2
curl -s "http://127.0.0.1:$PORT/health" | grep -q ok && ok "接收器活（:${PORT}）" || { err "接收器没起，看 /tmp/wf-recv.log"; exit 1; }

if [ -n "${PUBLIC_URL:-}" ]; then
  URL="${PUBLIC_URL%/}"
  ok "用固定隧道 URL：${URL}（cloudflared 由外部服务托管，不在此启动）"
else
  pkill -f "cloudflared tunnel" 2>/dev/null; sleep 1
  nohup cloudflared tunnel --url "http://127.0.0.1:$PORT" >/tmp/wf-cf.log 2>&1 &
  URL=""
  for i in $(seq 1 20); do
    URL="$(grep -oE 'https://[a-z0-9-]+\.trycloudflare\.com' /tmp/wf-cf.log | head -1)"
    [ -n "$URL" ] && break; sleep 1
  done
  [ -z "$URL" ] && { err "隧道 URL 没拿到，看 /tmp/wf-cf.log"; exit 1; }
  ok "临时隧道：$URL"
fi

step "8. 注册 GitHub webhook"
HOOK_ID="$(gh api "repos/$REPO/hooks" --jq '.[] | select(.config.url|endswith("/github")) | .id' 2>/dev/null | head -1)"
if [ -n "$HOOK_ID" ]; then
  gh api "repos/$REPO/hooks/$HOOK_ID" -X PATCH -f "config[url]=$URL/github" -f "config[secret]=$GH_SECRET" >/dev/null && ok "更新 webhook（id=${HOOK_ID}）→ $URL/github"
else
  gh api "repos/$REPO/hooks" -X POST -f name=web -F active=true \
    -f "events[]=issues" -f "config[url]=$URL/github" \
    -f "config[content_type]=json" -f "config[secret]=$GH_SECRET" \
    --jq '"  新建 webhook id=\(.id)"' && ok "已注册"
fi

# ── 9. 打印只能人工完成的部分 ───────────────────────────────
step "✅ 自动部分完成 —— 以下需你在 JIRA / Linear 手动建规则"
cat <<EOF
  JIRA Automation（项目 MOS → ⚡ → Create rule）：
    Trigger   : 字段值已更改 → 字段 标签
    Condition : 高级比较条件  {{issue.labels}} 包含 AI处理
    Action    : 发送 web 请求
      URL    : $URL/jira
      Header : X-Webhook-Token: $JIRA_TOKEN
      Body   : {"key":"{{issue.key}}","status":"待AI处理"}
    保存并打开规则。

  Linear Webhook（Settings → API → Webhooks → New webhook）：
    URL          : $URL/linear
    Signing secret: $LINEAR_SECRET   （填到本机 .env 的 LINEAR_WEBHOOK_SECRET，setup 已生成同串）
    Resources    : 勾选 Issues
    触发方式      : 给 issue 打标签「$LINEAR_LABEL」即触发（接收器回查 API 确认标签）
    注：Linear 验签头为 Linear-Signature（HMAC-SHA256 十六进制，无前缀）。

  ⚠️ 隧道 URL 是临时的，重启 cloudflared 会变，变了要更新上面 JIRA / Linear 规则的 URL
     （GitHub webhook 重跑 ./setup.sh --serve 会自动更新）。
EOF
