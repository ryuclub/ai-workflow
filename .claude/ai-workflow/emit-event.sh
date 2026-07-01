#!/usr/bin/env bash
# 结构化阶段事件上报：B/C skill 在阶段边界调用，把进度 POST 回控制面后端。
# 后端据此点亮流水线节点并经 SSE 推给 Dashboard、经 sink 转 Slack。
#
# 用法：
#   emit-event.sh <phase> <status> [message]
#     <phase>  阶段标识，须与后端 pipeline 的 phaseKeys 对齐：
#              B.start | B.investigate | B.issue | B.done
#              C.start | C.blueprint | C.implement | C.test | C.pr | C.done
#     <status> start | ok | fail | info
#   可选携带产物（经环境变量）：
#     WF_ISSUE_NUM / WF_ISSUE_URL  —— B 建好 Issue 时随 B.done 上报
#     WF_PR_URL                    —— C 出 PR 时随 C.pr/C.done 上报
#
# 读环境：WF_TASK_ID / WF_EVENT_URL / WF_INTERNAL_TOKEN（由 runner 注入）。
# 非编排环境（人手直接跑 skill）下这些为空 —— 静默跳过，不报错。

[ -z "$WF_TASK_ID" ] || [ -z "$WF_EVENT_URL" ] && exit 0

phase="$1"
status="${2:-info}"
message="${3:-}"

payload=$(python3 - "$phase" "$status" "$message" "${WF_ISSUE_NUM:-}" "${WF_ISSUE_URL:-}" "${WF_PR_URL:-}" <<'PY'
import json, sys
phase, status, msg, inum, iurl, pr = sys.argv[1:7]
d = {"phase": phase, "status": status, "message": msg}
if inum:
    try:
        d["issue_num"] = int(inum)
    except ValueError:
        pass
if iurl:
    d["issue_url"] = iurl
if pr:
    d["pr_url"] = pr
print(json.dumps(d, ensure_ascii=False))
PY
)

curl -s -X POST "$WF_EVENT_URL" \
  -H "Content-Type: application/json" \
  -H "X-Internal-Token: ${WF_INTERNAL_TOKEN:-}" \
  -d "$payload" >/dev/null 2>&1 || true
