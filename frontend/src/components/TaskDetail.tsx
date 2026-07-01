import { useCallback, useEffect, useRef, useState } from "react";
import { cancelTask, getTask, subscribeEvents } from "../api";
import type { EventMsg, Pipeline, TaskDetail as TD } from "../types";
import LogPanel from "./LogPanel";
import LogsPanel from "./LogsPanel";
import PipelineView from "./PipelineView";
import PRReviewGate from "./PRReviewGate";
import ReviewGate from "./ReviewGate";
import TicketPanel from "./TicketPanel";

// 任务详情主面板：流水线节点图 + 人审闸口 + 事件流，SSE 实时驱动。
export default function TaskDetail({
  taskId,
  pipeline,
}: {
  taskId: string;
  pipeline: Pipeline;
}) {
  const [detail, setDetail] = useState<TD | null>(null);
  const [events, setEvents] = useState<EventMsg[]>([]);
  const seen = useRef<Set<number>>(new Set());

  const refresh = useCallback(() => {
    getTask(taskId).then(setDetail).catch(() => {});
  }, [taskId]);

  useEffect(() => {
    setDetail(null);
    setEvents([]);
    seen.current = new Set();
    refresh();
    const close = subscribeEvents(taskId, (e) => {
      if (seen.current.has(e.id)) return;
      seen.current.add(e.id);
      setEvents((prev) => [...prev, e]);
      refresh(); // 任一事件触发详情重拉，节点状态随之更新
    });
    return close;
  }, [taskId, refresh]);

  if (!detail) return <div className="muted pad">加载任务…</div>;
  const t = detail.task;
  const terminal = ["done", "failed", "adjudication", "rejected", "canceled", "skipped"].includes(t.state);

  const doCancel = async () => {
    if (!confirm("取消该任务？正在运行的 claude 进程会被终止。")) return;
    try {
      await cancelTask(taskId);
      refresh();
    } catch (e) {
      alert("取消失败：" + (e as Error).message);
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
          {!terminal && (
            <button className="danger mini" onClick={doCancel}>
              取消任务
            </button>
          )}
        </div>
      </div>

      <PipelineView pipeline={pipeline} runs={detail.node_runs} taskState={t.state} />

      <TicketPanel taskId={taskId} title={t.title} />

      {t.error && <div className="err">失败原因：{t.error}</div>}

      {t.state === "awaiting_review" && (
        <ReviewGate taskId={taskId} onResolved={refresh} />
      )}

      {t.state === "awaiting_pr_review" && (
        <PRReviewGate task={t} onResolved={refresh} />
      )}

      <LogPanel events={events} />
      <LogsPanel taskId={taskId} />
    </div>
  );
}
