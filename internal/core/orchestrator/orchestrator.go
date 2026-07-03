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
	Config(tenantID string) *config.Config        // tenantID 为空返回全局模板（运行期操作键）
	Provider(tenantID string) source.Provider     // 按租户票源
	Github(tenantID, userID string) *github.Client // 按(租户,用户)两级令牌的 GitHub 客户端
}

// Orchestrator 持有依赖并驱动任务状态机。
type Orchestrator struct {
	deps   Deps
	st     store.Store
	bus    *events.Bus
	pl     pipeline.Pipeline
	runner runner.Runner

	semSize  int                      // 每租户并发上限
	semMu    sync.Mutex               // 保护 sems
	sems     map[string]chan struct{} // 每租户并发闸：一家占满不影响他家
	mu       sync.Mutex
	cancels  map[string]context.CancelFunc // 运行中任务的取消句柄
	canceled map[string]bool               // 已请求取消的任务
}

// New 构造编排器。deps 在每次取用时返回当前生效的配置/票源/客户端。
func New(deps Deps, st store.Store, bus *events.Bus, r runner.Runner) *Orchestrator {
	return &Orchestrator{
		deps: deps, st: st, bus: bus, pl: pipeline.Default(), runner: r,
		semSize: deps.Config("").MaxConcurrent(), sems: map[string]chan struct{}{},
		cancels: map[string]context.CancelFunc{}, canceled: map[string]bool{},
	}
}

// tenantSem 返回某租户的并发闸（懒建，容量=semSize）。
func (o *Orchestrator) tenantSem(tenantID string) chan struct{} {
	o.semMu.Lock()
	defer o.semMu.Unlock()
	s := o.sems[tenantID]
	if s == nil {
		s = make(chan struct{}, o.semSize)
		o.sems[tenantID] = s
	}
	return s
}

