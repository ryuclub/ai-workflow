// Package orchestrator 是流水线状态机：B → 等人审 → C；失败 → 待裁决。
// 它编排粗粒度状态流转；阶段内进度由 skill 经事件回传，节点状态在 api/ingest 落更。
package orchestrator

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ryuclub/ai-workflow/internal/core/config"
	"github.com/ryuclub/ai-workflow/internal/core/events"
	"github.com/ryuclub/ai-workflow/internal/core/github"
	"github.com/ryuclub/ai-workflow/internal/core/pipeline"
	"github.com/ryuclub/ai-workflow/internal/core/runner"
	"github.com/ryuclub/ai-workflow/internal/core/source"
	"github.com/ryuclub/ai-workflow/internal/core/store"
	"github.com/google/uuid"
)

// 标签状态机（GitHub Issue）。
const (
	LabelPending  = "待审核"
	LabelApproved = "已审核"
	LabelDone     = "已实装"
	LabelAdjudic  = "待裁决"
)

// maxReviewRounds 是修订轮次上限：超出仍未通过 → 待裁决（防 AI 改-审死循环）。
// 轮询周期见 config.PRPollInterval()（默认 120s，PR_POLL_INTERVAL 可调）。
const maxReviewRounds = 5

// Deps 提供随设置热重载而变的依赖（配置 / 票源 / GitHub 客户端）。
type Deps interface {
	Config() *config.Config
	Provider() source.Provider
	Github() *github.Client
}

// Orchestrator 持有依赖并驱动任务状态机。
type Orchestrator struct {
	deps   Deps
	st     store.Store
	bus    *events.Bus
	pl     pipeline.Pipeline
	runner runner.Runner

	sem      chan struct{} // 并发闸：限制同时跑 claude 的任务数
	mu       sync.Mutex
	cancels  map[string]context.CancelFunc // 运行中任务的取消句柄
	canceled map[string]bool               // 已请求取消的任务
}

// New 构造编排器。deps 在每次取用时返回当前生效的配置/票源/客户端。
func New(deps Deps, st store.Store, bus *events.Bus, r runner.Runner) *Orchestrator {
	return &Orchestrator{
		deps: deps, st: st, bus: bus, pl: pipeline.Default(), runner: r,
		sem:     make(chan struct{}, deps.Config().MaxConcurrent()),
		cancels: map[string]context.CancelFunc{}, canceled: map[string]bool{},
	}
}

