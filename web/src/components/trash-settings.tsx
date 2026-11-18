import { useInfiniteQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { Trash2 } from "lucide-react";
import { api, errorMessage, type Task, type TrashResponse } from "../lib/api";
import { invalidateTaskData } from "../app/task-cache";
import { formatDateTime } from "../lib/format";
import { QueryFeedback } from "./query-feedback";
import { Button } from "./ui/button";
import { SettingsHeading } from "./settings-heading";

export function TrashSettings() {
  const client = useQueryClient();
  const query = useInfiniteQuery({
    queryKey: ["trash"],
    initialPageParam: "",
    queryFn: ({ pageParam, signal }) => api<TrashResponse>(`/trash?${new URLSearchParams({ limit: "100", cursor: pageParam })}`, { signal }),
    getNextPageParam: (lastPage, _pages, _lastParam, params) => lastPage.next_cursor && !params.includes(lastPage.next_cursor) ? lastPage.next_cursor : undefined,
  });
  const restore = useMutation({
    mutationFn: (id: number) => api<Task>(`/tasks/${id}/restore`, { method: "POST" }),
    onSuccess: () => invalidateTaskData(client),
  });
  const tasks = [...new Map(query.data?.pages.flatMap((page) => page.tasks).map((task) => [task.id, task]) ?? []).values()];
  const lastCursor = query.data?.pages.at(-1)?.next_cursor;
  const cursorError = lastCursor && query.data?.pageParams.includes(lastCursor) ? new Error("回收站分页游标重复，请重试") : null;
  return <section className="settings-card">
    <SettingsHeading icon={Trash2} title="回收站" />
    <p className="settings-copy">删除的任务保留 30 天，到期后永久清除。恢复会保留任务的项目、标签和记录。</p>
    <QueryFeedback pending={query.isFetching} error={query.error ?? cursorError} retry={() => { if (query.isFetchNextPageError) void query.fetchNextPage(); else void query.refetch(); }} />
    {restore.error ? <p className="form-error" role="alert">{errorMessage(restore.error)}</p> : null}
    {query.isSuccess && tasks.length === 0 ? <p className="muted-copy">回收站是空的。</p> : null}
    <div className="management-list">{tasks.map((task) => <div className="management-item" key={task.id}>
      <div className="management-main"><strong>{task.title}</strong><small>删除于 {formatDateTime(task.deleted_at)}</small></div>
      <Button variant="quiet" type="button" aria-label={`恢复任务 ${task.title}`} disabled={restore.isPending} onClick={() => restore.mutate(task.id)}>{restore.isPending && restore.variables === task.id ? "恢复中…" : "恢复"}</Button>
    </div>)}</div>
    {query.hasNextPage && !query.isFetchNextPageError ? <Button variant="quiet" type="button" disabled={query.isFetching} onClick={() => void query.fetchNextPage()}>{query.isFetchingNextPage ? "读取中…" : "加载更多"}</Button> : null}
  </section>;
}
