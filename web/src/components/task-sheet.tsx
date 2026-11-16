import { useEffect, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { MessageSquare, Trash2 } from "lucide-react";

import {
  api,
  errorMessage,
  type Project,
  type Task,
  type TaskDetailResponse,
  type TaskPriority,
  type TaskStatus,
  type TasksResponse,
} from "../lib/api";
import {
  dateInputValue,
  dueDateFromInput,
  formatDateTime,
  priorityLabel,
  statusLabel,
} from "../lib/format";
import { Button } from "./ui/button";
import { Dialog } from "./ui/dialog";
import { Input } from "./ui/input";
import { useToast } from "./toast";
import { safeExternalURL } from "../lib/integrations";
import { invalidateTaskData } from "../app/task-cache";

const statuses: TaskStatus[] = ["inbox", "todo", "doing", "review", "done"];
const priorities: TaskPriority[] = [0, 1, 2, 3];

interface TaskSheetProps {
  taskID: number;
  projects: Project[];
  onClose: () => void;
}

export function TaskSheet({ taskID, projects, onClose }: TaskSheetProps) {
  const queryClient = useQueryClient();
  const { notify } = useToast();
  const detailQuery = useQuery<TaskDetailResponse>({
    queryKey: ["task", taskID],
    queryFn: ({ signal }) => api<TaskDetailResponse>(`/tasks/${taskID}`, { signal }),
    enabled: Number.isFinite(taskID),
  });
  const task = detailQuery.data?.task;

  const [title, setTitle] = useState("");
  const [notes, setNotes] = useState("");
  const [projectID, setProjectID] = useState("");
  const [priority, setPriority] = useState<TaskPriority>(0);
  const [dueDate, setDueDate] = useState("");
  const [agentReady, setAgentReady] = useState(false);
  const [tagNames, setTagNames] = useState("");
  const [comment, setComment] = useState("");
  const [loadedTaskID, setLoadedTaskID] = useState<number | null>(null);

  useEffect(() => {
    if (!task || task.id !== taskID || loadedTaskID === taskID) {
      return;
    }
    setTitle(task.title);
    setNotes(task.notes);
    setProjectID(task.project_id === null ? "" : String(task.project_id));
    setPriority(task.priority);
    setDueDate(dateInputValue(task.due_at));
    setAgentReady(task.agent_ready);
    setTagNames(task.tags.map((tag) => tag.name).join(", "));
    setLoadedTaskID(taskID);
  }, [loadedTaskID, task, taskID]);

  const saveMutation = useMutation({
    mutationFn: () =>
      api<Task>(`/tasks/${taskID}`, {
        method: "PATCH",
        body: JSON.stringify({
          title,
          notes,
          project_id: projectID === "" ? null : Number(projectID),
          priority,
          due_at: dueDate ? dueDateFromInput(dueDate) : null,
          agent_ready: agentReady,
          tags: tagNames
            .split(",")
            .map((name) => name.trim().replace(/^#/, ""))
            .filter(Boolean),
        }),
      }),
    onSuccess: (updated) => {
      updateTaskCache(queryClient, updated);
      notify("已保存", "success");
    },
    onError: (error) => notify(errorMessage(error), "error"),
  });

  const moveMutation = useMutation({
    mutationFn: (status: TaskStatus) =>
      api<Task>(`/tasks/${taskID}/move`, {
        method: "POST",
        body: JSON.stringify({ status }),
      }),
    onSuccess: (updated) => {
      updateTaskCache(queryClient, updated);
    },
    onError: (error) => notify(errorMessage(error), "error"),
  });

  const deleteMutation = useMutation({
    mutationFn: () => api<void>(`/tasks/${taskID}`, { method: "DELETE" }),
    onSuccess: () => {
      queryClient.removeQueries({ queryKey: ["task", taskID] });
      void invalidateTaskData(queryClient);
      notify("任务已移入回收站", "success");
      onClose();
    },
    onError: (error) => notify(errorMessage(error), "error"),
  });

  const commentMutation = useMutation({
    mutationFn: () =>
      api(`/tasks/${taskID}/comments`, {
        method: "POST",
        body: JSON.stringify({ text: comment }),
      }),
    onSuccess: () => {
      setComment("");
      void invalidateTaskData(queryClient);
      notify("评论已添加", "success");
    },
    onError: (error) => notify(errorMessage(error), "error"),
  });

  const comments = useMemo(
    () => detailQuery.data?.events.filter((event) => event.kind === "commented") ?? [],
    [detailQuery.data?.events],
  );

  return (
    <Dialog
      open
      onClose={onClose}
      title={task?.title ?? "任务详情"}
      subtitle={task ? `${statusLabel(task.status)} · 任务 #${task.id}` : "正在读取…"}
      className="task-sheet"
      footer={
        <div className="dialog-actions sheet-actions">
          <Button
            variant="quiet"
            type="button"
            onClick={() => {
              if (window.confirm("确定把这张卡移入回收站吗？")) {
                deleteMutation.mutate();
              }
            }}
            disabled={deleteMutation.isPending}
          >
            <Trash2 size={14} aria-hidden />
            删除
          </Button>
          <div className="sheet-actions-right">
            <Button variant="quiet" type="button" onClick={onClose}>
              关闭
            </Button>
            <Button
              type="button"
              onClick={() => saveMutation.mutate()}
              disabled={saveMutation.isPending || !title.trim()}
            >
              {saveMutation.isPending ? "保存中…" : "保存"}
            </Button>
          </div>
        </div>
      }
    >
      {detailQuery.isPending ? <p className="sheet-loading">正在打开这张卡…</p> : null}
      {detailQuery.isError ? (
        <p className="form-error" role="alert">
          {errorMessage(detailQuery.error)}
        </p>
      ) : null}
      {task ? (
        <div className="sheet-grid">
          <div className="form-stack sheet-main">
            <div className="field">
              <label htmlFor="task-title">标题</label>
              <Input
                id="task-title"
                value={title}
                onChange={(event) => setTitle(event.target.value)}
                maxLength={500}
              />
            </div>

            <div className="field">
              <span className="field-label">状态</span>
              <div className="status-picker" role="group" aria-label="任务状态">
                {statuses.map((status) => (
                  <button
                    key={status}
                    className={task.status === status ? "status-choice status-choice-active" : "status-choice"}
                    type="button"
                    onClick={() => moveMutation.mutate(status)}
                    disabled={moveMutation.isPending || task.status === status}
                  >
                    {statusLabel(status)}
                  </button>
                ))}
              </div>
            </div>

            <div className="form-row">
              <div className="field">
                <label htmlFor="task-project">项目</label>
                <select
                  id="task-project"
                  value={projectID}
                  onChange={(event) => setProjectID(event.target.value)}
                >
                  <option value="">未分类</option>
                  {projects.map((project) => (
                    <option key={project.id} value={project.id}>
                      {project.name}
                    </option>
                  ))}
                </select>
              </div>
              <div className="field">
                <label htmlFor="task-priority">优先级</label>
                <select
                  id="task-priority"
                  value={priority}
                  onChange={(event) => setPriority(Number(event.target.value) as TaskPriority)}
                >
                  {priorities.map((value) => (
                    <option key={value} value={value}>
                      {priorityLabel(value)}
                    </option>
                  ))}
                </select>
              </div>
              <div className="field">
                <label htmlFor="task-due">截止</label>
                <Input
                  id="task-due"
                  type="date"
                  value={dueDate}
                  onChange={(event) => setDueDate(event.target.value)}
                />
              </div>
            </div>

            <div className="field">
              <label htmlFor="task-tags">标签</label>
              <Input
                id="task-tags"
                value={tagNames}
                onChange={(event) => setTagNames(event.target.value)}
                placeholder="用逗号分隔，例如 bug, 后端"
              />
            </div>

            <label className="check-row">
              <input
                type="checkbox"
                checked={agentReady}
                onChange={(event) => setAgentReady(event.target.checked)}
              />
              <span>
                <strong>允许 agent 领取</strong>
                <small>标记为 agent_ready，脚本可以取走这张卡。</small>
              </span>
            </label>

            <div className="field">
              <label htmlFor="task-notes">备注</label>
              <textarea
                id="task-notes"
                value={notes}
                onChange={(event) => setNotes(event.target.value)}
                rows={7}
                placeholder="补充上下文、链接或下一步…"
              />
            </div>
          </div>

          <aside className="sheet-side">
            {detailQuery.data?.links.length ? <section className="timeline-card"><div className="timeline-heading">关联链接</div><div className="task-links">{detailQuery.data.links.map((link) => {
              const url = safeExternalURL(link.url);
              const label = link.kind === "codeg_work_task" ? "Codeg 任务" : link.kind === "github_issue" ? "GitHub Issue" : link.kind;
              return url ? <a key={link.id} href={url} target="_blank" rel="noopener noreferrer">{label} · {link.external_id}</a> : <span className="muted-copy" key={link.id}>{label} · {link.external_id}</span>;
            })}</div></section> : null}
            <section className="timeline-card">
              <div className="timeline-heading">
                <span>时间线</span>
                <span>{detailQuery.data?.events.length ?? 0}</span>
              </div>
              <div className="timeline">
                {(detailQuery.data?.events ?? []).slice(-12).map((event) => (
                  <div className="timeline-item" key={event.id}>
                    <span className="timeline-dot" aria-hidden />
                    <div>
                      <p>{eventLabel(event.kind)}</p>
                      <time>{formatDateTime(event.created_at)}</time>
                    </div>
                  </div>
                ))}
              </div>
            </section>

            <section className="comment-card">
              <div className="timeline-heading">
                <span className="inline-heading">
                  <MessageSquare size={14} aria-hidden />
                  评论
                </span>
                <span>{comments.length}</span>
              </div>
              <div className="comment-list">
                {comments.length === 0 ? (
                  <p className="muted-copy">暂无评论。</p>
                ) : (
                  comments.map((event) => {
                    const text =
                      typeof event.payload.text === "string" ? event.payload.text : "";
                    return (
                      <div className="comment-item" key={event.id}>
                        <p>{text}</p>
                        <time>{formatDateTime(event.created_at)}</time>
                      </div>
                    );
                  })
                )}
              </div>
              <form
                className="comment-form"
                onSubmit={(event) => {
                  event.preventDefault();
                  if (comment.trim()) {
                    commentMutation.mutate();
                  }
                }}
              >
                <textarea
                  value={comment}
                  onChange={(event) => setComment(event.target.value)}
                  rows={2}
                  placeholder="写一条评论…"
                  aria-label="评论"
                />
                <button type="submit" disabled={!comment.trim() || commentMutation.isPending}>
                  发送
                </button>
              </form>
            </section>
          </aside>
        </div>
      ) : null}
    </Dialog>
  );
}

function updateTaskCache(
  queryClient: ReturnType<typeof useQueryClient>,
  updated: Task,
) {
  queryClient.setQueryData<TasksResponse>(["tasks"], (current) => {
    if (!current) {
      return current;
    }
    return {
      ...current,
      tasks: current.tasks.map((task) => (task.id === updated.id ? updated : task)),
    };
  });
  void invalidateTaskData(queryClient);
}

function eventLabel(kind: string): string {
  switch (kind) {
    case "created":
      return "创建了任务";
    case "moved":
      return "移动了任务";
    case "updated":
      return "更新了任务";
    case "commented":
      return "追加了评论";
    case "deleted":
      return "移入回收站";
    default:
      return kind;
  }
}
