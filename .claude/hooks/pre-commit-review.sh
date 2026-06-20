#!/bin/bash
# 提交前审查强制钩子
# 检测到 git commit 命令时，强制显示审查基准文档

INPUT=$(cat)
COMMAND=$(echo "$INPUT" | grep -o '"command":"[^"]*"' | head -1 | sed 's/"command":"//;s/"$//')

if echo "$COMMAND" | grep -qE "git commit"; then
  REVIEW_DOC=".claude/guidelines/pre-commit-review.md"
  echo ""
  echo "=========================================="
  echo " 提交前审查检查"
  echo "=========================================="
  echo ""
  if [ -f "$REVIEW_DOC" ]; then
    cat "$REVIEW_DOC"
  else
    echo "[警告] 审查基准文档未找到: $REVIEW_DOC"
  fi
  echo ""
  echo "请确认已完成上述全部检查项。"
  echo "=========================================="
fi
