import type { QueryClient } from "@tanstack/react-query";

const taskDataKeys = ["tasks", "projects", "tags", "trash", "task"];

export function cancelTaskDataRequests(client: QueryClient): Promise<void[]> {
  return Promise.all(taskDataKeys.map((key) => client.cancelQueries({ queryKey: [key] })));
}

export function invalidateTaskData(client: QueryClient): Promise<void[]> {
  return Promise.all(taskDataKeys.map((key) =>
    client.invalidateQueries({ queryKey: [key] }),
  ));
}
