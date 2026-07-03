import type {
  EventMsg,
  Issue,
  Pipeline,
  Repo,
  SettingsView,
  Task,
  TaskDetail,
  Ticket,
} from "./types";

// —— 登录会话 token 与当前角色 ——
const TOKEN_KEY = "wf.session.token";
const ROLE_KEY = "wf.session.role";
export const getToken = () => localStorage.getItem(TOKEN_KEY) || "";
export const setToken = (t: string) => localStorage.setItem(TOKEN_KEY, t);
export const clearToken = () => {
  localStorage.removeItem(TOKEN_KEY);
  localStorage.removeItem(ROLE_KEY);
};
export const getRole = () => localStorage.getItem(ROLE_KEY) || "";
export const setRole = (r: string) => localStorage.setItem(ROLE_KEY, r);
export const isAdmin = () => getRole() === "admin";
const PLATFORM_KEY = "wf.session.platform";
export const setPlatformAdmin = (v: boolean) => localStorage.setItem(PLATFORM_KEY, v ? "1" : "0");
export const isPlatformAdmin = () => localStorage.getItem(PLATFORM_KEY) === "1";

export class AuthError extends Error {}

function authHeaders(extra?: Record<string, string>): Record<string, string> {
  const h: Record<string, string> = { ...(extra || {}) };
  const t = getToken();
  if (t) h["Authorization"] = "Bearer " + t;
  return h;
}

async function j<T>(r: Response): Promise<T> {
  if (r.status === 401) throw new AuthError("未授权");
  if (!r.ok) {
    const e = await r.json().catch(() => ({}));
    throw new Error((e as { error?: string }).error || r.statusText);
  }
  return r.json() as Promise<T>;
}

const get = (url: string) => fetch(url, { headers: authHeaders() });
const send = (method: string, url: string, body?: unknown) =>
  fetch(url, {
    method,
    headers: authHeaders(body ? { "Content-Type": "application/json" } : undefined),
    body: body ? JSON.stringify(body) : undefined,
  });

export const getAuthStatus = () =>
  fetch("/api/v1/auth/status").then(j<{ auth_required: boolean; bootstrapped: boolean }>);

// —— 登录会话 ——
export interface Membership { tenant_id: string; name: string; role: string }
export interface LoginResult {
  token: string;
  role: string;
  tenant_id: string;
  tenants: Membership[];
  user: { id: string; email: string; platform_admin: boolean };
}
export const login = (email: string, password: string, tenant_id?: string) =>
  fetch("/api/v1/auth/login", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ email, password, tenant_id }),
  }).then(j<LoginResult>);
export const logout = () => send("POST", "/api/v1/auth/logout").then(j<{ ok: boolean }>);
export const getMe = () =>
  get("/api/v1/auth/me").then(
    j<{ user_id: string; tenant_id: string; role: string; platform_admin: boolean; tenants: Membership[] }>,
  );
export const switchTenant = (tenant_id: string) =>
  send("POST", "/api/v1/auth/switch", { tenant_id }).then(j<{ token: string; tenant_id: string; role: string }>);

// —— 平台管理（平台超管）——
export interface Tenant { id: string; name: string; created_at: string }
export const listTenants = () =>
  get("/api/v1/platform/tenants").then(j<{ tenants: Tenant[] }>);
export const createTenant = (name: string, admin_email: string, admin_password: string) =>
  send("POST", "/api/v1/platform/tenants", { name, admin_email, admin_password }).then(
    j<{ ok: boolean; admin_email: string }>,
  );

// —— Claude 登录态令牌（租户共享=管理员；个人=本人）——
export const getTenantClaudeToken = () =>
  get("/api/v1/settings/claude-token").then(j<{ configured: boolean }>);
export const putTenantClaudeToken = (token: string) =>
  send("PUT", "/api/v1/settings/claude-token", { token }).then(j<{ ok: boolean; configured: boolean }>);
export const getMyClaudeToken = () =>
  get("/api/v1/me/claude-token").then(j<{ configured: boolean }>);
export const putMyClaudeToken = (token: string) =>
  send("PUT", "/api/v1/me/claude-token", { token }).then(j<{ ok: boolean; configured: boolean }>);
export const getMyGithubToken = () =>
  get("/api/v1/me/github-token").then(j<{ configured: boolean }>);
export const putMyGithubToken = (token: string) =>
  send("PUT", "/api/v1/me/github-token", { token }).then(j<{ ok: boolean; configured: boolean }>);

// —— 成员管理（租户管理员）——
export interface Member { user_id: string; email: string; role: string }
export const listMembers = () =>
  get("/api/v1/members").then(j<{ members: Member[] }>);
export const addMember = (email: string, password: string, role: string) =>
  send("POST", "/api/v1/members", { email, password, role }).then(j<{ ok: boolean }>);
