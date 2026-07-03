import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { cancelTask, deleteTask, getTask, getTaskLogs, restartTask, resumePRReviewTask, resumeReviewTask, subscribeEvents } from "../api";
import type { EventMsg, Pipeline, TaskDetail as TD } from "../types";
import { alertDialog, confirmDialog } from "./Dialog";
import LogPanel from "./LogPanel";
import PipelineView from "./PipelineView";
import PRReviewGate from "./PRReviewGate";
import ReviewGate from "./ReviewGate";
import TicketPanel from "./TicketPanel";

// 恢复/重跑选项：按「你想干什么」组织，只列当前任务状态下有意义的项，
// 并按现状标注推荐——用户不需要理解内部的 B/C/D 段模型。
interface RecoverOption {
  key: "resume" | "resumePR" | "B" | "C" | "D";
  title: string;
  desc: string;
  confirm: string;
}

// 图上可点的重跑/恢复点：流程图节点 id → 恢复选项（与下拉菜单同一套语义）。
const NODE_ACTION: Record<string, RecoverOption["key"]> = {
  read_jira: "B",
  review: "resume",
  blueprint: "C",
  pr_review: "resumePR",
  revise: "D",
};

function recoverOptions(t: { issue_num?: number; pr_num?: number; state: string }, reviewOk: boolean): RecoverOption[] {
  const out: RecoverOption[] = [];
  // 1) Issue 已产出但审核未通过（典型：等审前后被打断）→ 回到审核是首选。
  if (t.issue_num && !reviewOk && t.state !== "awaiting_review") {
    out.push({
      key: "resume",
      title: "继续：回到审核",
      desc: "Issue 已生成，不重跑调查，直接恢复「等待审核」",
      confirm: `恢复任务到「等待审核」？\n\n不重跑任何步骤，直接用现有 Issue 等待审核。`,
    });
  }
  // 2) 审核已通过 → 可以重做实装（已有 PR 时为「按最新 Issue 对齐修正既有 PR」）。
  if (t.issue_num && reviewOk) {
    out.push({
      key: "C",
      title: t.pr_num ? "按最新 Issue 对齐 PR" : "重新实装出 PR",
      desc: t.pr_num
        ? "核对最新 Issue（含分歧点勾选/验收变化）与既有 PR 的差异并修正，不重开 PR"
        : "保留 Issue 与审核结果，重跑「建分支 → 实装 → 测试 → 提 PR」",
      confirm: t.pr_num
        ? "按最新 Issue 对齐既有 PR？\n\n核对 Issue 最新内容与 PR 实现的差异并修正后 push；完全一致则不做变更，回到 PR 审查。"
        : "重新实装？\n\n保留 Issue 与审核结果，从「建分支+蓝图」重跑到「提 PR」。",
    });
  }
  // 3) 已有 PR → 可以回到 PR 审查闸口，或重做一轮修订。
  if (t.pr_num && t.state !== "awaiting_pr_review") {
    out.push({
      key: "resumePR",
      title: "继续：回到 PR 审查",
      desc: "PR 已产出，不重跑，直接恢复「等待 PR 审查」",
      confirm: "恢复任务到「等待 PR 审查」？\n\n不重跑任何步骤，直接用现有 PR 等待审查（approve 通过 / request changes 触发修订）。",
    });
  }
  if (t.pr_num) {
    out.push({
      key: "D",
      title: "重新按 review 意见修订",
      desc: "对现有 PR 重跑一轮「按意见修订」",
      confirm: "重新修订 PR？\n\n对现有 PR 重跑一轮「按意见修订」，完成后回到 PR 审查。",
    });
  }
  // 4) 兜底：从头重来（永远可用）。
  out.push({
    key: "B",
    title: "从头重跑（重新调查）",
    desc: t.issue_num ? "重新调查并更新现有 Issue，再次进入审核" : "重新跑「读取 JIRA → 调查 → 建 Issue」",
    confirm: "从头重跑？\n\n重新执行调查与分析" + (t.issue_num ? "（会更新现有 Issue 内容）" : "") + "，之后再次进入审核。",
  });
  return out;
}

