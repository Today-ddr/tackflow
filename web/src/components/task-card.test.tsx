import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import type { Task } from "../lib/api";
import { TaskCardBody } from "./task-card";

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
  it("keeps the task identity visible when there is no due date", () => {
    render(<TaskCardBody task={task()} />);

    expect(screen.getByText("整理看板层级")).toBeInTheDocument();
    expect(screen.getByText("#7")).toBeInTheDocument();
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
