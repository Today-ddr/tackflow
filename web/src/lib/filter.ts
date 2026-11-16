import type { BoardFilter } from "./format";
import type { Task } from "./api";

export function filterTasks(
  tasks: Task[],
  filter: BoardFilter,
  query: string,
): Task[] {
  const terms = query.trim().toLowerCase().match(/(?:[^\s"]+|"[^"]*")+/g) ?? [];
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
    return terms.every((term) => {
      const prefix = /^(status|tag|project):(.*)$/.exec(term);
      const needle = (prefix?.[2] ?? term).replace(/^"|"$/g, "");
      if (prefix?.[1] === "status") return task.status === needle;
      if (prefix?.[1] === "tag") return task.tags.some((tag) => tag.name.toLowerCase() === needle);
      if (prefix?.[1] === "project") return task.project?.name.toLowerCase() === needle;
      return task.title.toLowerCase().includes(needle) ||
        task.notes.toLowerCase().includes(needle) ||
        task.tags.some((tag) => tag.name.toLowerCase().includes(needle)) ||
        Boolean(task.project?.name.toLowerCase().includes(needle));
    });
  });
}
