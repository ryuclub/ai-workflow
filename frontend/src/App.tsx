import { useEffect, useRef, useState } from "react";
import { AuthError, clearToken, getAuthStatus, getMe, getPipeline, getSource, getToken, logout, type Membership, setPlatformAdmin, setRole, setToken, switchTenant } from "./api";
import ChatPanel from "./components/ChatPanel";
import DialogHost from "./components/Dialog";
import HealthPill from "./components/HealthPill";
import Login from "./components/Login";
import Settings from "./components/Settings";
import TaskDetail from "./components/TaskDetail";
import TaskList from "./components/TaskList";
import TicketList from "./components/TicketList";
import type { Pipeline } from "./types";

const W_KEY = "wf.sidebar.w";
const C_KEY = "wf.sidebar.collapsed";
const CHAT_KEY = "wf.chat.open";
const CHAT_W_KEY = "wf.chat.w";

export default function App() {
  const [authed, setAuthed] = useState<boolean | null>(null); // null=校验中
  const [tab, setTab] = useState<"tickets" | "tasks">("tickets");
  const [showSettings, setShowSettings] = useState(false);
  const [selected, setSelected] = useState<string | null>(null);
  const [pipeline, setPipeline] = useState<Pipeline | null>(null);
  const [source, setSource] = useState("");
  const [refreshKey, setRefreshKey] = useState(0);
  const [reposVersion, setReposVersion] = useState(0); // 登记仓变更后自增，驱动候选票下拉刷新
  const [tenants, setTenants] = useState<Membership[]>([]); // 当前用户可切换的租户
  const [tenantId, setTenantId] = useState(""); // 当前活跃租户

  const [width, setWidth] = useState(() => Number(localStorage.getItem(W_KEY)) || 360);
  const [collapsed, setCollapsed] = useState(() => localStorage.getItem(C_KEY) === "1");
  const [showChat, setShowChat] = useState(() => localStorage.getItem(CHAT_KEY) === "1");
  const [chatWidth, setChatWidth] = useState(() => Number(localStorage.getItem(CHAT_W_KEY)) || 400);
  const [chatUnread, setChatUnread] = useState(false);
  const showChatRef = useRef(showChat);
  showChatRef.current = showChat;
  const dragging = useRef(false);
  const draggingChat = useRef(false);

  // 启动鉴权校验：不需要鉴权→直接进；需要且有 token→验证；否则要登录。
  useEffect(() => {
    getAuthStatus()
      .then(async (a) => {
        if (!a.auth_required) return setAuthed(true);
        if (!getToken()) return setAuthed(false);
        try {
          const m = await getMe(); // 验证会话并刷新角色/平台标记/租户列表
          setRole(m.role);
          setPlatformAdmin(m.platform_admin);
          setTenants(m.tenants || []);
          setTenantId(m.tenant_id);
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
  useEffect(() => localStorage.setItem(CHAT_KEY, showChat ? "1" : "0"), [showChat]);
  useEffect(() => localStorage.setItem(CHAT_W_KEY, String(chatWidth)), [chatWidth]);

  // 任意组件可经全局事件弹开塔台窗（如 PR 审查闸口的「塔台审查」快捷按钮）。
  useEffect(() => {
    const open = () => {
      setShowChat(true);
      setChatUnread(false);
    };
    window.addEventListener("wf-open-chat", open);
    return () => window.removeEventListener("wf-open-chat", open);
  }, []);

  useEffect(() => {
    const move = (e: MouseEvent) => {
      if (dragging.current) setWidth(Math.min(760, Math.max(220, e.clientX)));
      if (draggingChat.current) setChatWidth(Math.min(760, Math.max(300, window.innerWidth - e.clientX)));
    };
    const up = () => {
      if (dragging.current || draggingChat.current) {
        dragging.current = false;
        draggingChat.current = false;
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
  const startDragChat = () => {
    draggingChat.current = true;
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
        <span className="logo">PR 工厂</span>
        {source && <span className="src-pill">票源：{source}</span>}
        <span style={{ marginLeft: "auto", display: "flex", gap: 8, alignItems: "center" }}>
          {tenants.length > 1 && (
            <select
              className="tenant-switch"
              value={tenantId}
              title="切换租户"
              onChange={async (e) => {
                try {
                  const r = await switchTenant(e.target.value);
                  setToken(r.token);
                  setRole(r.role);
                  window.location.reload(); // 切租户后整页重载，刷新所有租户相关视图
                } catch { /* 忽略：无权限等 */ }
              }}
            >
              {tenants.map((t) => (
                <option key={t.tenant_id} value={t.tenant_id}>
                  {t.tenant_id === tenantId ? "● " : ""}{t.name}（{t.role === "admin" ? "管理员" : "成员"}）
                </option>
              ))}
            </select>
          )}
          <HealthPill />
          <button
            className={"mini chat-toggle" + (showChat ? " on-btn" : "")}
            onClick={() => { setShowChat((v) => !v); setChatUnread(false); }}
          >
            📡 塔台{chatUnread && !showChat ? <span className="chat-dot" /> : null}
          </button>
          <button className={"mini" + (showSettings ? " on-btn" : "")} onClick={() => setShowSettings((v) => !v)}>
            ⚙ 设置
          </button>
          {getToken() && (
            <button className="mini" onClick={() => { logout().catch(() => {}); clearToken(); setAuthed(false); }}>退出</button>
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
            <TaskDetail taskId={selected} pipeline={pipeline} onDeleted={() => setSelected(null)} />
          ) : (
            <div className="empty">选择一张候选票开始任务，或从「任务」打开一个流水线。</div>
          )}
        </main>
        {/* 常驻挂载：SSE 保持连接，关窗时仅隐藏（新消息点亮未读角标） */}
        {showChat && <div className="resizer" onMouseDown={startDragChat} />}
        <aside className={"chatwrap" + (showChat ? "" : " hidden")} style={{ width: showChat ? chatWidth : 0 }}>
          <ChatPanel onNewMessage={() => { if (!showChatRef.current) setChatUnread(true); }} />
        </aside>
      </div>
      <DialogHost />
    </div>
  );
}
