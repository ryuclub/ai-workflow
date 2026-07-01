import { useState } from "react";
import { approvePR, requestRevise } from "../api";
import type { Task } from "../types";

// PR 审查闸口：出 PR 后等人在 GitHub 上 review。控制面会轮询 review 决议自动推进，
// 这里提供人工兜底 —— 直接确认通过、或立刻触发一轮按意见修订。
export default function PRReviewGate({
  task,
  onResolved,
}: {
  task: Task;
  onResolved: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");

  const wrap = (fn: () => Promise<unknown>) => async () => {
    setBusy(true);
    setErr("");
    try {
      await fn();
      onResolved();
    } catch (e) {
      setErr(String((e as Error).message || e));
    } finally {
      setBusy(false);
    }
  };

  const approve = wrap(() => approvePR(task.id));
  const revise = wrap(() => requestRevise(task.id));

  return (
    <div className="review-gate">
      <div className="rg-head">
        <strong>PR 审查{task.review_round ? `（已修订 ${task.review_round} 轮）` : ""}</strong>
        {task.pr_url && (
          <a href={task.pr_url} target="_blank" rel="noreferrer" className="link">
            在 GitHub 审查 PR ↗
          </a>
        )}
      </div>
      <div className="muted" style={{ margin: "6px 0" }}>
        控制面正轮询 GitHub review 决议：approved 自动完成，changes requested 自动触发修订。
        下面按钮用于人工兜底。
      </div>
      {err && <div className="err">{err}</div>}
      <div className="rg-actions">
        <button className="primary" disabled={busy} onClick={approve}>
          确认通过 ✓
        </button>
        <button disabled={busy} onClick={revise}>
          去修订
        </button>
      </div>
    </div>
  );
}
