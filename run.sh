#!/usr/bin/env bash
# 本地启动脚本（幂等）：缺什么补什么，然后起多租户控制面后端。
#   ./run.sh              生产式：构建前端 → 后端托管 SPA（默认 127.0.0.1:8788）
#   ./run.sh --dev        开发式：只起后端，前端请另开 `cd frontend && npm run dev`
#   ./run.sh --build-only 只做前置（装依赖 + 构建前端），不启动
#   ./run.sh --rebuild    强制重装/重构前端
#
# 多租户：首次启动会用 WF_BOOTSTRAP_* 建首个租户+管理员，并把现有 config.json/.env 迁进该租户。
# 凭据加密需 MASTER_KEY（本脚本自动在 .env 生成并持久化，勿更换——换了旧凭据解不开）。
# 注意：config.json / .env 只在「缺失」时从 example 生成，绝不覆盖已有（含密钥/机器相关）。
set -euo pipefail
cd "$(dirname "$0")"

MODE="prod"
REBUILD=0
for arg in "$@"; do
  case "$arg" in
    --dev)        MODE="dev" ;;
    --build-only) MODE="build" ;;
    --rebuild)    REBUILD=1 ;;
    -h|--help)    sed -n '2,10p' "$0"; exit 0 ;;
    *) echo "未知参数：$arg（--dev / --build-only / --rebuild / --help）" >&2; exit 2 ;;
  esac
done

info() { printf '\033[36m▶ %s\033[0m\n' "$1"; }
warn() { printf '\033[33m⚠ %s\033[0m\n' "$1"; }

# ── 0. 依赖自检 ────────────────────────────────────────────
command -v go   >/dev/null || { echo "缺 go（需 1.26+）" >&2; exit 1; }
command -v node >/dev/null || { echo "缺 node（需 20+）" >&2; exit 1; }
command -v npm  >/dev/null || { echo "缺 npm" >&2; exit 1; }
command -v claude >/dev/null || warn "PATH 未见 claude CLI —— 未配租户/个人令牌时会回落宿主登录态，须在能访问其登录态的会话内跑"
command -v gh     >/dev/null || warn "PATH 未见 gh CLI —— 私有仓克隆/Issue 操作会失败"

# ── 1. 配置落地（仅缺失时生成）──────────────────────────────
if [ ! -f config.json ]; then
  cp config.example.json config.json
  warn "已生成 config.json（模板）；首个租户将继承它，登记目标仓 + 选 source 后再起真实任务"
fi

ENV_FILE=".claude/ai-workflow/.env"
if [ ! -f "$ENV_FILE" ]; then
  cp "$ENV_FILE.example" "$ENV_FILE"
  warn "已生成 $ENV_FILE，请补齐票源凭据（JIRA 或 Linear）后再起真实任务"
fi

# 幂等补一个非空的键值：$1=键名 $2=生成的值（仅在缺失或为空时写入）
ensure_secret() {
  local key="$1" val="$2"
  if grep -qE "^${key}=.+" "$ENV_FILE"; then return 0; fi
  if grep -qE "^${key}=$" "$ENV_FILE"; then
    /usr/bin/sed -i '' "s|^${key}=$|${key}=${val}|" "$ENV_FILE"
  else
    printf '\n%s=%s\n' "$key" "$val" >> "$ENV_FILE"
  fi
  info "已生成并写入 $key（$ENV_FILE）"
}

if command -v openssl >/dev/null; then
  ensure_secret INTERNAL_TOKEN "$(openssl rand -hex 32)"
  # MASTER_KEY：多租户凭据加密主密钥，生成后勿更换
  ensure_secret MASTER_KEY "$(openssl rand -hex 32)"
else
  warn "未找到 openssl —— 无法自动生成 INTERNAL_TOKEN / MASTER_KEY，请手动补齐 $ENV_FILE"
fi

# 导出 MASTER_KEY 到进程环境（main 经 os.Getenv 读取，仅写 .env 不够）
export MASTER_KEY="$(grep -E '^MASTER_KEY=' "$ENV_FILE" 2>/dev/null | tail -1 | cut -d= -f2-)"

# 首个管理员（仅在数据库无用户时生效；可用环境变量覆盖）
export WF_BOOTSTRAP_EMAIL="${WF_BOOTSTRAP_EMAIL:-admin@local.dev}"
export WF_BOOTSTRAP_PASSWORD="${WF_BOOTSTRAP_PASSWORD:-admin123}"
export WF_BOOTSTRAP_TENANT="${WF_BOOTSTRAP_TENANT:-默认租户}"

# 读 PORT（.env 里的 PORT=，默认 8788），仅用于打印提示
PORT="$(grep -E '^PORT=' "$ENV_FILE" 2>/dev/null | tail -1 | cut -d= -f2)"
PORT="${PORT:-8788}"

# ── 2. 前端依赖 + 构建 ─────────────────────────────────────
if [ "$MODE" != "dev" ]; then
  if [ "$REBUILD" = 1 ] || [ ! -d frontend/node_modules ]; then
    info "安装前端依赖…"; ( cd frontend && npm install )
  fi
  if [ "$REBUILD" = 1 ] || [ ! -d frontend/dist ]; then
    info "构建前端…"; ( cd frontend && npm run build )
  fi
fi

[ "$MODE" = "build" ] && { info "前置完成（--build-only）"; exit 0; }

# ── 3. 启动后端 ────────────────────────────────────────────
info "登录：$WF_BOOTSTRAP_EMAIL / $WF_BOOTSTRAP_PASSWORD（首次启动创建；非本地务必用环境变量覆盖）"
if [ "$MODE" = "dev" ]; then
  info "开发模式：后端已起，另开一终端跑  cd frontend && npm run dev  （前端 :5173，/api 代理到后端）"
else
  info "打开 Dashboard → http://127.0.0.1:$PORT"
fi
exec go run ./cmd/server