export const setMemberRole = (uid: string, role: string) =>
  send("PUT", `/api/v1/members/${uid}/role`, { role }).then(j<{ ok: boolean }>);
export const removeMember = (uid: string) =>
  send("DELETE", `/api/v1/members/${uid}`).then(j<{ ok: boolean }>);

export const getSource = () =>
  get("/api/v1/source").then(j<{ source: string; default_query: string }>);

// 公共列表：仅启用仓，供候选票选仓下拉/路由。
export const listRepos = () =>
  get("/api/v1/repos").then(j<{ default: string; repos: Repo[] }>);

// 设置管理列表：全部仓（含停用），供设置页启用/停用管理。
export const listSettingRepos = () =>
  get("/api/v1/settings/repos").then(j<{ default: string; repos: Repo[] }>);

export const getPipeline = () => get("/api/v1/pipeline").then(j<Pipeline>);

export const listTickets = (query?: string) =>
  get("/api/v1/tickets" + (query ? `?query=${encodeURIComponent(query)}` : "")).then(
    j<{ source: string; tickets: Ticket[] }>,
  );

// 工单状态流转：transitions=当前可用目标（JIRA 只回工作流合法项，Linear 回团队状态集）。
export const getTicketTransitions = (id: string) =>
  get(`/api/v1/tickets/${encodeURIComponent(id)}/transitions`).then(j<{ transitions: string[] | null }>);
export const transitionTicket = (id: string, name: string) =>
  send("POST", `/api/v1/tickets/${encodeURIComponent(id)}/transition`, { name }).then(j<{ ok: boolean }>);

export const listTasks = () => get("/api/v1/tasks").then(j<{ tasks: Task[] }>);

export const getTask = (id: string) => get(`/api/v1/tasks/${id}`).then(j<TaskDetail>);

export const startTask = (source_id: string, repo: string, title?: string) =>
  send("POST", "/api/v1/tasks", { source_id, repo, title }).then(j<Task>);

export const approveTask = (id: string) =>
  send("POST", `/api/v1/tasks/${id}/approve`).then(j<{ ok: boolean }>);

export const rejectTask = (id: string, reason: string) =>
  send("POST", `/api/v1/tasks/${id}/reject`, { reason }).then(j<{ ok: boolean }>);

export const approvePR = (id: string) =>
  send("POST", `/api/v1/tasks/${id}/approve-pr`).then(j<{ ok: boolean }>);

export const requestRevise = (id: string) =>
  send("POST", `/api/v1/tasks/${id}/request-revise`).then(j<{ ok: boolean }>);

export const cancelTask = (id: string) =>
  send("POST", `/api/v1/tasks/${id}/cancel`).then(j<{ ok: boolean }>);

export const restartTask = (id: string, stage: "B" | "C" | "D") =>
  send("POST", `/api/v1/tasks/${id}/restart`, { stage }).then(j<{ ok: boolean }>);

// 中断任务回到审核闸口（Issue 已在，无需重跑 B）。
export const resumeReviewTask = (id: string) =>
  send("POST", `/api/v1/tasks/${id}/resume-review`).then(j<{ ok: boolean }>);

// 中断任务回到 PR 审查闸口（PR 已在，无需重跑 D）。
export const resumePRReviewTask = (id: string) =>
  send("POST", `/api/v1/tasks/${id}/resume-pr-review`).then(j<{ ok: boolean }>);

export const deleteTask = (id: string) =>
  send("DELETE", `/api/v1/tasks/${id}`).then(j<{ ok: boolean }>);

export const getIssue = (id: string) => get(`/api/v1/tasks/${id}/issue`).then(j<Issue>);

export const saveIssue = (id: string, title: string, body: string) =>
  send("PUT", `/api/v1/tasks/${id}/issue`, { title, body }).then(j<{ ok: boolean }>);

export const getTaskTicket = (id: string) =>
  get(`/api/v1/tasks/${id}/ticket`).then(j<Ticket>);

// —— 设置中心 ——
export const getSettings = () => get("/api/v1/settings").then(j<SettingsView>);
export const putSettings = (payload: Record<string, unknown>) =>
  send("PUT", "/api/v1/settings", payload).then(j<SettingsView>);
export const testConnection = (kind: "source" | "github") =>
  send("POST", `/api/v1/settings/test/${kind}`).then(j<{ ok: boolean; detail?: string; error?: string }>);
export const upsertRepo = (repo: { name: string; github: string; match: string[]; path?: string; base?: string; enabled?: boolean }) =>
  send("PUT", "/api/v1/settings/repos", repo).then(j<{ ok: boolean }>);
export const deleteRepoApi = (name: string) =>
  send("DELETE", `/api/v1/settings/repos/${encodeURIComponent(name)}`).then(j<{ ok: boolean }>);
