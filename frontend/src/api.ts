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

// —— Admin token（鉴权开启时使用）——
const TOKEN_KEY = "wf.admin.token";
export const getToken = () => localStorage.getItem(TOKEN_KEY) || "";
export const setToken = (t: string) => localStorage.setItem(TOKEN_KEY, t);
export const clearToken = () => localStorage.removeItem(TOKEN_KEY);

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
  fetch("/api/v1/auth/status").then(j<{ auth_required: boolean }>);

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
