import type { ProjectColor, TaskPriority, TaskStatus } from "./api";

const dateFormatter = new Intl.DateTimeFormat("zh-CN", {
  month: "short",
  day: "numeric",
});

const dateTimeFormatter = new Intl.DateTimeFormat("zh-CN", {
  month: "short",
  day: "numeric",
  hour: "2-digit",
  minute: "2-digit",
});

export function formatDate(value: string | null): string {
  if (!value) {
    return "";
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return "";
  }
  return dateFormatter.format(date);
}

export function formatDateTime(value: string | null): string {
  if (!value) {
    return "";
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return "";
  }
  return dateTimeFormatter.format(date);
}

export function dateInputValue(value: string | null): string {
  if (!value) {
    return "";
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return "";
  }
  const year = date.getFullYear();
  const month = String(date.getMonth() + 1).padStart(2, "0");
  const day = String(date.getDate()).padStart(2, "0");
  return `${year}-${month}-${day}`;
}

export function dueDateFromInput(value: string): string | null {
  if (!value) {
    return null;
  }
  const parts = value.split("-").map(Number);
  if (parts.length !== 3 || parts.some((part) => Number.isNaN(part))) {
    return null;
  }
  const date = new Date(parts[0], parts[1] - 1, parts[2], 23, 59, 59);
  return date.toISOString();
}

export function isOverdue(value: string | null): boolean {
  if (!value) {
    return false;
  }
  return new Date(value).getTime() < Date.now();
}

export function priorityLabel(priority: TaskPriority): string {
  switch (priority) {
    case 1:
      return "低";
    case 2:
      return "中";
    case 3:
      return "高";
    default:
      return "无";
  }
}

export function statusLabel(status: TaskStatus): string {
  switch (status) {
    case "inbox":
      return "收件箱";
    case "todo":
      return "待办";
    case "doing":
      return "进行中";
    case "review":
      return "待验收";
    case "done":
      return "已完成";
  }
}

export function titleForFilter(filter: BoardFilter): string {
  switch (filter.kind) {
    case "inbox":
      return "收件箱";
    case "project":
      return `@${filter.name}`;
    case "tag":
      return `#${filter.name}`;
    default:
      return "全部任务";
  }
}

export interface ProjectFilter {
  kind: "project";
  id: number;
  name: string;
  color: ProjectColor;
}

export interface TagFilter {
  kind: "tag";
  id: number;
  name: string;
  color: ProjectColor;
}

export type BoardFilter =
  | { kind: "all" }
  | { kind: "inbox" }
  | ProjectFilter
  | TagFilter;
