import { useSortable } from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
import { CalendarDays, Circle, CircleCheck, CircleDot, CircleEllipsis, GripVertical, Inbox, Sparkles } from "lucide-react";
import type { Task, TaskStatus } from "../lib/api";
import { formatDate, isOverdue, priorityLabel, statusLabel } from "../lib/format";
import { ProjectDot } from "./project-dot";

interface TaskCardProps {
  task: Task;
  onOpen: (taskID: number) => void;
  onStatusChange: (taskID: number, status: TaskStatus) => void;
  pending?: boolean;
}

export function TaskCard({ task, onOpen, onStatusChange, pending = false }: TaskCardProps) {
  const { attributes, listeners, setNodeRef, setActivatorNodeRef, transform, transition, isDragging } = useSortable({ id: task.id, data: { task }, disabled: pending });
  const StatusIcon = { inbox: Inbox, todo: Circle, doing: CircleDot, review: CircleEllipsis, done: CircleCheck }[task.status];
  return <article ref={setNodeRef} style={{ transform: CSS.Transform.toString(transform), transition }} className={`task-card ${isDragging ? "task-card-dragging" : ""}`}>
    <button className="task-card-open" type="button" onClick={() => onOpen(task.id)} aria-label={`打开任务 ${task.title}`}><TaskCardBody task={task} /></button>
    <div className="task-card-controls">
      <span className={`task-status-control task-status-${task.status}`} title={`${statusLabel(task.status)} · 点击更改状态`}><StatusIcon size={17} aria-hidden /><select className="task-status-select" aria-label={`更改状态：${task.title}`} value={task.status} disabled={pending} onChange={(event) => onStatusChange(task.id, event.target.value as TaskStatus)}>{(["inbox", "todo", "doing", "review", "done"] as TaskStatus[]).map((status) => <option key={status} value={status}>{statusLabel(status)}</option>)}</select></span>
      <button ref={setActivatorNodeRef} className="task-drag-handle" type="button" disabled={pending} {...attributes} {...listeners} aria-label={`拖动任务 ${task.title}`} title="拖动排序；空格选中后用方向键移动，Esc 取消"><GripVertical size={16} aria-hidden /></button>
    </div>
  </article>;
}

export function TaskCardBody({ task }: { task: Task }) {
  return <>
    {task.project || task.priority > 0 ? <span className="task-card-topline">{task.project ? <span className="task-project"><ProjectDot color={task.project.color} size="small" />{task.project.name}</span> : null}{task.priority > 0 ? <span className={`priority-badge priority-${task.priority}`} aria-label={`${priorityLabel(task.priority)}优先级`}>{priorityLabel(task.priority)}</span> : null}</span> : null}
    <span className="task-card-title">{task.title}</span>
    {task.tags.length > 0 ? <span className="task-tags">{task.tags.slice(0, 4).map((tag) => <span className="tag-chip" key={tag.id}>#{tag.name}</span>)}</span> : null}
    {task.due_at || task.agent_ready ? <span className="task-card-meta">{task.due_at ? <span className={`task-due ${task.status !== "done" && isOverdue(task.due_at) ? "task-due-overdue" : ""}`}><CalendarDays size={13} aria-hidden />{formatDate(task.due_at)}</span> : null}{task.agent_ready ? <span className="agent-badge" title="Agent 可领取"><Sparkles size={12} aria-hidden />Agent 可领取</span> : null}</span> : null}
  </>;
}
