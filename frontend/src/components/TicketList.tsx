import { type FormEvent, useEffect, useMemo, useRef, useState } from "react";
import { listRepos, listTickets, startTask } from "../api";
import type { Repo, Ticket } from "../types";

// 进行中的任务状态（非终态）才挡住重开。
const ACTIVE = ["queued", "running_b", "awaiting_review", "running_c", "awaiting_pr_review", "running_d"];
const isActive = (state?: string) => !!state && ACTIVE.includes(state);

const STATE_CN: Record<string, string> = {
  queued: "排队", running_b: "调查中", awaiting_review: "等人审", running_c: "实装中",
  awaiting_pr_review: "等 PR 审查", running_d: "修订中",
  done: "完成", failed: "失败", adjudication: "待裁决", rejected: "已打回",
  canceled: "已取消", skipped: "已跳过",
};
const cn = (s?: string) => (s ? STATE_CN[s] || s : "");

// 本地持久化:切 tab/刷新页面都保留候选票与上次搜索。
const CACHE_KEY = "wf.tickets.cache.v1";
type Cache = { q: string; tickets: Ticket[]; repos: Repo[] };
const loadCache = (): Cache | null => {
  try {
    return JSON.parse(localStorage.getItem(CACHE_KEY) || "null");
  } catch {
    return null;
  }
};
const saveCache = (c: Cache) => {
  try {
    localStorage.setItem(CACHE_KEY, JSON.stringify(c));
  } catch {
    /* 配额/隐私模式忽略 */
  }
};

