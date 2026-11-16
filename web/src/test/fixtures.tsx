import type { ReactElement } from "react";
import { QueryClientProvider } from "@tanstack/react-query";
import { render } from "@testing-library/react";
import { createQueryClient } from "../app/query";
import { ToastProvider } from "../components/toast";
import type { Me, Project, Tag, Task } from "../lib/api";

export const me: Me = { user: { username: "admin" }, auth_kind: "session", sudo_expires_at: null };
export const project: Project = { id: 1, name: "Codeg", color: "sage", position: 1024, archived_at: null, created_at: "2026-11-16T06:00:00Z", task_count: 1 };
export const tag: Tag = { id: 2, name: "bug", color: "clay", created_at: "2026-11-16T06:00:00Z", task_count: 1 };

export function task(overrides: Partial<Task> = {}): Task {
  return { id: 1, project_id: 1, title: "修复登录", notes: "浏览器", status: "todo", priority: 1, position: 1024, due_at: null, agent_ready: false, created_at: "2026-11-16T06:00:00Z", updated_at: "2026-11-16T06:00:00Z", completed_at: null, project, tags: [tag], ...overrides };
}

export function json(body: unknown, status = 200): Response {
  return new Response(status === 204 ? null : JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

export function renderWithClient(element: ReactElement, client = createQueryClient()) {
  return { ...render(<QueryClientProvider client={client}><ToastProvider>{element}</ToastProvider></QueryClientProvider>), client };
}
