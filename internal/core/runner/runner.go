// Package runner 负责在目标仓 worktree 内跑 claude -p（B/C）。
// P1 仅定义接口与占位实现；真实的 worktree + claude 执行在 P2 落地。
package runner

import (
	"context"

	"github.com/ryuclub/ai-workflow/internal/core/store"
)

// Runner 执行流水线的自动工作：B（jira-to-issue）、C（issue-to-pr）、D（pr-revise）。
// 阶段内的细粒度进度由 skill 经 /internal 事件回传，runner 只负责拉起与等待终态。
type Runner interface {
	// RunB 跑 jira-to-issue，产物为 GitHub Issue（待审核）。
	RunB(ctx context.Context, t *store.Task) error
	// RunC 跑 issue-to-pr，产物为 Draft PR + Issue（已实装）。
	RunC(ctx context.Context, t *store.Task) error
	// RunD 跑 pr-revise，按 PR review 意见改代码并 push（可多轮）。
	RunD(ctx context.Context, t *store.Task) error
}

// Noop 是 P1 占位实现：不真正跑 claude，供骨架联调与读路径验证。
type Noop struct{}

func (Noop) RunB(ctx context.Context, t *store.Task) error { return nil }
func (Noop) RunC(ctx context.Context, t *store.Task) error { return nil }
func (Noop) RunD(ctx context.Context, t *store.Task) error { return nil }