// acquire 取并发槽；排队期间若任务被取消（ctx done）则返回 false。
func (o *Orchestrator) acquire(ctx context.Context) bool {
	select {
	case o.sem <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

func (o *Orchestrator) release() { <-o.sem }

// register 为任务建一个可取消上下文并登记取消句柄。
func (o *Orchestrator) register(id string) context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	o.mu.Lock()
	o.cancels[id] = cancel
	o.mu.Unlock()
	return ctx
}

func (o *Orchestrator) unregister(id string) {
	o.mu.Lock()
	delete(o.cancels, id)
	o.mu.Unlock()
}

func (o *Orchestrator) isCanceled(id string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.canceled[id]
}

// Cancel 取消任务：杀掉正在跑的 claude 子进程并置 canceled 态。
func (o *Orchestrator) Cancel(taskID string) error {
	t, err := o.st.GetTask(taskID)
	if err != nil || t == nil {
		return fmt.Errorf("任务不存在: %s", taskID)
	}
	if t.State.Terminal() {
		return fmt.Errorf("任务已结束（%s），不可取消", t.State)
	}
	o.mu.Lock()
	o.canceled[taskID] = true
	cancel := o.cancels[taskID]
	o.mu.Unlock()
	if cancel != nil {
		cancel() // 终止运行中的 claude（context 取消 → exec 被杀）
	}
	t.Error = "用户取消"
	o.setState(t, store.StateCanceled)
	o.publish(taskID, "", "task.canceled", "warn", "任务已取消")
	return nil
}

// Pipeline 返回内置流水线定义（供 API 输出给前端画图）。
func (o *Orchestrator) Pipeline() pipeline.Pipeline { return o.pl }

// StartTask 校验入参、做幂等、创建任务并异步启动 B。sourceID 为活跃源的工单标识，title 为票标题快照。
func (o *Orchestrator) StartTask(tenantID, createdBy, sourceID, repo, title, idem string) (*store.Task, error) {
	src := o.deps.Provider()
	if !src.ValidateID(sourceID) {
		return nil, fmt.Errorf("非法 %s 标识: %q", src.Name(), sourceID)
	}
	if _, ok := o.deps.Config().Repos[repo]; !ok {
		return nil, fmt.Errorf("未登记的仓: %q", repo)
	}
	if idem != "" {
		if existing, err := o.st.FindByIdem(idem); err == nil && existing != nil {
			return existing, nil // 幂等：返回既有任务，不重复起活
		}
	}
	now := time.Now()
	t := &store.Task{
		ID:         uuid.NewString(),
		TenantID:   tenantID,
		CreatedBy:  createdBy,
		Source:     src.Name(),
		SourceID:   sourceID,
		Title:      title,
		Repo:       repo,
		PipelineID: o.pl.ID,
		State:      store.StateQueued,
		IdemKey:    idem,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := o.st.CreateTask(t); err != nil {
		return nil, err
	}
	// 初始化所有节点为 pending
	for _, id := range o.pl.NodeIDs() {
		_ = o.st.UpsertNodeRun(&store.NodeRun{TaskID: t.ID, NodeID: id, State: store.NodePending})
	}
	o.publish(t.ID, "", "task.created", "info", fmt.Sprintf("任务创建：%s/%s → %s", t.Source, sourceID, repo))
	go o.runB(o.register(t.ID), t)
	return t, nil
}

// runB 跑调查→建 Issue，完成后停在人审闸口。
func (o *Orchestrator) runB(ctx context.Context, t *store.Task) {
	defer o.unregister(t.ID)
	if !o.acquire(ctx) { // 排队等并发槽（期间状态保持 queued）
		return
	}
	defer o.release()
	if o.isCanceled(t.ID) {
		return
	}
	o.setState(t, store.StateRunningB)
	o.publish(t.ID, "", "task.running_b", "info", "开始：调查 → 建 Issue（B）")
	if err := o.runner.RunB(ctx, t); err != nil {
		if o.isCanceled(t.ID) {
			return // 已由 Cancel 置 canceled 态
		}
		o.fail(t, pipeline.NodeCreateIssue, fmt.Sprintf("B 失败：%v", err))
		return
	}
	// 重载：拾取 B 阶段经 /internal 事件写入的 IssueNum/IssueURL，避免被本地 stale 副本覆盖。
	t = o.reload(t)
	if t.State.Terminal() {
		return // skill 已显式声明结局（skip/fail），不覆盖
	}
	// B 退出 0 但没产出 Issue 且无明确结论 → 异常兜底，转待裁决。
	if t.IssueNum == 0 {
		o.fail(t, pipeline.NodeCreateIssue,
			"B 完成但未产出 Issue，也未声明 skip/fail（检查 claude 是否登录、是否真正执行了 jira-to-issue）")
		return
	}
	// B 结束 → 等人审。节点 review 置 waiting。
	_ = o.st.UpsertNodeRun(&store.NodeRun{TaskID: t.ID, NodeID: pipeline.NodeReview, State: store.NodeWaiting})
	o.setState(t, store.StateAwaitingReview)
	o.publish(t.ID, pipeline.NodeReview, "task.awaiting_review", "info", "B 完成，等待人工审核")
}

// Approve 人审通过：切标签 → 启动 C。
func (o *Orchestrator) Approve(ctx context.Context, taskID string) error {
	t, err := o.st.GetTask(taskID)
	if err != nil || t == nil {
		return fmt.Errorf("任务不存在: %s", taskID)
	}
	if t.State != store.StateAwaitingReview {
		return fmt.Errorf("任务非等待审核态（当前 %s）", t.State)
	}
	if t.IssueNum > 0 {
		gh := o.deps.Config().Repos[t.Repo].GitHub
		if err := o.deps.Github().SetLabels(ctx, gh, t.IssueNum, []string{LabelApproved}, []string{LabelPending}); err != nil {
			return fmt.Errorf("切换 已审核 标签失败: %w", err)
		}
	}
	mark := time.Now()
	_ = o.st.UpsertNodeRun(&store.NodeRun{TaskID: t.ID, NodeID: pipeline.NodeReview, State: store.NodeOK, EndedAt: &mark})
	o.publish(t.ID, pipeline.NodeReview, "node.completed", "info", "人审通过")
	go o.runC(o.register(t.ID), t)
	return nil
}

// Reject 人审打回：转待裁决。
func (o *Orchestrator) Reject(ctx context.Context, taskID, reason string) error {
	t, err := o.st.GetTask(taskID)
	if err != nil || t == nil {
		return fmt.Errorf("任务不存在: %s", taskID)
	}
	if t.State != store.StateAwaitingReview {
		return fmt.Errorf("任务非等待审核态（当前 %s）", t.State)
	}
	t.Error = reason
	o.setState(t, store.StateRejected)
	_ = o.st.UpsertNodeRun(&store.NodeRun{TaskID: t.ID, NodeID: pipeline.NodeReview, State: store.NodeFail})
	o.publish(t.ID, pipeline.NodeReview, "task.rejected", "warn", "人审打回："+reason)
	return nil
}

// runC 跑实装→PR。
func (o *Orchestrator) runC(ctx context.Context, t *store.Task) {
	defer o.unregister(t.ID)
	if !o.acquire(ctx) {
		return
	}
	defer o.release()
	if o.isCanceled(t.ID) {
		return
	}
	o.setState(t, store.StateRunningC)
	o.publish(t.ID, "", "task.running_c", "info", "开始：实装 → 测试 → PR（C）")
	if err := o.runner.RunC(ctx, t); err != nil {
		if o.isCanceled(t.ID) {
			return
		}
		o.fail(t, pipeline.NodePR, fmt.Sprintf("C 失败：%v", err))
		return
	}
	// 重载：拾取 C 阶段经 /internal 事件写入的 PRURL。
	t = o.reload(t)
	// C 出 PR → 进入 PR 审查闸口（等人 review，可多轮修订），不再直接完成。
	o.enterPRReview(t, "已出 PR，等待人工审查")
}

// enterPRReview 把任务推进到 PR 审查闸口：点亮 PR 节点、PR 审查节点置等待、状态转 awaiting_pr_review。
// PR 号从 PRURL 解析；解析不到则退回旧行为直接完成，避免卡死。
func (o *Orchestrator) enterPRReview(t *store.Task, msg string) {
	if t.PRNum == 0 {
		t.PRNum = parsePRNum(t.PRURL)
	}
	mark := time.Now()
	_ = o.st.UpsertNodeRun(&store.NodeRun{TaskID: t.ID, NodeID: pipeline.NodePR, State: store.NodeOK, EndedAt: &mark})
	if t.PRNum == 0 {
		o.setState(t, store.StateDone)
		o.publish(t.ID, pipeline.NodePR, "task.completed", "warn", "已出 PR，但未能解析 PR 号，跳过 PR 审查环节直接完成")
		return
	}
	_ = o.st.UpsertNodeRun(&store.NodeRun{TaskID: t.ID, NodeID: pipeline.NodePRReview, State: store.NodeWaiting})
	o.setState(t, store.StateAwaitingPRReview)
	o.publish(t.ID, pipeline.NodePRReview, "task.awaiting_pr_review", "info", msg)
}

// StartPoller 起后台轮询：周期性拉取处于 PR 审查态任务的 GitHub review 决议，
// 据此自动推进「通过→完成」或「changes requested→修订」。ctx 取消即停。
func (o *Orchestrator) StartPoller(ctx context.Context) {
	go func() {
		tk := time.NewTicker(o.deps.Config().PRPollInterval())
		defer tk.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tk.C:
				o.pollPRReviews(ctx)
			}
		}
	}()
}

