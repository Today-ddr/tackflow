import type { BoardFilter } from "./format";
import type { Task } from "./api";

export function filterTasks(
  tasks: Task[],
  filter: BoardFilter,
  query: string,
): Task[] {
  const needle = query.trim().toLowerCase();
  return tasks.filter((task) => {
    if (filter.kind === "inbox" && task.status !== "inbox") {
      return false;
    }
    if (filter.kind === "project" && task.project_id !== filter.id) {
      return false;
    }
    if (filter.kind === "tag" && !task.tags.some((tag) => tag.id === filter.id)) {
      return false;
    }
    if (!needle) {
      return true;
    }
    return (
      task.title.toLowerCase().includes(needle) ||
      task.notes.toLowerCase().includes(needle) ||
      task.tags.some((tag) => tag.name.toLowerCase().includes(needle)) ||
      task.project?.name.toLowerCase().includes(needle)
    );
  });
}
