import { useMemo, useState } from "react";
import {
  DndContext,
  DragOverlay,
  KeyboardSensor,
  PointerSensor,
  closestCorners,
  useDroppable,
  useSensor,
  useSensors,
  type DragEndEvent,
  type DragStartEvent,
} from "@dnd-kit/core";
import {
  SortableContext,
  sortableKeyboardCoordinates,
  verticalListSortingStrategy,
} from "@dnd-kit/sortable";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ChevronDown, Plus, Search } from "lucide-react";

import {
  api,
  errorMessage,
  type Task,
  type TaskStatus,
  type TasksResponse,
} from "../lib/api";
import { filterTasks } from "../lib/filter";
import {
  statusLabel,
  titleForFilter,
  type BoardFilter,
} from "../lib/format";
import { TaskCard, TaskCardBody } from "./task-card";
import { useToast } from "./toast";

const statusOrder: TaskStatus[] = ["inbox", "todo", "doing", "review", "done"];

interface BoardProps {
  tasks: Task[];
  filter: BoardFilter;
  query: string;
  onOpenTask: (taskID: number) => void;
  onOpenPalette: () => void;
  onOpenCapture: () => void;
  onClearFilter: () => void;
}

export function Board({
  tasks,
  filter,
  query,
  onOpenTask,
  onOpenPalette,
  onOpenCapture,
  onClearFilter,
}: BoardProps) {
  const queryClient = useQueryClient();
  const { notify } = useToast();
  const [activeTask, setActiveTask] = useState<Task | null>(null);
  const [doneOpen, setDoneOpen] = useState(false);
  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 6 } }),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }),
  );

  const visibleTasks = useMemo(
    () => filterTasks(tasks, filter, query),
    [tasks, filter, query],
  );

  const statuses = useMemo(() => {
    if (filter.kind === "inbox") {
      return ["inbox"] as TaskStatus[];
    }
    return statusOrder.filter(
      (status) => status !== "review" || visibleTasks.some((task) => task.status === "review"),
    );
  }, [filter.kind, visibleTasks]);

  const moveMutation = useMutation({
    mutationFn: (input: { id: number; status: TaskStatus; position?: number }) =>
      api<Task>(`/tasks/${input.id}/move`, {
        method: "POST",
        body: JSON.stringify({
          status: input.status,
          ...(input.position === undefined ? {} : { position: input.position }),
        }),
      }),
    onMutate: async (input) => {
      await queryClient.cancelQueries({ queryKey: ["tasks"] });
      const previous = queryClient.getQueryData<TasksResponse>(["tasks"]);
      queryClient.setQueryData<TasksResponse>(["tasks"], (current) => {
        if (!current) {
          return current;
        }
        const currentTask = current.tasks.find((task) => task.id === input.id);
        if (!currentTask) {
          return current;
        }
        return {
          ...current,
          tasks: current.tasks.map((task) =>
            task.id === input.id
              ? {
                  ...task,
                  status: input.status,
                  position: input.position ?? task.position,
                }
              : task,
          ),
        };
      });
      return { previous };
    },
    onError: (error, _input, context) => {
      if (context?.previous) {
        queryClient.setQueryData(["tasks"], context.previous);
      }
      notify(errorMessage(error), "error");
    },
    onSuccess: (task) => {
      queryClient.setQueryData<TasksResponse>(["tasks"], (current) => {
        if (!current) {
          return current;
        }
        return {
          ...current,
          tasks: current.tasks.map((item) => (item.id === task.id ? task : item)),
        };
      });
      void queryClient.invalidateQueries({ queryKey: ["task", task.id] });
    },
  });

  const onDragStart = (event: DragStartEvent) => {
    const task = event.active.data.current?.task as Task | undefined;
    setActiveTask(task ?? null);
  };

  const onDragEnd = (event: DragEndEvent) => {
    setActiveTask(null);
    const current = event.active.data.current?.task as Task | undefined;
    if (!current || !event.over) {
      return;
    }
    const overData = event.over.data.current as
      | { task?: Task; status?: TaskStatus }
      | undefined;
    if (!overData) {
      return;
    }
    if (overData.task?.id === current.id) {
      return;
    }

    const targetStatus = overData.task?.status ?? overData.status;
    if (!targetStatus) {
      return;
    }
    let position: number | undefined;
    if (overData.task) {
      const columnTasks = tasks
        .filter((task) => task.status === targetStatus && task.id !== current.id)
        .sort((left, right) => left.position - right.position);
      const targetIndex = columnTasks.findIndex((task) => task.id === overData.task?.id);
      if (targetIndex >= 0) {
        const before = columnTasks[targetIndex - 1]?.position;
        const after = columnTasks[targetIndex]?.position;
        position = betweenPositions(before, after);
      }
    }
    if (current.status === targetStatus && position === undefined) {
      return;
    }
    moveMutation.mutate({ id: current.id, status: targetStatus, position });
  };

  return (
    <section className="board-page" aria-label="任务看板">
      <header className="board-header">
        <div>
          <p className="eyebrow">任务看板</p>
          <div className="board-title-line">
            <h1>{titleForFilter(filter)}</h1>
            {filter.kind === "project" || filter.kind === "tag" ? (
              <button className="text-button" type="button" onClick={onClearFilter}>
                清除筛选
              </button>
            ) : null}
          </div>
          <p className="board-summary">
            {visibleTasks.length} 件事 · 拖一张卡，换一个位置
          </p>
        </div>
        <div className="board-actions">
          <button className="search-trigger" type="button" onClick={onOpenPalette}>
            <Search size={16} aria-hidden />
            <span>搜索任务</span>
            <kbd>⌘K</kbd>
          </button>
          <button className="primary-button capture-shortcut" type="button" onClick={onOpenCapture}>
            <Plus size={16} aria-hidden />
            记一笔
          </button>
        </div>
      </header>

      <DndContext
        sensors={sensors}
        collisionDetection={closestCorners}
        onDragStart={onDragStart}
        onDragEnd={onDragEnd}
      >
        <div className={`board-grid ${filter.kind === "inbox" ? "board-grid-inbox" : ""}`}>
          {statuses.map((status) => {
            const columnTasks = visibleTasks
              .filter((task) => task.status === status)
              .sort((left, right) => left.position - right.position);
            const collapsed = status === "done" && !doneOpen && columnTasks.length > 0;
            return (
              <BoardColumn
                key={status}
                status={status}
                tasks={columnTasks}
                collapsed={collapsed}
                onToggle={() => setDoneOpen((value) => !value)}
                onOpenTask={onOpenTask}
              />
            );
          })}
        </div>
        <DragOverlay dropAnimation={null}>
          {activeTask ? (
            <article className="task-card task-card-overlay">
              <TaskCardBody task={activeTask} />
            </article>
          ) : null}
        </DragOverlay>
      </DndContext>
    </section>
  );
}