export const setRepoEnabled = (name: string, enabled: boolean) =>
  send("PUT", `/api/v1/settings/repos/${encodeURIComponent(name)}/enabled`, { enabled }).then(j<{ ok: boolean }>);
export const importRepos = (owner?: string) =>
  send("POST", "/api/v1/settings/repos/import", owner ? { owner } : {}).then(
    j<{ ok: boolean; owner: string; found: number; added: number; skipped: number }>,
  );

// —— 健康面板 / 任务日志 ——
export interface HealthCheck { name: string; ok: boolean; detail: string }
export const getHealth = () => get("/api/v1/health").then(j<{ checks: HealthCheck[] }>);
export const checkHealth = () => send("POST", "/api/v1/health/check").then(j<{ checks: HealthCheck[] }>);
export const getTaskLogs = (id: string) =>
  get(`/api/v1/tasks/${id}/logs`).then(j<{ logs: { label: string; content: string }[] }>);

// —— 合流 SSE hub ——
// 浏览器对同域 HTTP/1.1 只允许 ~6 个并发连接：多条 SSE（事件流 + 聊天流 + 每任务直播）
// 会占满连接池导致后续请求全部挂起。故整页只开一条 /api/v1/stream，多路复用分发。
// 断线由我们手动重连（携带游标），不用 EventSource 自动重连（它会用旧 URL 重放）。
interface OutputHandler {
  taskId: string;
  onText: (text: string, label: string) => void;
  onEnd: () => void;
}
const hubEventHandlers = new Set<(e: EventMsg) => void>();
const hubMsgHandlers = new Set<(m: AgentMessage) => void>();
const hubDeltaHandlers = new Set<(text: string) => void>();
const hubOutputHandlers = new Set<OutputHandler>();
// 每任务直播的累积缓存：中途打开/收起再展开控制台时回放已收到的输出（尾部 200KB）。
const hubOutputBuf = new Map<string, { label: string; text: string }>();
let hubES: EventSource | null = null;
let hubRetry: ReturnType<typeof setTimeout> | null = null;
let hubEvSince = 0;
let hubChatSince = 0;

function hubUsers(): number {
  return hubEventHandlers.size + hubMsgHandlers.size + hubDeltaHandlers.size + hubOutputHandlers.size;
}

function ensureHub() {
  if (hubES || hubUsers() === 0) return;
  const q = new URLSearchParams();
  if (hubEvSince) q.set("ev_since", String(hubEvSince));
  if (hubChatSince) q.set("chat_since", String(hubChatSince));
  const tok = getToken();
  if (tok) q.set("token", tok);
  const es = new EventSource(`/api/v1/stream?${q.toString()}`);
  hubES = es;
  const evDispatch = (ev: MessageEvent) => {
    try {
      const e = JSON.parse(ev.data) as EventMsg;
      if (e.id) hubEvSince = Math.max(hubEvSince, e.id);
      for (const h of hubEventHandlers) h(e);
    } catch { /* 忽略 */ }
  };
  for (const name of [...EVENT_NAMES, "agent.action", "agent.notice", "health.changed"]) {
    es.addEventListener(name, evDispatch);
  }
  es.addEventListener("agent.message", (ev: MessageEvent) => {
    try {
      const m = JSON.parse(ev.data) as AgentMessage;
      hubChatSince = Math.max(hubChatSince, m.id);
      for (const h of hubMsgHandlers) h(m);
    } catch { /* 忽略 */ }
  });
  es.addEventListener("agent.delta", (ev: MessageEvent) => {
    try {
      const d = (JSON.parse(ev.data) as { text: string }).text;
      for (const h of hubDeltaHandlers) h(d);
    } catch { /* 忽略 */ }
  });
  es.addEventListener("task.output", (ev: MessageEvent) => {
    try {
      const d = JSON.parse(ev.data) as { task_id: string; label: string; text: string };
      const buf = hubOutputBuf.get(d.task_id) || { label: d.label, text: "" };
      buf.label = d.label;
      buf.text = (buf.text + d.text).slice(-200000);
      hubOutputBuf.set(d.task_id, buf);
      for (const h of hubOutputHandlers) if (h.taskId === d.task_id) h.onText(d.text, d.label);
    } catch { /* 忽略 */ }
  });
  es.addEventListener("task.output.end", (ev: MessageEvent) => {
    try {
      const d = JSON.parse(ev.data) as { task_id: string };
      hubOutputBuf.delete(d.task_id);
      for (const h of hubOutputHandlers) if (h.taskId === d.task_id) h.onEnd();
    } catch { /* 忽略 */ }
  });
  es.onerror = () => {
    // 手动重连：关旧连接，2 秒后带最新游标重开（防自动重连用旧 URL 重放历史）。
    es.close();
    if (hubES === es) hubES = null;
    if (!hubRetry) {
      hubRetry = setTimeout(() => {
        hubRetry = null;
        ensureHub();
      }, 2000);
    }
  };
}

