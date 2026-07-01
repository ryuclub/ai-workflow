import type { EventMsg } from "../types";

const LEVEL_COLOR: Record<string, string> = {
  info: "#444",
  warn: "#b8860b",
  error: "#e74c3c",
};

export default function LogPanel({ events }: { events: EventMsg[] }) {
  return (
    <div className="logpanel">
      <div className="lp-head">事件流</div>
      <div className="lp-body">
        {events.length === 0 && <div className="muted">暂无事件</div>}
        {events.map((e) => (
          <div key={e.id} className="lp-row">
            <span className="lp-ts">{new Date(e.ts).toLocaleTimeString()}</span>
            <span className="lp-type">{e.type}</span>
            <span style={{ color: LEVEL_COLOR[e.level] || "#444" }}>{e.message}</span>
          </div>
        ))}
      </div>
    </div>
  );
}