// acquire 取该租户的并发槽；排队期间若任务被取消（ctx done）则返回 false。
func (o *Orchestrator) acquire(ctx context.Context, tenantID string) bool {
	select {
	case o.tenantSem(tenantID) <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

func (o *Orchestrator) release(tenantID string) { <-o.tenantSem(tenantID) }

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
	o.publish(t, "", "task.canceled", "warn", "任务已取消")
	return nil
}

// Pipeline 返回内置流水线定义（供 API 输出给前端画图）。
func (o *Orchestrator) Pipeline() pipeline.Pipeline { return o.pl }

// Restart 原地从指定段重跑同一任务（不开新任务）：B=重新调查建 Issue（既有 Issue 走 upsert）、
// C=重新实装、D=重新按意见修订。仅限非运行中任务；C 需已有 Issue、D 需已有 PR。
func (o *Orchestrator) Restart(taskID, stage string) error {
	t, err := o.st.GetTask(taskID)
	if err != nil || t == nil {
		return fmt.Errorf("任务不存在: %s", taskID)
	}
	switch t.State {
	case store.StateQueued, store.StateRunningB, store.StateRunningC, store.StateRunningD:
		return fmt.Errorf("任务正在运行（%s），如需重跑请先取消", t.State)
	}
	stage = strings.ToUpper(strings.TrimSpace(stage))
	var reset []string
	var run func(context.Context, *store.Task)
	switch stage {
	case "B":
		reset = o.pl.NodeIDs() // 从头来：全部节点复位
		run = o.runB
	case "C":
		if t.IssueNum == 0 {
			return fmt.Errorf("该任务尚无 Issue，不能从 C 重跑（可从 B 重跑）")
		}
		// 防绕过人审：C 的前提是「审核确实通过过」（review 节点 ok，如 C 段失败/中断的重跑）。
		// 审核从未通过（等审核中/被打回）的任务从 C 重跑会跳过闸口，拒绝。
		if !o.reviewPassed(t.ID) {
			return fmt.Errorf("该任务的 Issue 审核尚未通过，从 C 重跑会绕过审核闸口；请先完成审核（或从 B 重跑）")
		}
		reset = []string{pipeline.NodeBlueprint, pipeline.NodeImplement, pipeline.NodeTest,
			pipeline.NodePR, pipeline.NodePRReview, pipeline.NodeRevise}
		run = o.runC
	case "D":
		if t.PRNum == 0 {
			return fmt.Errorf("该任务尚无 PR，不能从 D 重跑")
		}
		reset = []string{pipeline.NodePRReview, pipeline.NodeRevise}
		run = o.runD
	default:
		return fmt.Errorf("未知阶段 %q（可选 B / C / D）", stage)
	}
	// 清取消标记：曾被取消的任务重跑时会被 isCanceled 秒退，必须先清。
	o.mu.Lock()
	delete(o.canceled, taskID)
	o.mu.Unlock()
	for _, id := range reset {
		_ = o.st.UpsertNodeRun(&store.NodeRun{TaskID: t.ID, NodeID: id, State: store.NodePending})
	}
	t.Error = ""
	if stage == "D" {
		// 现存 review 意见视为本轮要处理的对象；游标推进到当下防轮询重复触发。
		t.ReviewCursor = time.Now().Format(time.RFC3339)
	}
	// 立即置排队态：UI 马上能看出「重跑已受理、在等并发槽」，而不是停留在旧终态。
	o.setState(t, store.StateQueued)
	o.bumpGen(t)
	o.publish(t, "", "task.restarted", "info", "从 "+stage+" 段重跑（排队中）")
	go run(o.register(t.ID), t)
	return nil
}

// StartTask 校验入参、做幂等、创建任务并异步启动 B。sourceID 为活跃源的工单标识，title 为票标题快照。
func (o *Orchestrator) StartTask(tenantID, createdBy, sourceID, repo, title, idem string) (*store.Task, error) {
	src := o.deps.Provider(tenantID)
	if src == nil {
		return nil, fmt.Errorf("租户票源未配置")
	}
	if !src.ValidateID(sourceID) {
		return nil, fmt.Errorf("非法 %s 标识: %q", src.Name(), sourceID)
	}
	if _, ok := o.deps.Config(tenantID).Repos[repo]; !ok {
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
	o.publish(t, "", "task.created", "info", fmt.Sprintf("任务创建：%s/%s → %s", t.Source, sourceID, repo))
	o.bumpGen(t)
	go o.runB(o.register(t.ID), t)
	return t, nil
}

// runB 跑调查→建 Issue，完成后停在人审闸口。
func (o *Orchestrator) runB(ctx context.Context, t *store.Task) {
	defer o.unregister(t.ID)
	if !o.acquire(ctx, t.TenantID) { // 排队等并发槽（期间状态保持 queued）
		return
	}
	defer o.release(t.TenantID)
	if o.isCanceled(t.ID) {
		return
	}
	o.setState(t, store.StateRunningB)
	// 立即点亮首节点：克隆仓/启动 claude 的准备期（可达数十秒）内流程图也有进度反馈，
	// 不等 skill 上报第一个阶段事件。
	startMark := time.Now()
	_ = o.st.UpsertNodeRun(&store.NodeRun{TaskID: t.ID, NodeID: pipeline.NodeReadJira, State: store.NodeRunning, StartedAt: &startMark})
	o.publish(t, pipeline.NodeReadJira, "task.running_b", "info", "开始：调查 → 建 Issue（B）")
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
	// B 结束 → 审核闸口。启用塔台自动审核的租户：塔台审核节点转运行中，
	// watcher 会据 task.awaiting_review 事件唤醒塔台执行审核（Slack 提醒此时被抑制，
	// 塔台判定需人工时经 EscalateReview 再发）；未启用：直接等人审。
	now := time.Now()
	if o.deps.Config(t.TenantID).AgentAutoReview() {
		_ = o.st.UpsertNodeRun(&store.NodeRun{TaskID: t.ID, NodeID: pipeline.NodeAgentReview, State: store.NodeRunning, StartedAt: &now})
		o.setState(t, store.StateAwaitingReview)
		o.publish(t, pipeline.NodeAgentReview, "task.awaiting_review", "info", "B 完成，塔台审核中")
		return
	}
	_ = o.st.UpsertNodeRun(&store.NodeRun{TaskID: t.ID, NodeID: pipeline.NodeReview, State: store.NodeWaiting})
	o.setState(t, store.StateAwaitingReview)
	o.publish(t, pipeline.NodeReview, "task.awaiting_review", "info", "B 完成，等待人工审核")
}

// EscalateReview 由塔台在自动审核中判定「需人工介入」时调用：塔台审核节点收尾、
// 人审节点转等待，并以 warn 事件发 Slack 提醒（自动审核模式下原始等审提醒被抑制）。
func (o *Orchestrator) EscalateReview(taskID, reason string) error {
	t, err := o.st.GetTask(taskID)
	if err != nil || t == nil {
		return fmt.Errorf("任务不存在: %s", taskID)
	}
	if t.State != store.StateAwaitingReview {
		return fmt.Errorf("任务非等待审核态（当前 %s）", t.State)
	}
	mark := time.Now()
	_ = o.st.UpsertNodeRun(&store.NodeRun{TaskID: t.ID, NodeID: pipeline.NodeAgentReview, State: store.NodeOK, EndedAt: &mark})
	_ = o.st.UpsertNodeRun(&store.NodeRun{TaskID: t.ID, NodeID: pipeline.NodeReview, State: store.NodeWaiting})
	o.publish(t, pipeline.NodeReview, "task.review_escalated", "warn", "塔台审核：需人工介入——"+reason)
	return nil
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
		gh := o.deps.Config(t.TenantID).Repos[t.Repo].GitHub
		if err := o.deps.Github(t.TenantID, t.CreatedBy).SetLabels(ctx, gh, t.IssueNum, []string{LabelApproved}, []string{LabelPending}); err != nil {
			return fmt.Errorf("切换 已审核 标签失败: %w", err)
		}
	}
	mark := time.Now()
	// 塔台审核节点一并收尾（未启用自动审核的租户该节点不在图中，落库无副作用）。
	_ = o.st.UpsertNodeRun(&store.NodeRun{TaskID: t.ID, NodeID: pipeline.NodeAgentReview, State: store.NodeOK, EndedAt: &mark})
	_ = o.st.UpsertNodeRun(&store.NodeRun{TaskID: t.ID, NodeID: pipeline.NodeReview, State: store.NodeOK, EndedAt: &mark})
	o.publish(t, pipeline.NodeReview, "node.completed", "info", "审核通过")
	o.bumpGen(t)
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
	o.publish(t, pipeline.NodeReview, "task.rejected", "warn", "人审打回："+reason)
	return nil
}

// runC 跑实装→PR。
func (o *Orchestrator) runC(ctx context.Context, t *store.Task) {
	defer o.unregister(t.ID)
	if !o.acquire(ctx, t.TenantID) {
		return
	}
	defer o.release(t.TenantID)
	if o.isCanceled(t.ID) {
		return
	}
	o.setState(t, store.StateRunningC)
	// 同 runB：准备期即点亮首节点。
	startMark := time.Now()
	_ = o.st.UpsertNodeRun(&store.NodeRun{TaskID: t.ID, NodeID: pipeline.NodeBlueprint, State: store.NodeRunning, StartedAt: &startMark})
	o.publish(t, pipeline.NodeBlueprint, "task.running_c", "info", "开始：实装 → 测试 → PR（C）")
	if err := o.runner.RunC(ctx, t); err != nil {
		if o.isCanceled(t.ID) {
			return
		}
		o.fail(t, pipeline.NodePR, fmt.Sprintf("C 失败：%v", err))
		return
	}
	// 重载：拾取 C 阶段经 /internal 事件写入的 PRURL。
	t = o.reload(t)
	if t.State.Terminal() {
		return // skill 已显式声明结局（skip/fail），不覆盖（与 runB/runD 一致）
	}
	// C 退出 0 但完全没产出 PR（skill 遇阻/自我拒绝/对齐失败）→ 待裁决，
	// 不能走 enterPRReview 的「解析不到 PR 号直接完成」兜底而误判为完成。
	if t.PRNum == 0 && t.PRURL == "" {
		o.fail(t, pipeline.NodePR, "C 完成但未产出 PR（可能实装受阻或自我拒绝，见 C 日志）")
		return
	}
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
		o.publish(t, pipeline.NodePR, "task.completed", "warn", "已出 PR，但未能解析 PR 号，跳过 PR 审查环节直接完成")
		return
	}
	_ = o.st.UpsertNodeRun(&store.NodeRun{TaskID: t.ID, NodeID: pipeline.NodePRReview, State: store.NodeWaiting})
	o.setState(t, store.StateAwaitingPRReview)
	o.publish(t, pipeline.NodePRReview, "task.awaiting_pr_review", "info", msg)
}

