import { useEffect, useState } from "react";
import { checkHealth, getHealth, type HealthCheck } from "../api";

const CN: Record<string, string> = { github: "GitHub", source: "票源", claude: "claude" };

// 顶栏健康指示：自动轮询 github/源；点开弹层可「全面检测」(含 claude)。
export default function HealthPill() {
  const [checks, setChecks] = useState<HealthCheck[]>([]);
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);

  const poll = () => getHealth().then((r) => setChecks(r.checks)).catch(() => {});
  useEffect(() => {
    poll();
    const t = setInterval(poll, 30000);
    return () => clearInterval(t);
  }, []);

  const full = async () => {
    setBusy(true);
    try {
      const r = await checkHealth();
      setChecks(r.checks);
    } finally {
      setBusy(false);
    }
  };

  const anyFail = checks.some((c) => !c.ok);
  const color = checks.length === 0 ? "#9aa0a6" : anyFail ? "#e74c3c" : "#2ecc71";

  return (
    <div className="health">
      <button className="mini" onClick={() => setOpen((v) => !v)} title="环境健康">
        <span className="dot" style={{ background: color }} /> 环境
      </button>
      {open && (
        <div className="health-pop">
          {checks.length === 0 && <div className="muted">检测中…</div>}
          {checks.map((c) => (
            <div key={c.name} className="health-row">
              <span className="dot" style={{ background: c.ok ? "#2ecc71" : "#e74c3c" }} />
              <b>{CN[c.name] || c.name}</b>
              <span className="muted2">{c.detail}</span>
            </div>
          ))}
          <button className="mini" disabled={busy} onClick={full}>
            {busy ? "检测中…" : "全面检测（含 claude）"}
          </button>
        </div>
      )}
    </div>
  );
}
