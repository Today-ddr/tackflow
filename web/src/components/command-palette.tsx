import { useEffect, useMemo, useRef, useState } from "react";
import { CornerDownLeft, Plus, Search, Settings } from "lucide-react";

import type { Task } from "../lib/api";
import { formatDate, priorityLabel } from "../lib/format";
import { filterTasks } from "../lib/filter";
import { ProjectDot } from "./project-dot";

interface CommandPaletteProps {
  tasks: Task[];
  onClose: () => void;
  onOpenTask: (taskID: number) => void;
  onOpenCapture: () => void;
  onOpenSettings: () => void;
}

export function CommandPalette({
  tasks,
  onClose,
  onOpenTask,
  onOpenCapture,
  onOpenSettings,
}: CommandPaletteProps) {
  const [query, setQuery] = useState("");
  const [selected, setSelected] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);
  const needle = query.trim().toLowerCase();

  const results = useMemo(() => {
    if (!needle) {
      return tasks
        .filter((task) => task.status !== "done")
        .sort((left, right) => right.updated_at.localeCompare(left.updated_at))
        .slice(0, 8);
    }
    return filterTasks(tasks, { kind: "all" }, needle).slice(0, 12);
  }, [needle, tasks]);

  useEffect(() => {
    inputRef.current?.focus();
  }, []);

  useEffect(() => {
    setSelected(0);
  }, [query]);

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.preventDefault();
        onClose();
      }
      if (event.key === "ArrowDown") {
        event.preventDefault();
        setSelected((value) => Math.min(value + 1, Math.max(results.length - 1, 0)));
      }
      if (event.key === "ArrowUp") {
        event.preventDefault();
        setSelected((value) => Math.max(value - 1, 0));
      }
      if (event.key === "Enter" && results[selected]) {
        event.preventDefault();
        onOpenTask(results[selected].id);
        onClose();
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [onClose, onOpenTask, results, selected]);

  return (
    <div
      className="command-backdrop"
      role="presentation"
      onMouseDown={(event) => {
        if (event.target === event.currentTarget) {
          onClose();
        }
      }}
    >
      <section className="command-palette" role="dialog" aria-modal="true" aria-label="搜索任务">
        <div className="command-input-row">
          <Search size={18} aria-hidden />
          <input
            ref={inputRef}
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="搜索，或用 status: / tag: / project:"
            aria-label="搜索任务"
          />
          <kbd>ESC</kbd>
        </div>
        {!needle ? (
          <div className="command-section">
            <p className="command-label">快速动作</p>
            <button
              className="command-action"
              type="button"
              onClick={() => {
                onOpenCapture();
                onClose();
              }}
            >
              <Plus size={16} aria-hidden />
              <span>记一笔</span>
              <CornerDownLeft size={13} aria-hidden />
            </button>
            <button
              className="command-action"
              type="button"
              onClick={() => {
                onOpenSettings();
                onClose();
              }}
            >
              <Settings size={16} aria-hidden />
              <span>打开设置</span>
            </button>
          </div>
        ) : null}
        <div className="command-results">
          <p className="command-label">{needle ? "搜索结果" : "最近任务"}</p>
          {results.length === 0 ? (
            <p className="command-empty">没有匹配的任务。</p>
          ) : (
            results.map((task, index) => (
              <button
                className={`command-result ${selected === index ? "command-result-active" : ""}`}
                type="button"
                key={task.id}
                onMouseEnter={() => setSelected(index)}
                onClick={() => {
                  onOpenTask(task.id);
                  onClose();
                }}
              >
                <span className="command-result-main">
                  {task.project ? (
                    <ProjectDot color={task.project.color} size="small" />
                  ) : (
                    <span className="project-dot project-dot-small color-ink" aria-hidden />
                  )}
                  <span>
                    <strong>{task.title}</strong>
                    <small>
                      {task.project?.name ?? "未分类"}
                      {task.due_at ? ` · ${formatDate(task.due_at)}` : ""}
                    </small>
                  </span>
                </span>
                {task.priority > 0 ? (
                  <span className={`priority-badge priority-${task.priority}`}>
                    {priorityLabel(task.priority)}
                  </span>
                ) : null}
              </button>
            ))
          )}
        </div>
      </section>
    </div>
  );
}
