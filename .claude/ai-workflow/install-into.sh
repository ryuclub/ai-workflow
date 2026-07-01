#!/bin/bash
# 把 AI 工作流工具集安装进【目标项目仓】，让该仓获得 AI 处理能力。
#   ./install-into.sh /path/to/target-repo
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"   # 源 .claude/ai-workflow
SRC_ROOT="$(cd "$HERE/../.." && pwd)"                   # 源仓库根
TARGET="${1:-}"
[ -z "$TARGET" ] && { echo "用法: install-into.sh <目标仓库根路径>"; exit 1; }
TARGET="$(cd "$TARGET" && pwd)" || exit 1
[ -d "$TARGET/.git" ] || { echo "✗ $TARGET 不是 git 仓库根"; exit 1; }
[ "$TARGET" = "$SRC_ROOT" ] && { echo "✗ 目标不能是源仓本身"; exit 1; }

echo "源: $SRC_ROOT"
echo "目标: $TARGET"

# 安装单元：入口 skill/命令 + skill 运行期所需工具（jira_api.py / emit-event.sh）。
# 触发与编排由【控制面 Go 后端】负责，故不再拷 webhook 接收器（server.py 等已退役）。
mkdir -p "$TARGET/.claude/skills" "$TARGET/.claude/commands" "$TARGET/.claude/ai-workflow"
cp -R "$SRC_ROOT/.claude/skills/jira-to-issue"     "$TARGET/.claude/skills/"
cp    "$SRC_ROOT/.claude/commands/issue-to-pr.md"  "$TARGET/.claude/commands/"
# ai-workflow：仅拷 skill 运行需要的工具，**不拷 .env**（凭据由控制面 runner 注入）
for f in jira_api.py emit-event.sh notify.sh .env.example README.md; do
  cp "$HERE/$f" "$TARGET/.claude/ai-workflow/"
done

# 确保目标仓 .claude/.gitignore 覆盖 .env 和 __pycache__
GI="$TARGET/.claude/.gitignore"; touch "$GI"
grep -qxF ".env" "$GI" 2>/dev/null || echo ".env" >> "$GI"
grep -qxF "__pycache__/" "$GI" 2>/dev/null || echo "__pycache__/" >> "$GI"

cat <<EOF

✅ 已安装到 $TARGET/.claude/{skills/jira-to-issue, commands/issue-to-pr.md, ai-workflow/}

目标仓后续步骤：
  cd "$TARGET"
  git add .claude && git commit -m "chore: 接入 AI 工作流工具集"   # 必须提交：headless 在 worktree 跑，要能看到
  # 然后在【控制面仓】的 config.json 的 repos 里登记本仓（github / 本机绝对路径 / 标题关键词），
  # 由控制面 Dashboard 发起任务即可。无需在目标仓起任何服务。
EOF
