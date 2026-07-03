import { useCallback, useEffect, useRef, useState } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import {
  type AgentAction,
  type AgentMessage,
  confirmAgentAction,
  denyAgentAction,
  getAgentMessages,
  listAgentActions,
  listTasks,
  sendAgentMessage,
  subscribeAgentStream,
  subscribeTaskOutput,
  subscribeTenantEvents,
} from "../api";
import type { EventMsg, Task } from "../types";

const KIND_LABEL: Record<string, string> = {
  wake: "系统事件",
  action_request: "待确认动作",
  action_result: "动作结局",
};

const EV_KEY = "wf.chat.events";
const RUNNING = new Set(["queued", "running_b", "running_c", "running_d"]);

// ChatPanel 是塔台（调度 Agent）的统一控制台：对话（SSE 流式 + 确认卡）、运行中任务的
// claude 输出直播、任务级事件时间线（可开关）。
export default function ChatPanel({ onNewMessage }: { onNewMessage?: () => void }) {
  const [messages, setMessages] = useState<AgentMessage[]>([]);
  const [actions, setActions] = useState<Record<number, AgentAction>>({});
  const [events, setEvents] = useState<EventMsg[]>([]); // 任务级事件时间线（订阅起，不回放）
  const [showEvents, setShowEvents] = useState(() => localStorage.getItem(EV_KEY) !== "0");
  const [running, setRunning] = useState<Task[]>([]);
  const [tasks, setTasks] = useState<Task[]>([]); // 全量任务（过滤下拉 + 徽标显示用）
  const [filter, setFilter] = useState(""); // ""=全局；task id=只看该任务相关
  const [input, setInput] = useState("");
  const [typing, setTyping] = useState("");
  const [enabled, setEnabled] = useState(true);
  const [model, setModel] = useState("");
  const [err, setErr] = useState("");
  const bodyRef = useRef<HTMLDivElement>(null);
  const lastIdRef = useRef(0);

  useEffect(() => localStorage.setItem(EV_KEY, showEvents ? "1" : "0"), [showEvents]);

  const loadActions = useCallback(() => {
    listAgentActions()
      .then((d) => {
        const m: Record<number, AgentAction> = {};
        for (const a of d.actions || []) m[a.id] = a;
        setActions(m);
      })
      .catch(() => {});
  }, []);

  const loadRunning = useCallback(() => {
    listTasks()
      .then((d) => {
        const all = d.tasks || [];
        setTasks(all);
        setRunning(all.filter((t) => RUNNING.has(t.state)));
      })
      .catch(() => {});
  }, []);

  // 首屏：拉历史 + 动作 + 运行中任务，再从游标处订阅聊天流与租户事件流。
  useEffect(() => {
    let closed = false;
    let unsubChat = () => {};
    getAgentMessages(0)
      .then((d) => {
        if (closed) return;
        const msgs = d.messages || [];
        setMessages(msgs);
        setEnabled(d.enabled);
        setModel((d as { model?: string }).model || "");
        lastIdRef.current = msgs.length ? msgs[msgs.length - 1].id : 0;
        loadActions();
        unsubChat = subscribeAgentStream(
          lastIdRef.current,
          (m) => {
            if (m.id <= lastIdRef.current) return;
            lastIdRef.current = m.id;
            setTyping("");
            setMessages((prev) => [...prev, m]);
            if (m.kind === "action_request" || m.kind === "action_result") loadActions();
            onNewMessage?.();
          },
          (delta) => setTyping((t) => t + delta),
        );
      })
      .catch((e) => setErr(e instanceof Error ? e.message : String(e)));
    loadRunning();
    const unsubEvents = subscribeTenantEvents((e) => {
      if (!e.type.startsWith("task.")) return;
      setEvents((prev) => [...prev.slice(-99), e]);
      loadRunning(); // 任务状态跃迁 → 刷新运行中列表（直播控制台随之挂/摘）
    });
    // SSE 重连间隙可能错过 task.* 事件（如控制面重启期间起的任务）→ 低频兜底 + 回前台即刷。
    const fallback = setInterval(() => {
      if (document.visibilityState === "visible") loadRunning();
    }, 30000);
    const onVisible = () => {
      if (document.visibilityState === "visible") loadRunning();
    };
    document.addEventListener("visibilitychange", onVisible);
    return () => {
      closed = true;
      unsubChat();
      unsubEvents();
      clearInterval(fallback);
      document.removeEventListener("visibilitychange", onVisible);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    const el = bodyRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [messages, typing, events, showEvents]);

  const [sending, setSending] = useState(false);
  const doSend = async () => {
    const content = input.trim();
    if (!content || sending) return;
    setInput("");
    setErr("");
    setSending(true);
    try {
      // 即时回显：POST 返回的就是落库消息，直接上屏（hub 再推到时会被游标去重）。
      // 任务过滤视图下发送：消息（及塔台回复）归属该任务，过滤视图里可见完整对话。
      const { message } = await sendAgentMessage(content, filter || undefined);
      if (message && message.id > lastIdRef.current) {
        lastIdRef.current = message.id;
        setMessages((prev) => [...prev, message]);
      }
    } catch (e) {
      setInput(content); // 失败把内容还回输入框，不丢
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setSending(false);
    }
  };

  const decide = (id: number, ok: boolean) => {
    setErr("");
    (ok ? confirmAgentAction(id) : denyAgentAction(id, "")).then(loadActions).catch((e) =>
      setErr(e instanceof Error ? e.message : String(e)),
    );
  };

  // 任务 id → 显示引用（#seq）。已删除任务回退短 id。
  const taskRef = (id?: string) => {
    if (!id) return "";
    const tk = tasks.find((t) => t.id === id);
    return tk ? `#${tk.seq}` : id.slice(0, 6);
  };
  // 过滤下拉候选：全部任务（listTasks 已按创建时间倒序），来源确定不依赖消息/事件积累。
  const filterTasks = tasks.slice(0, 50);

  // 对话与事件按时间合并成一条时间线；选了任务过滤则只看该任务相关条目。
  const timeline: Array<{ t: number; msg?: AgentMessage; ev?: EventMsg }> = [
    ...messages
      .filter((m) => !filter || m.task_id === filter)
      .map((m) => ({ t: Date.parse(m.created_at), msg: m })),
    ...(showEvents
      ? events
          .filter((e) => !filter || e.task_id === filter)
          .map((e) => ({ t: Date.parse(e.ts), ev: e }))
      : []),
  ].sort((a, b) => a.t - b.t);

  return (
    <div className="chat">
      <div className="chat-head">
        <span>📡 塔台</span>
        {model && <span className="muted2" title="塔台会话模型（设置页可改）">{model}</span>}
        {!enabled && <span className="muted2">（已停用）</span>}
        <select
          className="chat-filter"
          style={{ marginLeft: "auto" }}
          value={filter}
          title="按任务过滤时间线（普通对话仅在「全局」显示）"
          onChange={(e) => setFilter(e.target.value)}
        >
          <option value="">全局</option>
          {filterTasks.map((tk) => (
            <option key={tk.id} value={tk.id}>
              #{tk.seq} {tk.source_id}
            </option>
          ))}
        </select>
        <button
          className={"mini toggle " + (showEvents ? "on" : "off")}
          title="在时间线中显示任务级事件（起任务/等审/失败等）"
          onClick={() => setShowEvents((v) => !v)}
        >
          事件
        </button>
      </div>
      {running.length > 0 && (
        <div className="chat-live">
          {running
            .filter((t) => !filter || t.id === filter)
            .map((t) => (
              <LiveTaskOutput key={t.id} task={t} />
            ))}
        </div>
      )}
      <div className="chat-body" ref={bodyRef}>
        {timeline.length === 0 && !typing && (
          <div className="muted">
            我是本租户的塔台：问我任务现状、失败原因，或让我起任务/重跑/取消。
            任务失败时我也会主动在这里发通报。
          </div>
        )}
        {timeline.map((it, i) =>
          it.msg ? (
            <ChatMsg
              key={"m" + it.msg.id}
              m={it.msg}
              taskLabel={taskRef(it.msg.task_id)}
              action={it.msg.action_id ? actions[it.msg.action_id] : undefined}
              onDecide={decide}
            />
          ) : (
            <EventLine key={"e" + it.ev!.id + "-" + i} e={it.ev!} taskLabel={taskRef(it.ev!.task_id)} />
          ),
        )}
        {typing && (
          <div className="chat-msg assistant">
            <div className="chat-meta">塔台 · 输入中…</div>
            <div className="chat-bubble markdown">
              <ReactMarkdown remarkPlugins={[remarkGfm]}>{typing}</ReactMarkdown>
            </div>
          </div>
        )}
      </div>
      {err && <div className="err">{err}</div>}
      <div className="chat-input">
        <textarea
          value={input}
          placeholder={enabled ? "问现状 / 下指令…（Enter 发送，Shift+Enter 换行）" : "Agent 已停用"}
          disabled={!enabled}
          rows={2}
          onChange={(e) => setInput(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
              e.preventDefault();
              doSend();
            }
          }}
        />
        <button className="primary" disabled={!enabled || sending || !input.trim()} onClick={doSend}>
          {sending ? "…" : "发送"}
        </button>
      </div>
    </div>
  );
}

// EventLine 是时间线中的一条任务级事件（醒目色条式）。
function EventLine({ e, taskLabel }: { e: EventMsg; taskLabel?: string }) {
  const ts = new Date(e.ts);
  const tsText = isNaN(ts.getTime()) ? "" : ts.toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit" });
  return (
    <div className={"chat-event" + (e.level === "error" ? " ev-error" : e.level === "warn" ? " ev-warn" : "")}>
      {tsText}{taskLabel ? ` · ${taskLabel}` : ""} · {e.message}
    </div>
  );
}

// LiveTaskOutput 是运行中任务的 claude 输出实时控制台（默认展开；收起即断开 SSE）。
function LiveTaskOutput({ task }: { task: Task }) {
  const [open, setOpen] = useState(true);
  const [text, setText] = useState("");
  const [label, setLabel] = useState("");
  const [ended, setEnded] = useState(false);
  const preRef = useRef<HTMLPreElement>(null);

  useEffect(() => {
    if (!open) return;
    setEnded(false);
    setText(""); // 重新订阅会回放累积缓存，先清空防止与上次展开的内容重复
    const unsub = subscribeTaskOutput(
      task.id,
      (chunk, lb) => {
        setLabel(lb);
        setText((t) => (t + chunk).slice(-200000)); // 前端只留尾部 200KB
      },
      () => setEnded(true),
    );
    return unsub;
  }, [open, task.id]);

  useEffect(() => {
    const el = preRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [text]);

  return (
    <div className="live-task">
      <div className="live-task-head" onClick={() => setOpen((v) => !v)}>
        <span className="live-dot" />
        <span className="seq">#{task.seq}</span>
        <span className="badge">{task.source_id}</span>
        <span className="live-task-title">{task.title || task.repo}</span>
        <span className="muted2">{open ? "▾" : "▸"} 实时输出{label ? `（${label} 段）` : ""}</span>
      </div>
      {open && (
        <pre className="logdump live-console" ref={preRef}>
          {text || (ended ? "（本阶段直播已结束；历史输出可在聊天里让塔台 调日志）" : "等待输出…")}
        </pre>
      )}
    </div>
  );
}

function ChatMsg({
  m,
  taskLabel,
  action,
  onDecide,
}: {
  m: AgentMessage;
  taskLabel?: string;
  action?: AgentAction;
  onDecide: (id: number, ok: boolean) => void;
}) {
  const who = m.role === "assistant" ? "塔台" : m.role === "user" ? "我方成员" : KIND_LABEL[m.kind] || "系统";
  const cls = m.role === "assistant" ? "assistant" : m.role === "user" ? "user" : "system";
  const ts = new Date(m.created_at);
  const tsText = isNaN(ts.getTime()) ? "" : ts.toLocaleString("zh-CN", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" });
  return (
    <div className={"chat-msg " + cls}>
      <div className="chat-meta">
        {who} · {tsText}
        {taskLabel && <span className="badge" style={{ marginLeft: 6 }}>{taskLabel}</span>}
      </div>
      <div className="chat-bubble markdown">
        <ReactMarkdown remarkPlugins={[remarkGfm]}>{m.content}</ReactMarkdown>
        {m.kind === "action_request" && action && (
          <div className="chat-action">
            {action.status === "pending" ? (
              <>
                <span className="chat-action-tag">待确认</span>
                <button className="primary mini" onClick={() => onDecide(action.id, true)}>确认执行</button>
                <button className="danger mini" onClick={() => onDecide(action.id, false)}>拒绝</button>
              </>
            ) : (
              <span className="chat-action-tag">
                {action.status === "executed" ? "✅ 已执行" : action.status === "denied" ? "🚫 已拒绝" : "⚠️ 执行失败"}
                {action.result ? ` · ${action.result}` : ""}
              </span>
            )}
          </div>
        )}
      </div>
    </div>
  );
}