function BoardColumn({
  status,
  tasks,
  collapsed,
  onToggle,
  onOpenTask,
}: {
  status: TaskStatus;
  tasks: Task[];
  collapsed: boolean;
  onToggle: () => void;
  onOpenTask: (taskID: number) => void;
}) {
  const { setNodeRef, isOver } = useDroppable({
    id: `column-${status}`,
    data: { status },
  });
  return (
    <section
      ref={setNodeRef}
      className={`board-column board-column-${status} ${isOver ? "board-column-over" : ""}`}
      aria-label={statusLabel(status)}
    >
      <header className="column-header">
        <button
          className="column-title"
          type="button"
          onClick={status === "done" ? onToggle : undefined}
          disabled={status !== "done"}
        >
          <span>{statusLabel(status)}</span>
          <span className="column-count">{tasks.length}</span>
          {status === "done" && tasks.length > 0 ? (
            <ChevronDown
              size={14}
              className={collapsed ? "chevron-collapsed" : ""}
              aria-hidden
            />
          ) : null}
        </button>
      </header>
      {!collapsed ? (
        <SortableContext
          items={tasks.map((task) => task.id)}
          strategy={verticalListSortingStrategy}
        >
          <div className="column-body">
            {tasks.map((task) => (
              <TaskCard key={task.id} task={task} onOpen={onOpenTask} />
            ))}
            {tasks.length === 0 ? (
              <div className="column-empty">
                {status === "inbox" ? "先把念头丢进来。" : "拖到这里。"}
              </div>
            ) : null}
            <InlineComposer status={status} />
          </div>
        </SortableContext>
      ) : null}
    </section>
  );
}

function InlineComposer({ status }: { status: TaskStatus }) {
  const [open, setOpen] = useState(false);
  const [title, setTitle] = useState("");
  const queryClient = useQueryClient();
  const { notify } = useToast();

  const createMutation = useMutation({
    mutationFn: () =>
      status === "inbox"
        ? api<Task>("/capture", {
            method: "POST",
            body: JSON.stringify({ text: title }),
          })
        : api<Task>("/tasks", {
            method: "POST",
            body: JSON.stringify({ title, status }),
          }),
    onSuccess: () => {
      setTitle("");
      setOpen(false);
      void queryClient.invalidateQueries({ queryKey: ["tasks"] });
      void queryClient.invalidateQueries({ queryKey: ["projects"] });
      void queryClient.invalidateQueries({ queryKey: ["tags"] });
    },
    onError: (error) => notify(errorMessage(error), "error"),
  });

  if (!open) {
    return (
      <button className="add-task-button" type="button" onClick={() => setOpen(true)}>
        <Plus size={14} aria-hidden />
        新建
      </button>
    );
  }

  return (
    <form
      className="inline-composer"
      onSubmit={(event) => {
        event.preventDefault();
        if (title.trim()) {
          createMutation.mutate();
        }
      }}
    >
      <input
        autoFocus
        value={title}
        onChange={(event) => setTitle(event.target.value)}
        placeholder={status === "inbox" ? "随手记一句…#tag @项目" : "任务标题"}
        aria-label={`新建${statusLabel(status)}任务`}
        onKeyDown={(event) => {
          if (event.key === "Escape") {
            setOpen(false);
            setTitle("");
          }
        }}
      />
      <button type="submit" disabled={!title.trim() || createMutation.isPending}>
        {createMutation.isPending ? "…" : "保存"}
      </button>
    </form>
  );
}

function betweenPositions(before?: number, after?: number): number {
  if (before === undefined && after === undefined) {
    return 1024;
  }
  if (before === undefined) {
    return (after as number) - 1024;
  }
  if (after === undefined) {
    return before + 1024;
  }
  return (before + after) / 2;
}
