import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AppShell } from "./app-shell";
import { json, me, project, renderWithClient, tag, task } from "../test/fixtures";
import { filterTasks } from "../lib/filter";

afterEach(() => vi.unstubAllGlobals());

function mockPages(failSecond = false, repeat = false) {
  let shouldFail = failSecond;
  const signals: Array<AbortSignal | null | undefined> = [];
  const requests: string[] = [];
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    if (url.includes("/tasks?")) {
      requests.push(url);
      signals.push(init?.signal);
      if (url.includes("cursor=")) {
        if (shouldFail) return json({ error: { message: "第二页暂不可用" } }, 503);
        return json({ tasks: [task({ id: 501, title: "最后一张卡" })], next_cursor: repeat ? "page+2/=" : null });
      }
      return json({ tasks: Array.from({ length: 500 }, (_, index) => task({ id: index + 1, status: "done" })), next_cursor: "page+2/=" });
    }
    if (url.endsWith("/projects")) return json({ projects: [project] });
    if (url.endsWith("/tags")) return json({ tags: [tag] });
    return json({ update_available: false });
  }));
  return { signals, requests, recover: () => { shouldFail = false; } };
}

describe("all task pages", () => {
  it("includes page two in counts and command searches, forwarding cancellation", async () => {
    const { signals, requests } = mockPages();
    renderWithClient(<AppShell me={me} path="/" navigate={vi.fn()} onLogout={vi.fn()} />);
    expect(await screen.findByText("501 件事")).toBeInTheDocument();
    expect(screen.getByText("最后一张卡")).toBeInTheDocument();
    expect(requests[1]).toContain("cursor=page%2B2%2F%3D");
    expect(signals.every((signal) => signal instanceof AbortSignal)).toBe(true);
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: /搜索任务/ }));
    await user.type(screen.getByRole("textbox", { name: "搜索任务" }), "status:todo tag:bug project:Codeg 最后一张");
    expect(within(screen.getByRole("dialog", { name: "搜索任务" })).getByText("最后一张卡")).toBeInTheDocument();
  });

  it("shows failed pagination and retries to the complete list", async () => {
    const pages = mockPages(true);
    renderWithClient(<AppShell me={me} path="/" navigate={vi.fn()} onLogout={vi.fn()} />);
    expect(await screen.findByRole("alert")).toHaveTextContent("第二页暂不可用");
    expect(screen.queryByText("500 件事")).not.toBeInTheDocument();
    pages.recover();
    await userEvent.click(screen.getByRole("button", { name: "重试" }));
    expect(await screen.findByText("501 件事")).toBeInTheDocument();
  });

  it("stops repeated cursors with a recoverable error instead of looping", async () => {
    const { requests } = mockPages(false, true);
    renderWithClient(<AppShell me={me} path="/" navigate={vi.fn()} onLogout={vi.fn()} />);
    expect(await screen.findByRole("alert")).toHaveTextContent(/分页/);
    expect(requests).toHaveLength(2);
  });

  it("aborts an unfinished page when the shell unmounts", async () => {
    let requestSignal: AbortSignal | null | undefined;
    vi.stubGlobal("fetch", vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      if (String(input).includes("/tasks?")) {
        requestSignal = init?.signal;
        return new Promise<Response>(() => undefined);
      }
      return Promise.resolve(json({ projects: [], tags: [] }));
    }));
    const view = renderWithClient(<AppShell me={me} path="/" navigate={vi.fn()} onLogout={vi.fn()} />);
    await waitFor(() => expect(requestSignal).toBeDefined());
    view.unmount();
    expect(requestSignal?.aborted).toBe(true);
  });
});

it("opens the narrow-screen filter and applies a project without losing the board", async () => {
  mockPages();
  const navigate = vi.fn();
  renderWithClient(<AppShell me={me} path="/" navigate={navigate} onLogout={vi.fn()} />);
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: "筛选" }));
  const dialog = await screen.findByRole("dialog", { name: "筛选看板" });
  await user.click(within(dialog).getByRole("button", { name: "Codeg" }));
  expect(navigate).toHaveBeenCalledWith("/");
  expect(screen.getByRole("heading", { name: "@Codeg" })).toBeInTheDocument();
  expect(screen.queryByRole("dialog", { name: "筛选看板" })).not.toBeInTheDocument();
});

it("combines search prefixes with text and accepts quoted metadata names", () => {
  const tasks = [task(), task({ id: 2, status: "done" }), task({ id: 3, project: { ...project, name: "My Project" }, tags: [] })];
  expect(filterTasks(tasks, { kind: "all" }, "status:todo tag:BUG project:codeg 登录").map((item) => item.id)).toEqual([1]);
  expect(filterTasks(tasks, { kind: "all" }, 'project:"My Project"').map((item) => item.id)).toEqual([3]);
  expect(filterTasks(tasks, { kind: "all" }, "status:unknown")).toEqual([]);
});
