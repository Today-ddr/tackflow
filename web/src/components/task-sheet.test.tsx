import { QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";

import { createQueryClient } from "../app/query";
import type { Task, TaskDetailResponse } from "../lib/api";
import { TaskSheet } from "./task-sheet";
import { ToastProvider } from "./toast";
import { renderWithClient } from "../test/fixtures";

afterEach(() => vi.unstubAllGlobals());

function jsonResponse(body: unknown, status: number): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function task(status: Task["status"]): Task {
  return {
    id: 5,
    project_id: null,
    title: "原任务",
    notes: "原备注",
    status,
    priority: 1,
    position: 1024,
    due_at: null,
    agent_ready: false,
    created_at: "2026-11-16T06:00:00Z",
    updated_at: "2026-11-16T06:00:00Z",
    completed_at: null,
    project: null,
    tags: [],
  };
}

beforeAll(() => {
  HTMLDialogElement.prototype.showModal = function showModal() {
    this.open = true;
  };
  HTMLDialogElement.prototype.close = function close() {
    this.open = false;
  };
});

describe("TaskSheet", () => {
  it("invalidates task counts and trash when deleting a card", async () => {
    const client = createQueryClient();
    client.setQueryData(["tags"], { tags: [] });
    client.setQueryData(["trash"], { pages: [], pageParams: [] });
    client.setQueryData(["tasks"], { tasks: [task("todo")], next_cursor: null });
    client.setQueryData(["projects"], { projects: [] });
    vi.stubGlobal("confirm", () => true);
    vi.stubGlobal("fetch", vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => init?.method === "DELETE" ? new Response(null, { status: 204 }) : jsonResponse({ task: task("todo"), events: [], links: [] }, 200)));
    const close = vi.fn();
    renderWithClient(<TaskSheet taskID={5} projects={[]} onClose={close} />, client);
    await screen.findByLabelText("标题");
    await userEvent.click(screen.getByRole("button", { name: "删除" }));
    await waitFor(() => expect(close).toHaveBeenCalled());
    for (const key of ["tasks", "projects", "tags", "trash"]) expect(client.getQueryState([key])?.isInvalidated).toBe(true);
  });

  it("refreshes the board after comments while retaining the complete task cache", async () => {
    const client = createQueryClient();
    const cached = { tasks: [task("todo"), { ...task("todo"), id: 501 }], next_cursor: null };
    client.setQueryData(["tasks"], cached);
    vi.stubGlobal("fetch", vi.fn(async () => jsonResponse({ task: task("todo"), events: [], links: [] }, 200)));
    renderWithClient(<TaskSheet taskID={5} projects={[]} onClose={vi.fn()} />, client);
    const user = userEvent.setup();
    await user.type(await screen.findByRole("textbox", { name: "评论" }), "已核对");
    await user.click(screen.getByRole("button", { name: "发送" }));
    await waitFor(() => expect(screen.getByRole("textbox", { name: "评论" })).toHaveValue(""));
    expect(client.getQueryState(["tasks"])?.isInvalidated).toBe(true);
    expect(client.getQueryData(["tasks"])).toEqual(cached);
  });

  it("keeps unsaved edits when the status is changed", async () => {
    let currentStatus: Task["status"] = "inbox";
    let detailRequests = 0;
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith("/api/v1/tasks/5/move")) {
        currentStatus = "todo";
        return jsonResponse(task(currentStatus), 200);
      }
      if (url.endsWith("/api/v1/tasks/5")) {
        if (init?.method === "PATCH") {
          return jsonResponse(task(currentStatus), 200);
        }
        detailRequests++;
        const detail: TaskDetailResponse = {
          task: task(currentStatus),
          events: [],
          links: [],
        };
        return jsonResponse(detail, 200);
      }
      return jsonResponse({ error: { code: "not_found", message: "不存在" } }, 404);
    });
    vi.stubGlobal("fetch", fetchMock);

    render(
      <ToastProvider>
        <QueryClientProvider client={createQueryClient()}>
          <TaskSheet taskID={5} projects={[]} onClose={() => undefined} />
        </QueryClientProvider>
      </ToastProvider>,
    );

    const user = userEvent.setup();
    const title = await screen.findByLabelText("标题");
    await user.clear(title);
    await user.type(title, "未保存标题");

    await user.click(screen.getByRole("button", { name: "待办" }));

    await waitFor(() => {
      expect(detailRequests).toBeGreaterThanOrEqual(2);
    });
    expect(screen.getByLabelText("标题")).toHaveValue("未保存标题");
  });
});
