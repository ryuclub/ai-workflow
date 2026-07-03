import { useEffect, useState } from "react";
import { listTasks, subscribeTenantEvents } from "../api";
import type { Task } from "../types";

const STATE_LABEL: Record<string, string> = {
  queued: "排队",
  running_b: "调查中",
  awaiting_review: "等人审",
  running_c: "实装中",
  awaiting_pr_review: "等 PR 审查",
  running_d: "修订中",
  done: "完成",
  failed: "失败",
  adjudication: "待裁决",
  rejected: "已打回",
  canceled: "已取消",
  skipped: "已跳过",
};

const STATE_CLASS: Record<string, string> = {
  awaiting_review: "s-wait",
  awaiting_pr_review: "s-wait",
  running_b: "s-run",
  running_c: "s-run",
  running_d: "s-run",
  done: "s-ok",
  failed: "s-fail",
  adjudication: "s-fail",
  rejected: "s-fail",
  canceled: "s-fail",
  skipped: "s-skip",
};

// 任务列表：事件驱动刷新（租户级 SSE 有事件才重拉；低频兜底轮询防 SSE 断流）。
export default function TaskList({
  selectedId,
  onSelect,
  refreshKey,
}: {
  selectedId: string | null;
  onSelect: (id: string) => void;
  refreshKey: number;
}) {
  const [tasks, setTasks] = useState<Task[]>([]);

  useEffect(() => {
    let alive = true;
    let timer: ReturnType<typeof setTimeout> | null = null;
    const load = () => listTasks().then((d) => alive && setTasks(d.tasks || [])).catch(() => {});
    // 事件可能连发（节点级日志），合并 300ms 内的刷新。
    const loadSoon = () => {
      if (timer) return;
      timer = setTimeout(() => { timer = null; load(); }, 300);
    };
    load();
    const unsub = subscribeTenantEvents(loadSoon);
    const fallback = setInterval(load, 60000);
    return () => {
      alive = false;
      unsub();
      clearInterval(fallback);
      if (timer) clearTimeout(timer);
    };
  }, [refreshKey]);

  return (
    <div className="panel">
      <div className="panel-head">任务</div>
      <div className="task-list">
        {tasks.map((t) => (
          <div
            key={t.id}
            className={"task-row" + (t.id === selectedId ? " sel" : "")}
            onClick={() => onSelect(t.id)}
          >
            <div className="task-row-main">
              <div className="task-row-top">
                <span>
                  <span className="seq">#{t.seq}</span>
                  <span className="badge">{t.source_id}</span>
                </span>
                <span className={"state " + (STATE_CLASS[t.state] || "")}>
                  {STATE_LABEL[t.state] || t.state}
                </span>
              </div>
              <div className="task-row-title" title={t.title || ""}>
                {t.title || "(无标题)"}
              </div>
              <span className="repo">{t.repo}</span>
            </div>
          </div>
        ))}
        {tasks.length === 0 && <div className="muted">暂无任务</div>}
      </div>
    </div>
  );
}
