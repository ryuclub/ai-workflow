export interface Repo {
  name: string;
  github: string;
  path: string;
  match: string[];
  base?: string;
  enabled?: boolean;
}

export interface Ticket {
  source: string;
  id: string;
  title: string;
  body?: string;
  status: string;
  url: string;
  assignee?: string;
  labels?: string[];
  suggested_repo?: string;
  existing_task_id?: string;
  existing_task_state?: string;
}

export type NodeKind = "auto" | "gate" | "terminal";

export interface PipelineNode {
  id: string;
  name: string;
  kind: NodeKind;
  x: number;
  y: number;
  skill?: string;
  phaseKeys?: string[];
  sourcePos?: "left" | "right" | "top" | "bottom";
  targetPos?: "left" | "right" | "top" | "bottom";
}

export interface PipelineEdge {
  from: string;
  to: string;
}

export interface Pipeline {
  id: string;
  nodes: PipelineNode[];
  edges: PipelineEdge[];
}

export interface Task {
  id: string;
  seq: number;
  source: string;
  source_id: string;
  title?: string;
  repo: string;
  pipeline_id: string;
  state: string;
  issue_url?: string;
  issue_num?: number;
  pr_url?: string;
  pr_num?: number;
  review_round?: number;
  error?: string;
  created_at: string;
  updated_at: string;
}

export interface NodeRun {
  task_id: string;
  node_id: string;
  state: string; // pending | running | ok | fail | waiting
  started_at?: string;
  ended_at?: string;
}

export interface TaskDetail {
  task: Task;
  node_runs: NodeRun[];
}

export interface EventMsg {
  id: number;
  task_id: string;
  node_id?: string;
  type: string;
  level: string;
  message: string;
  ts: string;
}

export interface Issue {
  number: number;
  title: string;
  body: string;
  url: string;
  state: string;
  labels: { name: string }[];
}

export interface SettingsView {
  source: string;
  jira_domain: string;
  jira_user: string;
  jira_project: string;
  jira_api_key_set: boolean;
  linear_team: string;
  linear_api_key_set: boolean;
  github_token_set: boolean;
  status_map: Record<string, string> | null;
  max_concurrent: number;
  task_timeout_min: number;
}
