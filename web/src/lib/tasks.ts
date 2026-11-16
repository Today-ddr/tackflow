import { api, type Task, type TasksResponse } from "./api";

export async function fetchAllTasks(signal: AbortSignal): Promise<TasksResponse> {
  const tasks = new Map<number, Task>();
  const cursors = new Set<string>();
  let cursor: string | null = null;
  do {
    signal.throwIfAborted();
    const params = new URLSearchParams({ limit: "500" });
    if (cursor) params.set("cursor", cursor);
    const page = await api<TasksResponse>(`/tasks?${params}`, { signal });
    for (const task of page.tasks) tasks.set(task.id, task);
    cursor = page.next_cursor;
    if (cursor) {
      if (cursors.has(cursor)) throw new Error("任务分页游标重复，请重试");
      cursors.add(cursor);
    }
  } while (cursor);
  signal.throwIfAborted();
  return { tasks: [...tasks.values()], next_cursor: null };
}
