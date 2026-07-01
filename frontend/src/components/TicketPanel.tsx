import { useState } from "react";
import { getTaskTicket } from "../api";
import type { Ticket } from "../types";

// 票详情面板：表头直接显示票标题，可折叠，展开时按需拉取源工单的状态/正文。
export default function TicketPanel({ taskId, title }: { taskId: string; title?: string }) {
  const [open, setOpen] = useState(false);
  const [tk, setTk] = useState<Ticket | null>(null);
  const [err, setErr] = useState("");
  const [loading, setLoading] = useState(false);

  const toggle = () => {
    const next = !open;
    setOpen(next);
    if (next && !tk && !loading) {
      setLoading(true);
      setErr("");
      getTaskTicket(taskId)
        .then(setTk)
        .catch((e) => setErr(String(e.message || e)))
        .finally(() => setLoading(false));
    }
  };

  return (
    <div className="ticketpanel">
      <div className="tp-head" onClick={toggle}>
        <span className="tp-headtitle" title={title || tk?.title || ""}>
          {title || tk?.title || "票详情"}
        </span>
        <span>{open ? "▾" : "▸"}</span>
      </div>
      {open && (
        <div className="tp-body">
          {loading && <div className="muted">加载中…</div>}
          {err && <div className="err">{err}</div>}
          {tk && (
            <>
              <div className="tp-meta">
                <span>{tk.id}</span>
                <span>{tk.status}</span>
                {tk.assignee && <span>负责人：{tk.assignee}</span>}
                <a href={tk.url} target="_blank" rel="noreferrer" className="link">
                  原链接 ↗
                </a>
              </div>
              <div className="tp-content">{tk.body || "(无正文)"}</div>
            </>
          )}
        </div>
      )}
    </div>
  );
}
