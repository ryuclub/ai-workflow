import { useState } from "react";
import { approvePR, requestRevise, sendAgentMessage } from "../api";
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
  const [towerAsked, setTowerAsked] = useState(false);

  // 让塔台先审一遍 PR：发指令进塔台会话（归属本任务），并弹开塔台窗看结果。
  const towerReview = async () => {
    setBusy(true);
    setErr("");
    try {
      await sendAgentMessage(
        `请审查任务 #${task.seq} 的 PR：用 get_pr 读取 PR 与 diff，对照 get_issue 的最新 Issue（需求/方案分歧点勾选/验收条件），` +
          `给出审查意见：问题清单（按严重度）、风险点、结论（建议通过 / 建议修订及理由）。只审查，不要执行任何写操作。`,
        task.id,
      );
      setTowerAsked(true);
      window.dispatchEvent(new CustomEvent("wf-open-chat")); // 弹开塔台窗看审查过程
    } catch (e) {
      setErr(String((e as Error).message || e));
    } finally {
      setBusy(false);
    }
  };

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
        控制面正轮询 GitHub review 决议：approved 自动结束任务，changes requested 自动触发修订。
        下面按钮用于人工兜底——<b>只改流水线任务状态，不会合并、不会改动 PR</b>；合并请在 GitHub 上操作。
      </div>
      {err && <div className="err">{err}</div>}
      <div className="rg-actions">
        <button disabled={busy} onClick={towerReview} title="让塔台读 PR diff 对照最新 Issue 给出审查意见（只审不动，结果在塔台窗）">
          📡 塔台审查{towerAsked ? "（已发起 →塔台窗）" : ""}
        </button>
        <button className="primary" disabled={busy} onClick={approve} title="仅把本任务标记为完成；PR 保持原样，不会被合并">
          结束任务（审查已通过）
        </button>
        <button disabled={busy} onClick={revise} title="立即触发一轮「按 review 意见修订」，不等轮询">
          触发修订
        </button>
      </div>
    </div>
  );
}