// 任务详情主面板：流水线节点图 + 人审闸口 + 事件流，SSE 实时驱动。
export default function TaskDetail({
  taskId,
  pipeline,
  onDeleted,
}: {
  taskId: string;
  pipeline: Pipeline;
  onDeleted?: () => void;
}) {
  const [detail, setDetail] = useState<TD | null>(null);
  const [events, setEvents] = useState<EventMsg[]>([]);
  const [restartOpen, setRestartOpen] = useState(false);
  const seen = useRef<Set<number>>(new Set());

  const refresh = useCallback(() => {
    getTask(taskId).then(setDetail).catch(() => {});
  }, [taskId]);

  useEffect(() => {
    setDetail(null);
    setEvents([]);
    seen.current = new Set();
    refresh();
    // 事件可能连发（重跑时 node.log 洪峰），详情重拉做 300ms 合并——
    // 高频重建 React Flow 受控节点会导致图上节点短暂消失。
    let timer: ReturnType<typeof setTimeout> | null = null;
    const refreshSoon = () => {
      if (timer) return;
      timer = setTimeout(() => {
        timer = null;
        refresh();
      }, 300);
    };
    const close = subscribeEvents(taskId, (e) => {
      if (seen.current.has(e.id)) return;
      seen.current.add(e.id);
      setEvents((prev) => [...prev, e]);
      refreshSoon();
    });
    return () => {
      close();
      if (timer) clearTimeout(timer);
    };
  }, [taskId, refresh]);

  // —— 派生数据：全部 hooks 必须位于任何 return 之前（否则 hooks 数量随渲染变化会崩） ——
  const t = detail?.task;
  const terminal = !!t && ["done", "failed", "adjudication", "rejected", "canceled", "skipped"].includes(t.state);
  const running = !!t && ["queued", "running_b", "running_c", "running_d"].includes(t.state);
  const reviewOk = detail?.node_runs.some((nr) => nr.node_id === "review" && nr.state === "ok") ?? false;
  const options = t ? recoverOptions(t, reviewOk) : [];

  // 图上可点的节点 → 当前可用的恢复选项（任务运行中不开放）。
  // useMemo 稳定对象标识：避免每次渲染都重建导致 React Flow 节点集反复重算。
  const optionsKey = options.map((o) => o.key).join(",");
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const actionable = useMemo(() => {
    const m: Record<string, string> = {};
    if (!running) {
      for (const [nodeId, key] of Object.entries(NODE_ACTION)) {
        const opt = options.find((o) => o.key === key);
        if (opt) m[nodeId] = opt.title;
      }
    }
    return m;
  }, [running, optionsKey]);

  if (!detail || !t) return <div className="muted pad">加载任务…</div>;

  const doCancel = async () => {
    if (!(await confirmDialog("取消该任务？正在运行的 claude 进程会被终止。", { danger: true, confirmText: "取消任务" }))) return;
    try {
      await cancelTask(taskId);
      refresh();
    } catch (e) {
      alertDialog("取消失败：" + (e as Error).message);
    }
  };

  const doRecover = async (opt: RecoverOption) => {
    setRestartOpen(false);
    if (!(await confirmDialog(opt.confirm, { confirmText: opt.title }))) return;
    try {
      if (opt.key === "resume") await resumeReviewTask(taskId);
      else if (opt.key === "resumePR") await resumePRReviewTask(taskId);
      else await restartTask(taskId, opt.key);
      refresh();
    } catch (e) {
      alertDialog("操作失败：" + (e as Error).message);
    }
  };

  const onNodeAction = (nodeId: string) => {
    const opt = options.find((o) => o.key === NODE_ACTION[nodeId]);
    if (opt) doRecover(opt);
  };

  const doDelete = async () => {
    if (!(await confirmDialog(`删除任务 #${t.seq}（${t.source_id}）及其全部记录？不可恢复。`, { danger: true, confirmText: "删除" }))) return;
    try {
      await deleteTask(taskId);
      onDeleted?.();
    } catch (e) {
      alertDialog("删除失败：" + (e as Error).message);
    }
  };

  return (
    <div className="detail">
      <div className="detail-head">
        <div>
          <span className="seq">#{t.seq}</span>
          <span className="badge">{t.source}/{t.source_id}</span>
          <span className="repo">→ {t.repo}</span>
        </div>
        <div className="links">
          {t.issue_url && (
            <a href={t.issue_url} target="_blank" rel="noreferrer" className="link">
              Issue #{t.issue_num} ↗
            </a>
          )}
          {t.pr_url && (
            <a href={t.pr_url} target="_blank" rel="noreferrer" className="link">
              PR ↗
            </a>
          )}
          {!running && (
            <span className="restart-wrap">
              <button className="mini" onClick={() => setRestartOpen((v) => !v)}>
                恢复 / 重跑 ▾
              </button>
              {restartOpen && (
                <span className="restart-menu recover-menu">
                  {options.map((opt, i) => (
                    <button key={opt.key} className="recover-item" onClick={() => doRecover(opt)}>
                      <span className="recover-title">
                        {opt.title}
                        {i === 0 && options.length > 1 && <span className="recover-rec">推荐</span>}
                      </span>
                      <span className="recover-desc">{opt.desc}</span>
                    </button>
                  ))}
                </span>
              )}
            </span>
          )}
          {!terminal && (
            <button className="danger mini" onClick={doCancel}>
              取消任务
            </button>
          )}
          {terminal && (
            <button className="danger mini" onClick={doDelete}>
              删除任务
            </button>
          )}
        </div>
      </div>

      <PipelineView pipeline={pipeline} runs={detail.node_runs} taskState={t.state} actionable={actionable} onNodeAction={onNodeAction} />

      <TicketPanel taskId={taskId} title={t.title} />

      {t.error && <div className="err">失败原因：{t.error}</div>}

      {t.state === "awaiting_review" && (
        <ReviewGate taskId={taskId} onResolved={refresh} />
      )}

      {t.state === "awaiting_pr_review" && (
        <PRReviewGate task={t} onResolved={refresh} />
      )}

      <LogPanel events={events} />
      {/* 运行中的实时输出在塔台看；这里按需展开历史全量日志（每次展开重新拉取） */}
      <LogsOnDemand taskId={taskId} />
    </div>
  );
}

// LogsOnDemand：折叠的 claude 全量日志（B/C/D 段落盘输出），展开时才拉取。
function LogsOnDemand({ taskId }: { taskId: string }) {
  const [open, setOpen] = useState(false);
  const [logs, setLogs] = useState<{ label: string; content: string }[] | null>(null);

  const toggle = () => {
    const next = !open;
    setOpen(next);
    if (next) {
      setLogs(null);
      getTaskLogs(taskId)
        .then((d) => setLogs(d.logs || []))
        .catch(() => setLogs([]));
    }
  };

  return (
    <div className="logpanel">
      <div className="lp-head" style={{ cursor: "pointer" }} onClick={toggle}>
        {open ? "▾" : "▸"} claude 全量日志（按段落盘）
      </div>
      {open && (
        <div style={{ padding: "6px 12px" }}>
          {logs === null && <div className="muted2">加载中…</div>}
          {logs !== null && logs.length === 0 && <div className="muted2">暂无日志（尚未跑过任何段）</div>}
          {(logs || []).map((l) => (
            <div key={l.label} style={{ marginBottom: 8 }}>
              <div className="muted2" style={{ marginBottom: 4 }}>{l.label} 段</div>
              <pre className="logdump">{l.content}</pre>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
