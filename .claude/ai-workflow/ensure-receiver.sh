#!/bin/bash
# webhook 接收器进程管理（幂等）。
#   .claude/ai-workflow/ensure-receiver.sh [start|stop|restart]
#     start    （默认）已健康在跑→报告即可；没跑/僵死→清理并后台起（幂等）
#     stop     停止接收器（没跑则报告无需操作）
#     restart  不看过去状态，先停再起（清掉旧进程 → 全新拉起）
#
# ⚠️ start/restart 必须在能访问 claude 登录态(keychain)的【图形登录会话】里执行；
#    否则 headless claude 会「Not logged in」却退出 0（假成功，不建 Issue/PR）。详见 docs 第 7 节。
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"   # .claude/ai-workflow
LOG="${WF_RECV_LOG:-/tmp/wf-recv.log}"
PORT="$(grep -E '^PORT=' "$HERE/.env" 2>/dev/null | head -1 | cut -d= -f2-)"
PORT="${PORT:-8787}"
CMD="${1:-start}"

pidof_recv(){ pgrep -f 'ai-workflow/server.py' | head -1; }
health(){ curl -s -m 3 "http://127.0.0.1:$PORT/health" 2>/dev/null | grep -q ok; }

do_stop(){
  if ! pidof_recv >/dev/null; then
    echo "ℹ 接收器未在运行，无需停止。"
    return 0
  fi
  echo "ℹ 正在停止接收器（pid=$(pidof_recv)）…"
  pkill -f 'ai-workflow/server.py' 2>/dev/null || true
  for _ in $(seq 1 10); do pidof_recv >/dev/null || break; sleep 0.3; done
  if pidof_recv >/dev/null; then
    echo "ℹ 进程未退，强制 kill -9…"
    pkill -9 -f 'ai-workflow/server.py' 2>/dev/null || true
    sleep 0.5
  fi
  pidof_recv >/dev/null && { echo "✗ 接收器仍未停止"; return 1; }
  echo "✓ 接收器已停止。"
}

do_launch(){
  echo "ℹ 后台启动接收器…"
  nohup python3 "$HERE/server.py" >"$LOG" 2>&1 & disown
  if curl -s --retry 10 --retry-delay 1 --retry-all-errors -m 3 "http://127.0.0.1:$PORT/health" 2>/dev/null | grep -q ok; then
    echo "✓ 接收器已启动（:$PORT, pid=$(pidof_recv)）。日志：$LOG"
  else
    echo "✗ 接收器启动失败，看日志：$LOG"
    tail -8 "$LOG" 2>/dev/null
    return 1
  fi
}

do_start(){
  # 1) 已健康在跑 → 报告即可（幂等）
  if health; then
    echo "✓ 接收器已在运行（:$PORT, pid=$(pidof_recv)），无需启动。日志：$LOG"
    return 0
  fi
  # 2) 端口无响应却有残留进程 → 清掉再起
  if pidof_recv >/dev/null; then
    echo "ℹ 发现僵死接收器进程，先清理…"
    do_stop || return 1
  fi
  # 3) 后台启动（继承本登录会话环境 → claude 可用 keychain）
  do_launch
}

case "$CMD" in
  start)   do_start ;;
  stop)    do_stop ;;
  restart) echo "ℹ 重启接收器（不看过去状态，先停再起）…"; do_stop && do_launch ;;
  *) echo "用法: ensure-receiver.sh [start|stop|restart]"; exit 2 ;;
esac
