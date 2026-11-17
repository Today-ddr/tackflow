import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { Task } from "../lib/api";
import { TaskCard, TaskCardBody } from "./task-card";

function task(overrides: Partial<Task> = {}): Task {
  return {
    id: 7,
    project_id: null,
    title: "整理看板层级",
    notes: "",
    status: "todo",
    priority: 0,
    position: 1024,
    due_at: null,
    agent_ready: false,
    created_at: "2026-11-16T06:00:00Z",
    updated_at: "2026-11-16T06:00:00Z",
    completed_at: null,
    project: null,
    tags: [],
    ...overrides,
  };
}

describe("TaskCardBody", () => {
  it("omits meaningless placeholder metadata", () => {
    render(<TaskCardBody task={task()} />);

    expect(screen.getByText("整理看板层级")).toBeInTheDocument();
    expect(screen.queryByText("#7")).not.toBeInTheDocument();
    expect(screen.queryByText("未分类")).not.toBeInTheDocument();
  });

  it("shows compact task metadata without duplicating decoration", () => {
    render(
      <TaskCardBody
        task={task({
          project: {
            id: 1,
            name: "codeg",
            color: "sage",
            position: 1024,
            created_at: "2026-11-16T06:00:00Z",
            archived_at: null,
            task_count: 3,
          },
          priority: 2,
          tags: [
            {
              id: 2,
              name: "ui",
              color: "ink",
              created_at: "2026-11-16T06:00:00Z",
              task_count: 1,
            },
          ],
        })}
      />,
    );

    expect(screen.getByText("codeg")).toBeInTheDocument();
    expect(screen.getByText("中")).toBeInTheDocument();
    expect(screen.getByText("#ui")).toBeInTheDocument();
  });
});

it("changes status without opening the task and keeps the drag handle separate", async () => {
  const open = vi.fn();
  const changeStatus = vi.fn();
  const current = task();
  render(<TaskCard task={current} onOpen={open} onStatusChange={changeStatus} />);
  const user = userEvent.setup();
  await user.selectOptions(screen.getByRole("combobox", { name: `更改状态：${current.title}` }), "doing");
  expect(changeStatus).toHaveBeenCalledWith(current.id, "doing");
  expect(open).not.toHaveBeenCalled();
  expect(screen.getByRole("button", { name: `拖动任务 ${current.title}` })).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: `打开任务 ${current.title}` }));
  expect(open).toHaveBeenCalledWith(current.id);
});
