#!/bin/bash
# 把【全套 AI 工作流工具集】安装进目标项目仓，让该仓获得 AI 处理能力。
#   ./install-into.sh /path/to/target-repo
# 非破坏性：已存在的 settings.json 不覆盖，只提示需手动合入的 hook/权限。
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"   # 源 .claude/ai-workflow
SRC=".claude"                                           # 源仓 .claude 子目录名
SRC_ROOT="$(cd "$HERE/../.." && pwd)"                   # 源仓库根
TARGET="${1:-}"
[ -z "$TARGET" ] && { echo "用法: install-into.sh <目标仓库根路径>"; exit 1; }
TARGET="$(cd "$TARGET" && pwd)" || exit 1
[ -d "$TARGET/.git" ] || { echo "✗ $TARGET 不是 git 仓库根"; exit 1; }
[ "$TARGET" = "$SRC_ROOT" ] && { echo "✗ 目标不能是源仓本身"; exit 1; }

echo "源:   $SRC_ROOT"
echo "目标: $TARGET"
echo

TC="$TARGET/.claude"
mkdir -p "$TC/skills" "$TC/commands" "$TC/guidelines" "$TC/hooks" "$TC/ai-workflow"

# 1) 入口 skills（引擎核心）。Go/Gin 专属 skill（gin-api-docs）默认跳过，WITH_GO_SKILLS=1 才装。
for s in "$SRC_ROOT/$SRC/skills"/*/; do
  name="$(basename "$s")"
  if [ "$name" = "gin-api-docs" ] && [ "${WITH_GO_SKILLS:-0}" != "1" ]; then
    echo "  ⊘ 跳过 Go 专属 skill: gin-api-docs（WITH_GO_SKILLS=1 可装）"; continue
  fi
  # 去掉 glob 的尾斜杠：`cp -R dir/ dest/` 拷的是内容，会把 skill 铺平进 skills/；
  # `cp -R dir dest/` 才会保留 skill 目录本身。
  cp -R "${s%/}" "$TC/skills/"
done
echo "✓ skills/        $(ls "$TC/skills" | tr '\n' ' ')"

# 2) 编排命令（不覆盖目标仓已有同名）
cp -Rn "$SRC_ROOT/$SRC/commands/." "$TC/commands/" 2>/dev/null
echo "✓ commands/      $(ls "$TC/commands" | wc -l | tr -d ' ') 个（已有同名保留不覆盖）"

# 3) 规约：**不覆盖**目标仓已有同名（其自有 guidelines 优先；仅补缺失的 Mosavi/Go 默认）
cp -Rn "$SRC_ROOT/$SRC/guidelines/." "$TC/guidelines/" 2>/dev/null
echo "✓ guidelines/    （目标仓已有同名保留不覆盖；新增的为 Mosavi/Go 默认，按本项目栈裁剪/删除）"

# 4) 提交前审查钩子
cp "$SRC_ROOT/$SRC/hooks/pre-commit-review.sh" "$TC/hooks/"
echo "✓ hooks/pre-commit-review.sh"

# 5) 引擎：拷脚本/文档/示例，**不拷 .env / config.json**（密钥与本机配置不外带）
for f in jira_api.py notify.sh server.py setup.sh ensure-receiver.sh install-into.sh .env.example config.example.json README.md; do
  cp "$HERE/$f" "$TC/ai-workflow/"
done
cp -R "$HERE/docs" "$TC/ai-workflow/"
echo "✓ ai-workflow/   引擎脚本 + docs（无密钥）"

# 6) settings.json：存在则不覆盖，仅提示
if [ -f "$TC/settings.json" ]; then
  echo "⚠ 目标已有 .claude/settings.json — 未覆盖。请确认其 hooks 含："
  echo '    PreToolUse[Bash] → command: bash .claude/hooks/pre-commit-review.sh'
else
  cp "$SRC_ROOT/$SRC/settings.json" "$TC/settings.json"
  echo "✓ settings.json  （权限 + pre-commit-review 钩子）"
fi

# 7) 确保目标仓 .claude/.gitignore 覆盖密钥与运行时产物
GI="$TC/.gitignore"; touch "$GI"
for line in ".env" "__pycache__/" "ai-workflow/config.json"; do
  grep -qxF "$line" "$GI" 2>/dev/null || echo "$line" >> "$GI"
done
echo "✓ .claude/.gitignore"

cat <<EOF

✅ 全套件已安装到 $TARGET/.claude/

目标仓后续步骤：
  cd "$TARGET"
  .claude/ai-workflow/setup.sh           # 填/生成 .env、建标签、验证（含 go build/docker 检测）
  git add .claude && git commit -m "chore: 接入 AI 工作流工具集"   # 必须提交：headless 在 worktree 跑，要能看到
  .claude/ai-workflow/setup.sh --serve   # 起接收器 + 注册 GitHub webhook（或 PUBLIC_URL=… 用固定隧道）
  # 再在 JIRA 建 Automation 规则（条件含本仓的 repo: 标签），见 docs/ai-workflow-setup.md
EOF