// pollPRReviews 扫一遍处于 PR 审查态的任务，用一次 GraphQL 批量查所有（可跨仓）PR 的决议，再逐个分派。
func (o *Orchestrator) pollPRReviews(ctx context.Context) {
	tasks, err := o.st.ListTasks()
	if err != nil {
		return
	}
	var refs []github.PRRef
	byKey := map[string]*store.Task{}
	for _, t := range tasks {
		if t.State != store.StateAwaitingPRReview || t.PRNum == 0 {
			continue
		}
		repo, ok := o.deps.Config().Repos[t.Repo]
		if !ok {
			continue
		}
		refs = append(refs, github.PRRef{Key: t.ID, Repo: repo.GitHub, Num: t.PRNum})
		byKey[t.ID] = t
	}
	if len(refs) == 0 {
		return
	}
	results, err := o.deps.Github().GetPRReviewsBatch(ctx, refs)
	if err != nil {
		return // 整批失败，下轮再试
	}
	for key, rv := range results {
		if t := byKey[key]; t != nil && rv != nil {
			o.dispatchPRReview(t, rv)
		}
	}
}

// dispatchPRReview 据 review 决议推进任务。幂等游标确保同一条意见只触发一次修订。
func (o *Orchestrator) dispatchPRReview(t *store.Task, rv *github.PRReview) {
	switch rv.Decision {
	case "APPROVED":
		o.completePRReview(t, "PR 已通过审查（approved）")
	case "CHANGES_REQUESTED":
		if rv.LatestAt.IsZero() {
			return
		}
		// 仅对「新于上次已处理游标」的 changes request 反应，避免反复起修订。
		if cur, _ := time.Parse(time.RFC3339, t.ReviewCursor); !rv.LatestAt.After(cur) {
			return
		}
		o.startRevise(t, rv.LatestAt)
	}
	// COMMENTED / REVIEW_REQUIRED / 空：仍等待，不改状态。
}

