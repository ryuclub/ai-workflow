import { useEffect, useRef, useState } from "react";
import { AuthError, clearToken, getAuthStatus, getPipeline, getSource, getToken } from "./api";
import HealthPill from "./components/HealthPill";
import Login from "./components/Login";
import Settings from "./components/Settings";
import TaskDetail from "./components/TaskDetail";
import TaskList from "./components/TaskList";
import TicketList from "./components/TicketList";
import type { Pipeline } from "./types";

const W_KEY = "wf.sidebar.w";
const C_KEY = "wf.sidebar.collapsed";

export default function App() {
  const [authed, setAuthed] = useState<boolean | null>(null); // null=校验中
  const [tab, setTab] = useState<"tickets" | "tasks">("tickets");
  const [showSettings, setShowSettings] = useState(false);
  const [selected, setSelected] = useState<string | null>(null);
  const [pipeline, setPipeline] = useState<Pipeline | null>(null);
  const [source, setSource] = useState("");
  const [refreshKey, setRefreshKey] = useState(0);
  const [reposVersion, setReposVersion] = useState(0); // 登记仓变更后自增，驱动候选票下拉刷新

  const [width, setWidth] = useState(() => Number(localStorage.getItem(W_KEY)) || 360);
  const [collapsed, setCollapsed] = useState(() => localStorage.getItem(C_KEY) === "1");
  const dragging = useRef(false);

  // 启动鉴权校验：不需要鉴权→直接进；需要且有 token→验证；否则要登录。
  useEffect(() => {
    getAuthStatus()
      .then(async (a) => {
        if (!a.auth_required) return setAuthed(true);
        if (!getToken()) return setAuthed(false);
        try {
          await getSource();
          setAuthed(true);
        } catch (e) {
          setAuthed(e instanceof AuthError ? false : true);
        }
      })
      .catch(() => setAuthed(true));
  }, []);

  useEffect(() => {
    if (authed !== true) return;
    getPipeline().then(setPipeline).catch(() => {});
    getSource().then((s) => setSource(s.source)).catch(() => {});
  }, [authed]);

  useEffect(() => localStorage.setItem(W_KEY, String(width)), [width]);
  useEffect(() => localStorage.setItem(C_KEY, collapsed ? "1" : "0"), [collapsed]);

  useEffect(() => {
    const move = (e: MouseEvent) => {
      if (dragging.current) setWidth(Math.min(760, Math.max(220, e.clientX)));
    };
    const up = () => {
      if (dragging.current) {
        dragging.current = false;
        document.body.style.cursor = "";
        document.body.style.userSelect = "";
      }
    };
    window.addEventListener("mousemove", move);
    window.addEventListener("mouseup", up);
    return () => {
      window.removeEventListener("mousemove", move);
      window.removeEventListener("mouseup", up);
    };
  }, []);

  const startDrag = () => {
    dragging.current = true;
    document.body.style.cursor = "col-resize";
    document.body.style.userSelect = "none";
  };

  const isMobile = () => window.matchMedia("(max-width: 720px)").matches;
  const openTask = (id: string) => {
    setSelected(id);
    setTab("tasks");
    setShowSettings(false);
    setRefreshKey((k) => k + 1);
    if (isMobile()) setCollapsed(true); // 窄屏选中后收起抽屉露出内容
  };

  if (authed === null) return <div className="empty">校验中…</div>;
  if (authed === false) return <Login onOk={() => setAuthed(true)} />;

  return (
    <div className="app">
      <header className="topbar">
        <button className="mini" title={collapsed ? "展开侧栏" : "收起侧栏"} onClick={() => setCollapsed((c) => !c)}>
          {collapsed ? "›" : "‹"}
        </button>
        <span className="logo">AI 工作流流水线</span>
        {source && <span className="src-pill">票源：{source}</span>}
        <span style={{ marginLeft: "auto", display: "flex", gap: 8, alignItems: "center" }}>
          <HealthPill />
          <button className={"mini" + (showSettings ? " on-btn" : "")} onClick={() => setShowSettings((v) => !v)}>
            ⚙ 设置
          </button>
          {getToken() && (
            <button className="mini" onClick={() => { clearToken(); setAuthed(false); }}>退出</button>
          )}
        </span>
      </header>
      <div className="layout">
        <aside className={"sidebar" + (collapsed ? " collapsed" : "")} style={{ width: collapsed ? 0 : width, overflow: collapsed ? "hidden" : undefined }}>
          <div className="tabs">
            <button className={tab === "tickets" ? "on" : ""} onClick={() => { setTab("tickets"); setShowSettings(false); }}>候选票</button>
            <button className={tab === "tasks" ? "on" : ""} onClick={() => { setTab("tasks"); setShowSettings(false); }}>任务</button>
          </div>
          <div className="pane" style={{ display: tab === "tickets" ? "flex" : "none" }}>
            <TicketList onStarted={openTask} onOpenTask={openTask} reposVersion={reposVersion} />
          </div>
          <div className="pane" style={{ display: tab === "tasks" ? "flex" : "none" }}>
            <TaskList selectedId={selected} onSelect={(id) => { setSelected(id); setShowSettings(false); }} refreshKey={refreshKey} />
          </div>
        </aside>
        {!collapsed && <div className="resizer" onMouseDown={startDrag} />}
        <main className="main">
          {showSettings ? (
            <Settings onReposChanged={() => setReposVersion((v) => v + 1)} />
          ) : selected && pipeline ? (
            <TaskDetail taskId={selected} pipeline={pipeline} />
          ) : (
            <div className="empty">选择一张候选票开始任务，或从「任务」打开一个流水线。</div>
          )}
        </main>
      </div>
    </div>
  );
}
