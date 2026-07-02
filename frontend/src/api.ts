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
export interface Membership { user_id: string; tenant_id: string; role: string }
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

export const EVENT_NAMES = [
  "task.created", "task.running_b", "task.awaiting_review", "task.running_c",
  "task.awaiting_pr_review", "task.running_d",
  "task.completed", "task.failed", "task.rejected", "task.skipped", "task.canceled",
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