function releaseHub() {
  if (hubUsers() === 0 && hubES) {
    hubES.close();
    hubES = null;
  }
}

// subscribeTaskOutput 订阅任务运行中 claude 输出的实时直播（走合流 hub）。
// 订阅时先回放该任务已累积的输出（中途打开控制台不丢前文）。
export function subscribeTaskOutput(
  taskId: string,
  onText: (text: string, label: string) => void,
  onEnd: () => void,
): () => void {
  const h: OutputHandler = { taskId, onText, onEnd };
  hubOutputHandlers.add(h);
  ensureHub();
  const cached = hubOutputBuf.get(taskId);
  if (cached && cached.text) {
    const { text, label } = cached;
    queueMicrotask(() => hubOutputHandlers.has(h) && h.onText(text, label));
  }
  return () => {
    hubOutputHandlers.delete(h);
    releaseHub();
  };
}

export const EVENT_NAMES = [
  "task.created", "task.running_b", "task.awaiting_review", "task.running_c",
  "task.awaiting_pr_review", "task.running_d",
  "task.completed", "task.failed", "task.rejected", "task.skipped", "task.canceled",
  "task.restarted", "task.deleted", "task.review_escalated",
  "node.started", "node.completed", "node.failed", "node.log",
];

// subscribeEvents 打开 SSE（EventSource 无法设头，故把 token 放 query）。
export function subscribeEvents(taskId: string, onEvent: (e: EventMsg) => void): () => void {
  const t = getToken();
  const q = "since=0" + (t ? `&token=${encodeURIComponent(t)}` : "");
  const es = new EventSource(`/api/v1/tasks/${taskId}/events?${q}`);
  const handler = (ev: MessageEvent) => {
    try {
      onEvent(JSON.parse(ev.data) as EventMsg);
    } catch {
      /* 忽略心跳 */
    }
  };
  for (const name of EVENT_NAMES) es.addEventListener(name, handler);
  return () => es.close();
}

// subscribeTenantEvents 订阅租户级全局事件流（走合流 hub；任务列表/健康灯事件驱动刷新）。
export function subscribeTenantEvents(onEvent: (e: EventMsg) => void): () => void {
  hubEventHandlers.add(onEvent);
  ensureHub();
  return () => {
    hubEventHandlers.delete(onEvent);
    releaseHub();
  };
}

// —— M7 调度 Agent ——
export interface AgentMessage {
  id: number;
  role: string; // user / assistant / system
  kind: string; // chat / wake / action_request / action_result
  content: string;
  user_id?: string;
  user_email?: string;
  action_id?: number;
  task_id?: string; // 关联任务（唤醒/动作类消息），按任务过滤用
  created_at: string;
}
export interface AgentAction {
  id: number;
  tool: string;
  args_json: string;
  summary: string;
  status: string; // pending / executed / denied / failed
  result?: string;
  decided_by?: string;
  task_id?: string;
  created_at: string;
}
export const getAgentMessages = (since = 0) =>
  get(`/api/v1/agent/messages?since=${since}`).then(j<{ messages: AgentMessage[] | null; enabled: boolean }>);
export const getAgentMessagesBefore = (before: number, limit = 50) =>
  get(`/api/v1/agent/messages?before=${before}&limit=${limit}`).then(j<{ messages: AgentMessage[] | null }>);
export const sendAgentMessage = (content: string, taskId?: string) =>
  send("POST", "/api/v1/agent/messages", { content, task_id: taskId || "" }).then(j<{ message: AgentMessage }>);
export const listAgentActions = (status?: string) =>
  get("/api/v1/agent/actions" + (status ? `?status=${status}` : "")).then(j<{ actions: AgentAction[] | null }>);
export const confirmAgentAction = (id: number) =>
  send("POST", `/api/v1/agent/actions/${id}/confirm`).then(j<{ action: AgentAction }>);
export const denyAgentAction = (id: number, reason: string) =>
  send("POST", `/api/v1/agent/actions/${id}/deny`, { reason }).then(j<{ action: AgentAction }>);

// subscribeAgentStream 订阅塔台聊天流（走合流 hub）：onMessage=落库消息，onDelta=打字机增量。
// since 作为 hub 聊天游标下限（调用方已加载历史并按 id 去重，重叠无害）。
export function subscribeAgentStream(
  since: number,
  onMessage: (m: AgentMessage) => void,
  onDelta: (text: string) => void,
): () => void {
  hubChatSince = Math.max(hubChatSince, since);
  hubMsgHandlers.add(onMessage);
  hubDeltaHandlers.add(onDelta);
  ensureHub();
  return () => {
    hubMsgHandlers.delete(onMessage);
    hubDeltaHandlers.delete(onDelta);
    releaseHub();
  };
}
