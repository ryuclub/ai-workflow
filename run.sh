#!/usr/bin/env bash
# 本地启动脚本（幂等）：缺什么补什么，然后起控制面后端。
#   ./run.sh              生产式：构建前端 → 后端托管 SPA（默认 127.0.0.1:8788）
#   ./run.sh --dev        开发式：只起后端，前端请另开 `cd frontend && npm run dev`
#   ./run.sh --build-only 只做前置（装依赖 + 构建前端），不启动
#   ./run.sh --rebuild    强制重装/重构前端
#
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
    -h|--help)    sed -n '2,9p' "$0"; exit 0 ;;
    *) echo "未知参数：$arg（--dev / --build-only / --rebuild / --help）" >&2; exit 2 ;;
  esac
done

info() { printf '\033[36m▶ %s\033[0m\n' "$1"; }
warn() { printf '\033[33m⚠ %s\033[0m\n' "$1"; }

# ── 0. 依赖自检 ────────────────────────────────────────────
command -v go   >/dev/null || { echo "缺 go（需 1.26+）" >&2; exit 1; }
command -v node >/dev/null || { echo "缺 node（需 20+）" >&2; exit 1; }
command -v npm  >/dev/null || { echo "缺 npm" >&2; exit 1; }
command -v claude >/dev/null || warn "PATH 未见 claude CLI —— headless 会「未登录假成功」，务必在图形登录会话内跑"
command -v gh     >/dev/null || warn "PATH 未见 gh CLI —— 私有仓克隆/Issue 操作会失败"

# ── 1. 配置落地（仅缺失时生成）──────────────────────────────
if [ ! -f config.json ]; then
  cp config.example.json config.json
  warn "已生成 config.json（模板），请登记目标仓 + 选 source 后再起真实任务"
fi

ENV_FILE=".claude/ai-workflow/.env"
if [ ! -f "$ENV_FILE" ]; then
  cp "$ENV_FILE.example" "$ENV_FILE"
  # 自动补一个随机 INTERNAL_TOKEN，省去手动生成
  if command -v openssl >/dev/null; then
    TOKEN="$(openssl rand -hex 32)"
    # 仅替换空值行 INTERNAL_TOKEN=
    /usr/bin/sed -i '' "s/^INTERNAL_TOKEN=$/INTERNAL_TOKEN=$TOKEN/" "$ENV_FILE"
    info "已生成 $ENV_FILE 并填入随机 INTERNAL_TOKEN"
  fi
  warn "请补齐 $ENV_FILE 的票源凭据（JIRA 或 Linear）后再起真实任务"
fi

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
if [ "$MODE" = "dev" ]; then
  info "开发模式：后端已起，另开一终端跑  cd frontend && npm run dev  （前端 :5173，/api 代理到后端）"
else
  info "打开 Dashboard → http://127.0.0.1:$PORT"
fi
exec go run ./cmd/server
