import { useState } from "react";
import { getTaskLogs } from "../api";

// claude 输出面板：可折叠，展开时按需拉取各阶段(B/C)全量输出。
export default function LogsPanel({ taskId }: { taskId: string }) {
  const [open, setOpen] = useState(false);
  const [logs, setLogs] = useState<{ label: string; content: string }[] | null>(null);
  const [loading, setLoading] = useState(false);

  const toggle = () => {
    const next = !open;
    setOpen(next);
    if (next) {
      setLoading(true);
      getTaskLogs(taskId)
        .then((r) => setLogs(r.logs))
        .catch(() => setLogs([]))
        .finally(() => setLoading(false));
    }
  };

  return (
    <div className="logpanel">
      <div className="lp-head" style={{ cursor: "pointer" }} onClick={toggle}>
        claude 输出 {open ? "▾" : "▸"}
      </div>
      {open && (
        <div className="lp-body">
          {loading && <div className="muted">加载中…</div>}
          {logs && logs.length === 0 && <div className="muted">暂无日志（任务可能尚未运行 claude）</div>}
          {logs?.map((l) => (
            <details key={l.label} open>
              <summary>阶段 {l.label}</summary>
              <pre className="logdump">{l.content}</pre>
            </details>
          ))}
        </div>
      )}
    </div>
  );
}
