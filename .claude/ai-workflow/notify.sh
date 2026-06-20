#!/bin/bash
# 向 Slack 发送一条通知。webhook 从同目录 .env 的 SLACK_WEBHOOK_URL 读取（gitignore 覆盖）。
# 用法: notify.sh "<消息文本>"
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
if [ -f "$DIR/.env" ]; then
  set -a; . "$DIR/.env"; set +a
fi

MSG="${1:-}"
[ -z "${SLACK_WEBHOOK_URL:-}" ] && { echo "错误: SLACK_WEBHOOK_URL 未配置" >&2; exit 1; }
[ -z "$MSG" ] && { echo "用法: notify.sh <消息文本>" >&2; exit 1; }

# 用 python 安全转义为 JSON 字符串，避免引号/换行破坏 payload
payload=$(printf '%s' "$MSG" | python3 -c 'import json,sys; print(json.dumps({"text": sys.stdin.read()}))')

curl -sS -X POST -H 'Content-type: application/json' --data "$payload" "$SLACK_WEBHOOK_URL"
echo