// 入口：候选票列表。选票 + 选仓（建议仓预填）+ 开始任务。
export default function TicketList({
  onStarted,
  onOpenTask,
  reposVersion = 0,
}: {
  onStarted: (taskId: string) => void;
  onOpenTask: (taskId: string) => void;
  reposVersion?: number;
}) {
  const cached = loadCache();
  const [tickets, setTickets] = useState<Ticket[]>(cached?.tickets ?? []);
  const [repos, setRepos] = useState<Repo[]>(cached?.repos ?? []);
  const [pick, setPick] = useState<Record<string, string>>(() => {
    const p: Record<string, string> = {};
    for (const tk of cached?.tickets ?? []) p[tk.id] = tk.suggested_repo || "";
    return p;
  });
  const [loading, setLoading] = useState(!cached);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState("");
  const [q, setQ] = useState(cached?.q ?? "");
  const [assignee, setAssignee] = useState(""); // 人员筛选：""=全部，"__none__"=未分配

  // 从当前票集提取去重经办人（供筛选下拉）
  const assignees = useMemo(
    () => Array.from(new Set(tickets.map((t) => t.assignee).filter(Boolean) as string[])).sort(),
    [tickets],
  );
  const hasUnassigned = useMemo(() => tickets.some((t) => !t.assignee), [tickets]);
  const shown = useMemo(() => {
    if (!assignee) return tickets;
    if (assignee === "__none__") return tickets.filter((t) => !t.assignee);
    return tickets.filter((t) => t.assignee === assignee);
  }, [tickets, assignee]);

  const load = (query?: string) => {
    setLoading(true);
    setErr("");
    Promise.all([listTickets(query), listRepos()])
      .then(([t, r]) => {
        const tks = t.tickets || [];
        const rps = r.repos || [];
        setTickets(tks);
        setRepos(rps);
        const p: Record<string, string> = {};
        for (const tk of tks) p[tk.id] = tk.suggested_repo || r.default || (rps[0]?.name ?? "");
        setPick(p);
        saveCache({ q: query ?? "", tickets: tks, repos: rps }); // 本地持久化
      })
      .catch((e) => setErr(String(e.message || e)))
      .finally(() => setLoading(false));
  };
  // 有本地缓存则直接用（切 tab/刷新不重拉票）；无缓存才首拉。
  useEffect(() => {
    if (!cached) load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // 用 ref 持有最新票/查询，供仓刷新时更新缓存（避免闭包捕获旧值）。
  const ticketsRef = useRef(tickets);
  ticketsRef.current = tickets;
  const qRef = useRef(q);
  qRef.current = q;

  // 选仓下拉始终随挂载/登记仓变更刷新：仓列表很轻，且不受票缓存影响，
  // 这样登记新仓后（reposVersion 自增）或整页刷新都能立即看到最新仓。
  useEffect(() => {
    listRepos()
      .then((r) => {
        const rps = r.repos || [];
        setRepos(rps);
        const tks = ticketsRef.current;
        setPick((prev) => {
          const p = { ...prev };
          for (const tk of tks)
            if (!p[tk.id]) p[tk.id] = tk.suggested_repo || r.default || (rps[0]?.name ?? "");
          return p;
        });
        saveCache({ q: qRef.current, tickets: tks, repos: rps }); // 缓存同步最新仓
      })
      .catch(() => {});
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [reposVersion]);

  // 票集变化后，若所选经办人已不存在则回到「全部」
  useEffect(() => {
    if (assignee && assignee !== "__none__" && !assignees.includes(assignee)) setAssignee("");
    if (assignee === "__none__" && !hasUnassigned) setAssignee("");
  }, [assignees, hasUnassigned, assignee]);

  const submitSearch = (e: FormEvent) => {
    e.preventDefault();
    load(q.trim());
  };

  const start = async (tk: Ticket) => {
    setBusy(tk.id);
    setErr("");
    try {
      const task = await startTask(tk.id, pick[tk.id], tk.title);
      onStarted(task.id);
    } catch (e) {
      setErr(String((e as Error).message || e));
    } finally {
      setBusy("");
    }
  };

  return (
    <div className="panel">
      <div className="panel-head">
        <span>候选票</span>
        <button className="mini" onClick={() => { setQ(""); load(); }}>
          刷新
        </button>
      </div>
      <form className="ticket-search" onSubmit={submitSearch}>
        <input
          value={q}
          onChange={(e) => setQ(e.target.value)}
          placeholder="搜索票号(PROJ-3250 / 3250)或标题关键词…"
        />
        <button className="mini" type="submit">
          搜索
        </button>
      </form>
      {(assignees.length > 0 || hasUnassigned) && (
        <div className="ticket-filter">
          <span className="muted2">经办人</span>
          <select value={assignee} onChange={(e) => setAssignee(e.target.value)}>
            <option value="">全部（{tickets.length}）</option>
            {assignees.map((a) => (
              <option key={a} value={a}>{a}</option>
            ))}
            {hasUnassigned && <option value="__none__">未分配</option>}
          </select>
          {assignee && <button className="mini link" onClick={() => setAssignee("")}>清除</button>}
        </div>
      )}
      {loading && <div className="muted">加载中…</div>}
      {err && <div className="err">{err}</div>}
      <div className="ticket-list">
        {shown.map((tk) => (
          <div key={tk.id} className="ticket">
            <div className="ticket-main">
              <a
                className="badge link"
                href={tk.url}
                target="_blank"
                rel="noreferrer"
                title="在 JIRA 打开"
              >
                {tk.id}
              </a>
              <span className="ticket-title" title={tk.title}>
                {tk.title}
              </span>
            </div>
            <div className="ticket-meta">
              <span className="status">{tk.status}</span>
              {tk.existing_task_id && isActive(tk.existing_task_state) ? (
                // 进行中：挡住重开，给「查看」入口
                <button className="mini link" onClick={() => onOpenTask(tk.existing_task_id!)}>
                  进行中 →
                </button>
              ) : (
                <>
                  {tk.existing_task_id && (
                    // 已结束：给上次结果链接，但允许重开
                    <button
                      className="mini link"
                      title={"上次：" + cn(tk.existing_task_state)}
                      onClick={() => onOpenTask(tk.existing_task_id!)}
                    >
                      上次↗
                    </button>
                  )}
                  <select
                    value={pick[tk.id] || ""}
                    onChange={(e) => setPick({ ...pick, [tk.id]: e.target.value })}
                  >
                    {repos.map((r) => (
                      <option key={r.name} value={r.name}>
                        {r.name}
                      </option>
                    ))}
                  </select>
                  <button
                    className="primary mini"
                    disabled={busy === tk.id || !pick[tk.id]}
                    onClick={() => start(tk)}
                  >
                    {busy === tk.id ? "…" : tk.existing_task_id ? "重新开始" : "开始任务"}
                  </button>
                </>
              )}
            </div>
          </div>
        ))}
        {!loading && tickets.length === 0 && <div className="muted">无候选票</div>}
        {!loading && tickets.length > 0 && shown.length === 0 && (
          <div className="muted">该经办人下无候选票</div>
        )}
      </div>
    </div>
  );
}