// completePRReview 把任务从 PR 审查态推进到完成（PR approved）。CAS 守卫防与修订并发。
func (o *Orchestrator) completePRReview(t *store.Task, msg string) {
	if !o.transition(t, store.StateAwaitingPRReview, store.StateDone) {
		return
	}
	mark := time.Now()
	_ = o.st.UpsertNodeRun(&store.NodeRun{TaskID: t.ID, NodeID: pipeline.NodePRReview, State: store.NodeOK, EndedAt: &mark})
	o.publish(t.ID, pipeline.NodePRReview, "task.completed", "info", msg)
}

// startRevise 起一轮修订：校验轮次上限，CAS 抢占状态，记录游标+轮次，异步跑 D。
func (o *Orchestrator) startRevise(t *store.Task, cursor time.Time) {
	if t.ReviewRound >= maxReviewRounds {
		o.fail(t, pipeline.NodePRReview, fmt.Sprintf("PR 审查已修订 %d 轮仍未通过，转人工裁决", maxReviewRounds))
		return
	}
	if !o.transition(t, store.StateAwaitingPRReview, store.StateRunningD) {
		return // 状态已被其它路径改变
	}
	t.ReviewCursor = cursor.Format(time.RFC3339)
	t.ReviewRound++
	_ = o.st.UpdateTask(t)
	go o.runD(o.register(t.ID), t)
}

// runD 跑「按 review 意见修订 PR」，完成后回到 PR 审查闸口等下一轮；失败转待裁决。
func (o *Orchestrator) runD(ctx context.Context, t *store.Task) {
	defer o.unregister(t.ID)
	if !o.acquire(ctx) {
		return
	}
	defer o.release()
	if o.isCanceled(t.ID) {
		return
	}
	now := time.Now()
	_ = o.st.UpsertNodeRun(&store.NodeRun{TaskID: t.ID, NodeID: pipeline.NodeRevise, State: store.NodeRunning, StartedAt: &now})
	o.publish(t.ID, pipeline.NodeRevise, "task.running_d", "info", fmt.Sprintf("按 review 意见修订 PR（第 %d 轮）", t.ReviewRound))
	if err := o.runner.RunD(ctx, t); err != nil {
		if o.isCanceled(t.ID) {
			return
		}
		o.fail(t, pipeline.NodeRevise, fmt.Sprintf("PR 修订失败：%v", err))
		return
	}
	t = o.reload(t)
	if t.State.Terminal() {
		return // skill 已显式声明结局（如 fail）
	}
	mark := time.Now()
	_ = o.st.UpsertNodeRun(&store.NodeRun{TaskID: t.ID, NodeID: pipeline.NodeRevise, State: store.NodeOK, EndedAt: &mark})
	_ = o.st.UpsertNodeRun(&store.NodeRun{TaskID: t.ID, NodeID: pipeline.NodePRReview, State: store.NodeWaiting})
	o.setState(t, store.StateAwaitingPRReview)
	o.publish(t.ID, pipeline.NodePRReview, "task.awaiting_pr_review", "info", "修订已 push，等待再次审查")
}

// ApprovePR 人工在 Dashboard 确认 PR 已通过（兜底轮询延迟/仓库无 required review 时）。
func (o *Orchestrator) ApprovePR(ctx context.Context, taskID string) error {
	t, err := o.st.GetTask(taskID)
	if err != nil || t == nil {
		return fmt.Errorf("任务不存在: %s", taskID)
	}
	if t.State != store.StateAwaitingPRReview {
		return fmt.Errorf("任务非等待 PR 审查态（当前 %s）", t.State)
	}
	o.completePRReview(t, "PR 审查通过（人工确认）")
	return nil
}

