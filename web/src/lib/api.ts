export type TaskStatus = "inbox" | "todo" | "doing" | "review" | "done";
export type TaskPriority = 0 | 1 | 2 | 3;
export type ProjectColor = "sage" | "clay" | "ink" | "sky" | "plum";

export interface LoginResponse {
  user: {
    username: string;
  };
}

export interface Me {
  user: {
    username: string;
  };
  auth_kind: "session" | "token";
  sudo_expires_at: string | null;
}

export interface Project {
  id: number;
  name: string;
  color: ProjectColor;
  position: number;
  archived_at: string | null;
  created_at: string;
  task_count: number;
}

export interface Tag {
  id: number;
  name: string;
  color: ProjectColor;
  created_at: string;
  task_count: number;
}

export interface Task {
  id: number;
  project_id: number | null;
  title: string;
  notes: string;
  status: TaskStatus;
  priority: TaskPriority;
  position: number;
  due_at: string | null;
  agent_ready: boolean;
  created_at: string;
  updated_at: string;
  completed_at: string | null;
  project: Project | null;
  tags: Tag[];
}

export interface TaskEvent {
  id: number;
  kind: string;
  payload: Record<string, unknown>;
  created_at: string;
}

export interface TaskLink {
  id: number;
  kind: string;
  external_id: string;
  url: string | null;
  created_at: string;
}

export interface TaskDetailResponse {
  task: Task;
  events: TaskEvent[];
  links: TaskLink[];
}

export interface TasksResponse {
  tasks: Task[];
  next_cursor: string | null;
}

export interface TrashResponse {
  tasks: Array<Task & { deleted_at: string }>;
  next_cursor: string | null;
}

export interface UpdateStatus {
  current_version: string;
  latest_version: string | null;
  update_available: boolean;
  interval_hours: number;
  last_checked_at: string | null;
  last_error: string;
  release_body: string;
  release_url: string;
  published_at: string | null;
  configured: boolean;
  updater?: {
    current_tag: string;
    previous_tag: string;
    status: string;
    message: string;
    updated_at: string;
    backup_path: string;
  };
}

export interface Settings {
  update_interval_hours: number;
  timezone: string;
}

export interface APIToken {
  id: number;
  name: string;
  prefix: string;
  scopes: string[];
  project_ids: number[];
  tag_ids: number[];
  created_at: string;
  last_used_at: string | null;
  expires_at?: string | null;
}

export interface SessionInfo {
  id: string;
  created_at: string;
  last_seen_at: string;
  expires_at: string;
  user_agent: string;
  ip: string;
  current: boolean;
  sudo_until: string | null;
}

interface ErrorBody {
  error?: {
    code?: string;
    message?: string;
    request_id?: string;
  };
}

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly requestId: string;

  constructor(status: number, code: string, message: string, requestId = "") {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
    this.requestId = requestId;
  }
}

export async function api<T>(
  path: string,
  init: RequestInit = {},
): Promise<T> {
  const headers = new Headers(init.headers);
  headers.set("X-Tack-Client", "web");
  if (init.body && !headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json");
  }

  const response = await fetch(`/api/v1${path}`, {
    ...init,
    headers,
    credentials: headers.has("Authorization") ? "omit" : "same-origin",
  });

  if (!response.ok) {
    let body: ErrorBody = {};
    try {
      body = (await response.json()) as ErrorBody;
    } catch {
      // Keep the HTTP status as the source of truth when the body is not JSON.
    }
    throw new ApiError(
      response.status,
      body.error?.code ?? "request_failed",
      body.error?.message ?? "请求失败，请稍后重试",
      body.error?.request_id,
    );
  }

  if (response.status === 204) {
    return undefined as T;
  }
  return (await response.json()) as T;
}

export async function downloadExport(format: "json" | "csv"): Promise<void> {
  const response = await fetch(`/api/v1/export?format=${format}`, {
    headers: { "X-Tack-Client": "web" },
    credentials: "same-origin",
  });
  if (!response.ok) {
    throw new ApiError(response.status, "export_failed", "导出失败");
  }
  const blob = await response.blob();
  const objectURL = URL.createObjectURL(blob);
  const anchor = document.createElement("a");
  anchor.href = objectURL;
  anchor.download = `tack-export.${format}`;
  document.body.append(anchor);
  anchor.click();
  anchor.remove();
  URL.revokeObjectURL(objectURL);
}

export function errorMessage(error: unknown): string {
  if (error instanceof ApiError) {
    return error.message;
  }
  if (error instanceof Error && error.message) {
    return error.message;
  }
  return "操作失败，请稍后重试";
}
