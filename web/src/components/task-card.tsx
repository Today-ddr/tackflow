import { useSortable } from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
import { CalendarDays, Sparkles } from "lucide-react";

import type { Task } from "../lib/api";
import { formatDate, isOverdue, priorityLabel } from "../lib/format";
import { ProjectDot } from "./project-dot";

interface TaskCardProps {
  task: Task;
  onOpen: (taskID: number) => void;
}

export function TaskCard({ task, onOpen }: TaskCardProps) {
  const {
    attributes,
    listeners,
    setNodeRef,
    transform,
    transition,
    isDragging,
  } = useSortable({
    id: task.id,
    data: { task },
  });

  return (
    <article
      ref={setNodeRef}
      style={{
        transform: CSS.Transform.toString(transform),
        transition,
      }}
      className={`task-card ${isDragging ? "task-card-dragging" : ""}`}
      onClick={() => onOpen(task.id)}
      onKeyDown={(event) => {
        if (event.key === "Enter" || event.key === " ") {
          event.preventDefault();
          onOpen(task.id);
        }
      }}
      aria-label={`打开任务 ${task.title}`}
      {...attributes}
      {...listeners}
    >
      <TaskCardBody task={task} />
    </article>
  );
}

export function TaskCardBody({ task }: { task: Task }) {
  return (
    <>
      <div className="task-card-topline">
        {task.project ? (
          <span className="task-project">
            <ProjectDot color={task.project.color} size="small" />
            {task.project.name}
          </span>
        ) : (
          <span className="task-project task-project-empty">未分类</span>
        )}
        {task.priority > 0 ? (
          <span className={`priority-badge priority-${task.priority}`}>
            {priorityLabel(task.priority)}
          </span>
        ) : null}
      </div>
      <h3>{task.title}</h3>
      {task.tags.length > 0 ? (
        <div className="task-tags">
          {task.tags.slice(0, 4).map((tag) => (
            <span className="tag-chip" key={tag.id}>
              #{tag.name}
            </span>
          ))}
        </div>
      ) : null}
      <div className="task-card-meta">
        {task.due_at ? (
          <span className={`task-due ${isOverdue(task.due_at) ? "task-due-overdue" : ""}`}>
            <CalendarDays size={13} aria-hidden />
            {formatDate(task.due_at)}
          </span>
        ) : (
          <span className="task-card-id">#{task.id}</span>
        )}
        {task.agent_ready ? (
          <span className="agent-badge" title="Agent 可领取">
            <Sparkles size={12} aria-hidden />
            agent
          </span>
        ) : null}
      </div>
    </>
  );
}