// StartPoller 起后台轮询：周期性拉取处于 PR 审查态任务的 GitHub review 决议，
// 据此自动推进「通过→完成」或「changes requested→修订」。ctx 取消即停。
func (o *Orchestrator) StartPoller(ctx context.Context) {
	go func() {
		tk := time.NewTicker(o.deps.Config("").PRPollInterval())
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
	// 按租户分组：各租户用各自的 GitHub 客户端（token 不同），分别批量查询。
	byKey := map[string]*store.Task{}
	refsByTenant := map[string][]github.PRRef{}
	for _, t := range tasks {
		if t.State != store.StateAwaitingPRReview || t.PRNum == 0 {
			continue
		}
		repo, ok := o.deps.Config(t.TenantID).Repos[t.Repo]
		if !ok {
			continue
		}
		refsByTenant[t.TenantID] = append(refsByTenant[t.TenantID], github.PRRef{Key: t.ID, Repo: repo.GitHub, Num: t.PRNum})
		byKey[t.ID] = t
	}
	for tenantID, refs := range refsByTenant {
		gh := o.deps.Github(tenantID, "") // 读 PR 决议用租户共享令牌
		if gh == nil {
			continue
		}
		results, err := gh.GetPRReviewsBatch(ctx, refs)
		if err != nil {
			continue // 该租户整批失败，下轮再试
		}
		for key, rv := range results {
			if t := byKey[key]; t != nil && rv != nil {
				o.dispatchPRReview(t, rv)
			}
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
	o.publish(t, pipeline.NodePRReview, "task.completed", "info", msg)
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
	t.RunGen++
	_ = o.st.UpdateTask(t)
	go o.runD(o.register(t.ID), t)
}

// runD 跑「按 review 意见修订 PR」，完成后回到 PR 审查闸口等下一轮；失败转待裁决。
func (o *Orchestrator) runD(ctx context.Context, t *store.Task) {
	defer o.unregister(t.ID)
	if !o.acquire(ctx, t.TenantID) {
		return
	}
	defer o.release(t.TenantID)
	if o.isCanceled(t.ID) {
		return
	}
	now := time.Now()
	_ = o.st.UpsertNodeRun(&store.NodeRun{TaskID: t.ID, NodeID: pipeline.NodeRevise, State: store.NodeRunning, StartedAt: &now})
	o.publish(t, pipeline.NodeRevise, "task.running_d", "info", fmt.Sprintf("按 review 意见修订 PR（第 %d 轮）", t.ReviewRound))
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
	o.publish(t, pipeline.NodePRReview, "task.awaiting_pr_review", "info", "修订已 push，等待再次审查")
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

// ResumeReview 把中断/待裁决的任务送回审核闸口：Issue 已产出时无需重跑 B，
// 直接恢复「等待审核」状态（典型场景：等审前后被服务重启打断转了待裁决）。
func (o *Orchestrator) ResumeReview(taskID string) error {
	t, err := o.st.GetTask(taskID)
	if err != nil || t == nil {
		return fmt.Errorf("任务不存在: %s", taskID)
	}
	switch t.State {
	case store.StateQueued, store.StateRunningB, store.StateRunningC, store.StateRunningD:
		return fmt.Errorf("任务正在运行（%s）", t.State)
	case store.StateAwaitingReview:
		return fmt.Errorf("任务已在审核闸口")
	}
	if t.IssueNum == 0 {
		return fmt.Errorf("该任务尚无 Issue，请从「读取 JIRA」重跑（B）")
	}
	o.mu.Lock()
	delete(o.canceled, taskID)
	o.mu.Unlock()
	t.Error = ""
	now := time.Now()
	if o.deps.Config(t.TenantID).AgentAutoReview() {
		_ = o.st.UpsertNodeRun(&store.NodeRun{TaskID: t.ID, NodeID: pipeline.NodeAgentReview, State: store.NodeRunning, StartedAt: &now})
		o.setState(t, store.StateAwaitingReview)
		o.publish(t, pipeline.NodeAgentReview, "task.awaiting_review", "info", "回到审核闸口，塔台审核中")
		return nil
	}
	_ = o.st.UpsertNodeRun(&store.NodeRun{TaskID: t.ID, NodeID: pipeline.NodeReview, State: store.NodeWaiting})
	o.setState(t, store.StateAwaitingReview)
	o.publish(t, pipeline.NodeReview, "task.awaiting_review", "info", "回到审核闸口，等待人工审核")
	return nil
}

// ResumePRReview 把中断的任务送回 PR 审查闸口：PR 已产出时无需重跑 D，
// 直接恢复「等待 PR 审查」（轮询/人工按钮随后驱动通过或修订）。
func (o *Orchestrator) ResumePRReview(taskID string) error {
	t, err := o.st.GetTask(taskID)
	if err != nil || t == nil {
		return fmt.Errorf("任务不存在: %s", taskID)
	}
	switch t.State {
	case store.StateQueued, store.StateRunningB, store.StateRunningC, store.StateRunningD:
		return fmt.Errorf("任务正在运行（%s）", t.State)
	case store.StateAwaitingPRReview:
		return fmt.Errorf("任务已在 PR 审查闸口")
	}
	if t.PRNum == 0 {
		return fmt.Errorf("该任务尚无 PR，无法回到 PR 审查（可重新实装）")
	}
	o.mu.Lock()
	delete(o.canceled, taskID)
	o.mu.Unlock()
	t.Error = ""
	// 游标推进到当下：闸口恢复前的历史 review 意见视为已认领，防轮询立即触发修订。
	t.ReviewCursor = time.Now().Format(time.RFC3339)
	_ = o.st.UpsertNodeRun(&store.NodeRun{TaskID: t.ID, NodeID: pipeline.NodePRReview, State: store.NodeWaiting})
	o.setState(t, store.StateAwaitingPRReview)
	o.publish(t, pipeline.NodePRReview, "task.awaiting_pr_review", "info", "回到 PR 审查闸口")
	return nil
}

// bumpGen 递增任务运行代数并落库：每启动一段 claude 调一次。
// 回传事件带代数校验，旧代数（孤儿进程）的事件被丢弃——服务重启会遗孤正在跑的
// claude 子进程，它们若继续回传会污染新一轮运行的节点状态。
func (o *Orchestrator) bumpGen(t *store.Task) {
	t.RunGen++
	_ = o.st.UpdateTask(t)
}

// reviewPassed 判断该任务的 Issue 审核节点是否已通过（ok）。
func (o *Orchestrator) reviewPassed(taskID string) bool {
	runs, err := o.st.ListNodeRuns(taskID)
	if err != nil {
		return false
	}
	for _, nr := range runs {
		if nr.NodeID == pipeline.NodeReview {
			return nr.State == store.NodeOK
		}
	}
	return false
}

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
	o.syncJira(cur.TenantID, cur.SourceID, to)
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
	o.syncJira(t.TenantID, t.SourceID, s)
}

// syncJira 据该租户 config.status_map 把票源状态流转（best-effort，配置为空则不动）。
func (o *Orchestrator) syncJira(tenantID, sourceID string, s store.TaskState) {
	name := o.deps.Config(tenantID).StatusMap[string(s)]
	if name == "" {
		return
	}
	prov := o.deps.Provider(tenantID)
	if prov == nil {
		return
	}
	go func() {
		if err := prov.Transition(context.Background(), sourceID, name); err != nil {
			_ = o.bus.Publish(&store.Event{TaskID: "", TenantID: tenantID, Type: "source.transition_failed", Level: "warn",
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
	o.publish(t, "", "task.skipped", "info", "判定无需处理："+reason)
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
	o.publish(t, node, "task.failed", "error", msg+" → 待裁决")
}

func (o *Orchestrator) publish(t *store.Task, node, typ, level, msg string) {
	_ = o.bus.Publish(&store.Event{
		TaskID: t.ID, TenantID: t.TenantID, NodeID: node, Type: typ, Level: level, Message: msg,
	})
}