// RequestRevise 人工在 Dashboard 触发一轮修订（不等轮询）。游标推进到当下，
// 把现存 review 意见视为已认领，之后仅更新的 review 才再触发。
func (o *Orchestrator) RequestRevise(ctx context.Context, taskID string) error {
	t, err := o.st.GetTask(taskID)
	if err != nil || t == nil {
		return fmt.Errorf("任务不存在: %s", taskID)
	}
	if t.State != store.StateAwaitingPRReview {
		return fmt.Errorf("任务非等待 PR 审查态（当前 %s）", t.State)
	}
	o.startRevise(t, time.Now())
	return nil
}

// --- 内部小工具 ---

// transition 在锁内做状态 CAS：仅当库内当前状态==from 才置 to。
// 返回 true 表示本次抢占成功；成功时把 t 同步到最新库值（含 PRNum/游标等字段）。
func (o *Orchestrator) transition(t *store.Task, from, to store.TaskState) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	cur, err := o.st.GetTask(t.ID)
	if err != nil || cur == nil || cur.State != from {
		return false
	}
	cur.State = to
	_ = o.st.UpdateTask(cur)
	o.syncJira(cur.SourceID, to)
	*t = *cur
	return true
}

// parsePRNum 从 PR URL（.../pull/<n>）解析编号；解析不到返回 0。
func parsePRNum(prURL string) int {
	i := strings.LastIndex(prURL, "/pull/")
	if i < 0 {
		return 0
	}
	rest := prURL[i+len("/pull/"):]
	j := 0
	for j < len(rest) && rest[j] >= '0' && rest[j] <= '9' {
		j++
	}
	n, _ := strconv.Atoi(rest[:j])
	return n
}

// reload 从 store 取最新 task，失败则退回原副本。
func (o *Orchestrator) reload(t *store.Task) *store.Task {
	if fresh, err := o.st.GetTask(t.ID); err == nil && fresh != nil {
		return fresh
	}
	return t
}

func (o *Orchestrator) setState(t *store.Task, s store.TaskState) {
	t.State = s
	_ = o.st.UpdateTask(t)
	o.syncJira(t.SourceID, s)
}

// syncJira 据 config.status_map 把票源状态流转（best-effort，配置为空则不动）。
func (o *Orchestrator) syncJira(sourceID string, s store.TaskState) {
	name := o.deps.Config().StatusMap[string(s)]
	if name == "" {
		return
	}
	prov := o.deps.Provider()
	go func() {
		if err := prov.Transition(context.Background(), sourceID, name); err != nil {
			_ = o.bus.Publish(&store.Event{TaskID: "", Type: "source.transition_failed", Level: "warn",
				Message: fmt.Sprintf("%s → %q 流转失败：%v", sourceID, name, err)})
		}
	}()
}

// DeclareSkip 由 skill 经事件声明「正常无需处理」→ 终态 已跳过（非错误）。
func (o *Orchestrator) DeclareSkip(taskID, reason string) {
	t, err := o.st.GetTask(taskID)
	if err != nil || t == nil || t.State.Terminal() {
		return
	}
	t.Error = reason
	o.setState(t, store.StateSkipped)
	o.publish(taskID, "", "task.skipped", "info", "判定无需处理："+reason)
}

// DeclareFail 由 skill 经事件声明「异常遇阻」→ 终态 待裁决。
func (o *Orchestrator) DeclareFail(taskID, node, reason string) {
	t, err := o.st.GetTask(taskID)
	if err != nil || t == nil || t.State.Terminal() {
		return
	}
	o.fail(t, node, reason)
}

func (o *Orchestrator) fail(t *store.Task, node, msg string) {
	t.Error = msg
	o.setState(t, store.StateAdjudication)
	_ = o.st.UpsertNodeRun(&store.NodeRun{TaskID: t.ID, NodeID: node, State: store.NodeFail})
	o.publish(t.ID, node, "task.failed", "error", msg+" → 待裁决")
}

func (o *Orchestrator) publish(taskID, node, typ, level, msg string) {
	_ = o.bus.Publish(&store.Event{
		TaskID: taskID, NodeID: node, Type: typ, Level: level, Message: msg,
	})
}
