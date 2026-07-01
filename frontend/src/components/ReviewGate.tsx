import { useEffect, useMemo, useState } from "react";
import Markdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { approveTask, getIssue, rejectTask, saveIssue } from "../api";
import type { Issue } from "../types";

// 人审闸口：就地渲染/编辑该任务的 GitHub Issue。
// - 方案分歧点复选框可直接点选（点击即写回 GitHub）；
// - 「审查备注」追加进正文固定段（实装阶段 C 可读）；
// - 保存/通过前做冲突检测：GitHub 已被改过则让人选择覆盖还是加载最新。
export default function ReviewGate({
  taskId,
  onResolved,
}: {
  taskId: string;
  onResolved: () => void;
}) {
  const [issue, setIssue] = useState<Issue | null>(null);
  const [title, setTitle] = useState("");
  const [body, setBody] = useState("");
  const [note, setNote] = useState("");
  const [tab, setTab] = useState<"edit" | "preview">("preview");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");

  useEffect(() => {
    getIssue(taskId)
      .then((iss) => {
        setIssue(iss);
        setTitle(iss.title);
        setBody(iss.body);
      })
      .catch((e) => setErr(String(e.message || e)));
  }, [taskId]);

  const dirty = !!issue && (title !== issue.title || body !== issue.body);

  // 扫描正文里的方案分歧点复选框行（`- [ ]` / `- [x]`），供可交互勾选。
  const tasks = useMemo(() => {
    const out: { line: number; checked: boolean; text: string }[] = [];
    body.split("\n").forEach((l, i) => {
      const m = l.match(/^\s*[-*]\s+\[([ xX])\]\s+(.*)$/);
      if (m) out.push({ line: i, checked: m[1].toLowerCase() === "x", text: m[2] });
    });
    return out;
  }, [body]);

  const wrap = (fn: () => Promise<unknown>) => async () => {
    setBusy(true);
    setErr("");
    try {
      await fn();
    } catch (e) {
      setErr(String((e as Error).message || e));
    } finally {
      setBusy(false);
    }
  };

  // 写回 GitHub 并把本地快照推进到已保存内容（避免随后误判冲突）。
  const persist = async (t: string, b: string) => {
    await saveIssue(taskId, t, b);
    setIssue((prev) => (prev ? { ...prev, title: t, body: b } : prev));
  };

  // 冲突检测：远端已变则让人决定。返回 false = 应中止本次操作。
  const ensureFresh = async (): Promise<boolean> => {
    const latest = await getIssue(taskId);
    if (issue && (latest.title !== issue.title || latest.body !== issue.body)) {
      const useMine = confirm(
        "GitHub 上的 Issue 已被改动（可能你在 GitHub 直接改了）。\n\n" +
          "确定 = 用当前 Dashboard 版本覆盖；\n取消 = 放弃本次并加载 GitHub 最新版本。",
      );
      if (!useMine) {
        setIssue(latest);
        setTitle(latest.title);
        setBody(latest.body);
        return false;
      }
    }
    return true;
  };

  // 勾选/取消方案分歧点（即时写回）。
  const toggleTask = async (lineIdx: number) => {
    setBusy(true);
    setErr("");
    try {
      const lines = body.split("\n");
      lines[lineIdx] = /\[[ ]\]/.test(lines[lineIdx])
        ? lines[lineIdx].replace(/\[ \]/, "[x]")
        : lines[lineIdx].replace(/\[[xX]\]/, "[ ]");
      const nb = lines.join("\n");
      setBody(nb);
      await persist(title, nb);
    } catch (e) {
      setErr(String((e as Error).message || e));
    } finally {
      setBusy(false);
    }
  };

  // 追加审查备注到正文「## 审查备注」段（最新在段首），并写回。
  const appendNote = wrap(async () => {
    const n = note.trim();
    if (!n) return;
    const header = "## 审查备注";
    const stamp = new Date().toLocaleString();
    const line = `- （${stamp}）${n}`;
    const nb = body.includes(header)
      ? body.replace(header, `${header}\n${line}`)
      : `${body.trimEnd()}\n\n${header}\n${line}`;
    setBody(nb);
    setNote("");
    await persist(title, nb);
  });

  const save = wrap(async () => {
    if (!(await ensureFresh())) return;
    await persist(title, body);
  });

  const approve = wrap(async () => {
    if (!(await ensureFresh())) return;
    if (dirty) await persist(title, body);
    await approveTask(taskId);
    onResolved();
  });

  const reject = wrap(async () => {
    const reason = prompt("打回原因？") || "";
    await rejectTask(taskId, reason);
    onResolved();
  });

  if (err && !issue) return <div className="err">读取 Issue 失败：{err}</div>;
  if (!issue) return <div className="muted">加载 Issue…</div>;

  return (
    <div className="review-gate">
      <div className="rg-head">
        <strong>人工审核 Issue #{issue.number}</strong>
        <a href={issue.url} target="_blank" rel="noreferrer" className="link">
          在 GitHub 打开 ↗
        </a>
      </div>
      <input
        className="rg-title"
        value={title}
        onChange={(e) => setTitle(e.target.value)}
      />
      <div className="rg-tabs">
        <button className={tab === "preview" ? "on" : ""} onClick={() => setTab("preview")}>
          预览
        </button>
        <button className={tab === "edit" ? "on" : ""} onClick={() => setTab("edit")}>
          编辑
        </button>
      </div>

      {/* 方案分歧点：可点击勾选，点击即写回 GitHub */}
      {tasks.length > 0 && (
        <div className="rg-choices" style={{ border: "1px solid #e0e0e0", borderRadius: 6, padding: 8, margin: "6px 0" }}>
          <div className="muted2" style={{ marginBottom: 4 }}>方案分歧点 · 勾选选定项（点击即写回 GitHub）</div>
          {tasks.map((t) => (
            <label key={t.line} style={{ display: "flex", gap: 6, alignItems: "flex-start", padding: "2px 0", cursor: "pointer" }}>
              <input type="checkbox" checked={t.checked} disabled={busy} onChange={() => toggleTask(t.line)} />
              <span>{t.text}</span>
            </label>
          ))}
        </div>
      )}

      {tab === "edit" ? (
        <textarea
          className="rg-body"
          value={body}
          onChange={(e) => setBody(e.target.value)}
        />
      ) : (
        <div className="rg-preview markdown">
          <Markdown remarkPlugins={[remarkGfm]}>{body}</Markdown>
        </div>
      )}

      {/* 审查备注：追加进正文固定段，实装阶段可读 */}
      <div className="rg-note" style={{ margin: "8px 0" }}>
        <textarea
          style={{ width: "100%", minHeight: 48 }}
          placeholder="审查备注 / 意见（追加到 Issue 正文『## 审查备注』段，实装阶段 C 可读）"
          value={note}
          onChange={(e) => setNote(e.target.value)}
        />
        <button disabled={busy || !note.trim()} onClick={appendNote}>
          追加备注并保存
        </button>
      </div>

      {err && <div className="err">{err}</div>}
      <div className="rg-actions">
        <button disabled={busy || !dirty} onClick={save}>
          保存修改
        </button>
        <button className="primary" disabled={busy} onClick={approve}>
          {dirty ? "保存并通过" : "通过 ✓"}
        </button>
        <button className="danger" disabled={busy} onClick={reject}>
          打回
        </button>
      </div>
    </div>
  );
}
