import { act, screen, waitFor } from "@testing-library/react";
import { useQuery } from "@tanstack/react-query";
import { afterEach, expect, it, vi } from "vitest";
import { AppShell } from "../components/app-shell";
import { fetchAllTasks } from "../lib/tasks";
import { createQueryClient } from "./query";
import { useTaskEvents } from "./task-events";
import { json, me, renderWithClient, task } from "../test/fixtures";

class Stream extends EventTarget {
  static instances: Stream[] = [];
  closed = false;
  constructor(readonly url: string) { super(); Stream.instances.push(this); }
  close() { this.closed = true; }
}

afterEach(() => { vi.unstubAllGlobals(); vi.useRealTimers(); Stream.instances = []; });

function TaskSnapshot() {
  useTaskEvents(true);
  const query = useQuery({ queryKey: ["tasks"], queryFn: ({ signal }) => fetchAllTasks(signal) });
  return <output aria-label="任务快照">{query.data ? query.data.tasks.map((item) => item.title).join(",") || "empty" : "pending"}</output>;
}

function CachedSnapshot({ queryKey, read }: { queryKey: readonly unknown[]; read: (signal: AbortSignal) => Promise<string> }) {
  useTaskEvents(true);
  const query = useQuery({ queryKey, queryFn: ({ signal }) => read(signal) });
  return <output aria-label="缓存快照">{query.data ?? "pending"}</output>;
}

it("coalesces ready and change events, invalidates all task caches and closes on unmount", async () => {
  vi.stubGlobal("EventSource", Stream);
  vi.stubGlobal("fetch", vi.fn(async () => json({ tasks: [], projects: [], tags: [], next_cursor: null })));
  const client = createQueryClient();
  client.setQueryData(["trash"], { pages: [] });
  client.setQueryData(["task", 7], { task: { id: 7 } });
  client.setQueryData(["projects", "all"], { projects: [] });
  const invalidations = vi.spyOn(client, "invalidateQueries");
  const view = renderWithClient(<AppShell me={me} path="/" navigate={vi.fn()} onLogout={vi.fn()} />, client);
  await screen.findByText("0 件事");
  await waitFor(() => expect(Stream.instances).toHaveLength(1));
  const stream = Stream.instances[0];
  expect(stream.url).toBe("/api/v1/events");
  vi.useFakeTimers();
  await act(async () => {
    stream.dispatchEvent(new MessageEvent("ready", { data: '{"cursor":0}', lastEventId: "0" }));
    for (let index = 0; index < 10; index++) stream.dispatchEvent(new MessageEvent("change", { data: JSON.stringify({ id: index, resource: "task", resource_id: 7, kind: "updated", created_at: "2026-11-16T06:00:00Z" }) }));
    await vi.advanceTimersByTimeAsync(500);
  });
  expect(client.getQueryState(["trash"])?.isInvalidated).toBe(true);
  expect(client.getQueryState(["task", 7])?.isInvalidated).toBe(true);
  expect(client.getQueryState(["projects", "all"])?.isInvalidated).toBe(true);
  expect(invalidations).toHaveBeenCalledTimes(5);
  act(() => {
    stream.dispatchEvent(new MessageEvent("ready", { data: '{"cursor":10}', lastEventId: "10" }));
    stream.dispatchEvent(new MessageEvent("change", { data: '{"resource":"tag"}' }));
  });
  view.unmount();
  await act(async () => {
    stream.dispatchEvent(new MessageEvent("ready", { data: '{"cursor":11}', lastEventId: "11" }));
    stream.dispatchEvent(new MessageEvent("change", { data: '{"resource":"task"}' }));
    await vi.advanceTimersByTimeAsync(500);
  });
  expect(stream.closed).toBe(true);
  expect(invalidations).toHaveBeenCalledTimes(5);
});

it("refreshes the snapshot after initial and reconnect ready without requiring a change event", async () => {
  vi.stubGlobal("EventSource", Stream);
  let title = "";
  const fetchMock = vi.fn(async () => json({ tasks: title ? [task({ title })] : [], next_cursor: null }));
  vi.stubGlobal("fetch", fetchMock);
  renderWithClient(<TaskSnapshot />);
  await waitFor(() => expect(screen.getByLabelText("任务快照")).toHaveTextContent("empty"));
  const stream = Stream.instances[0];
  for (const [cursor, nextTitle] of [[1, "初连前新增的任务"], [2, "重连后的最新任务"]] as const) {
    title = nextTitle;
    act(() => stream.dispatchEvent(new MessageEvent("ready", { data: JSON.stringify({ cursor }), lastEventId: String(cursor) })));
    await waitFor(() => expect(screen.getByLabelText("任务快照")).toHaveTextContent(nextTitle));
  }
  expect(fetchMock).toHaveBeenCalledTimes(3);
  expect(Stream.instances).toHaveLength(1);
});

it.each(["ready", "change"])("restarts an unfinished first multi-page snapshot on %s and ignores its late response", async (eventType) => {
  vi.stubGlobal("EventSource", Stream);
  let changed = false;
  let finishOldPage: ((response: Response) => void) | undefined;
  let oldSignal: AbortSignal | null | undefined;
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    if (String(input).includes("cursor=")) {
      oldSignal = init?.signal;
      return new Promise<Response>((resolve) => { finishOldPage = resolve; });
    }
    return json({ tasks: [task({ title: changed ? "最新任务" : "过期任务" })], next_cursor: changed ? null : "second-page" });
  });
  vi.stubGlobal("fetch", fetchMock);
  renderWithClient(<TaskSnapshot />);
  await waitFor(() => expect(finishOldPage).toBeDefined());
  changed = true;
  act(() => Stream.instances[0].dispatchEvent(new MessageEvent(eventType, { data: eventType === "ready" ? '{"cursor":1}' : '{"id":1,"resource":"task"}', lastEventId: "1" })));
  await waitFor(() => expect(screen.getByLabelText("任务快照")).toHaveTextContent("最新任务"));
  expect(oldSignal?.aborted).toBe(true);
  await act(async () => { finishOldPage?.(json({ tasks: [task({ id: 2, title: "过期的第二页" })], next_cursor: null })); });
  expect(screen.getByLabelText("任务快照")).not.toHaveTextContent("过期");
  expect(fetchMock).toHaveBeenCalledTimes(3);
});

it.each([
  { name: "projects including archived", queryKey: ["projects", "all"] },
  { name: "tags", queryKey: ["tags"] },
  { name: "trash", queryKey: ["trash"] },
  { name: "task detail", queryKey: ["task", 7] },
])("restarts pending $name data after a change", async ({ queryKey }) => {
  vi.stubGlobal("EventSource", Stream);
  let finishOldRead: ((value: string) => void) | undefined;
  const initial = new Promise<string>((resolve) => { finishOldRead = resolve; });
  const read = vi.fn<(signal: AbortSignal) => Promise<string>>().mockReturnValueOnce(initial).mockResolvedValue("fresh");
  renderWithClient(<CachedSnapshot queryKey={queryKey} read={read} />);
  await waitFor(() => expect(read).toHaveBeenCalledTimes(1));
  act(() => Stream.instances[0].dispatchEvent(new MessageEvent("change", { data: '{"id":1,"resource":"task"}' })));
  await waitFor(() => expect(screen.getByLabelText("缓存快照")).toHaveTextContent("fresh"));
  expect(read.mock.calls[0][0].aborted).toBe(true);
  await act(async () => { finishOldRead?.("stale"); });
  expect(screen.getByLabelText("缓存快照")).toHaveTextContent("fresh");
  expect(read).toHaveBeenCalledTimes(2);
});

it("does not start another request if unmounted while an event flush is cancelling the first snapshot", async () => {
  vi.stubGlobal("EventSource", Stream);
  let finishFirstRead: ((response: Response) => void) | undefined;
  const firstRead = new Promise<Response>((resolve) => { finishFirstRead = resolve; });
  const fetchMock = vi.fn(async () => firstRead);
  vi.stubGlobal("fetch", fetchMock);
  const view = renderWithClient(<TaskSnapshot />);
  await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
  const stream = Stream.instances[0];
  vi.useFakeTimers();
  act(() => {
    stream.dispatchEvent(new MessageEvent("ready", { data: '{"cursor":1}', lastEventId: "1" }));
    vi.advanceTimersByTime(200);
    view.unmount();
  });
  await act(async () => {
    finishFirstRead?.(json({ tasks: [], next_cursor: null }));
    await vi.advanceTimersByTimeAsync(500);
  });
  expect(stream.closed).toBe(true);
  expect(fetchMock).toHaveBeenCalledTimes(1);
});

it("does not open events for token authentication", () => {
  vi.stubGlobal("EventSource", Stream);
  vi.stubGlobal("fetch", vi.fn(async () => json({ tasks: [], projects: [], tags: [], next_cursor: null })));
  const view = renderWithClient(<AppShell me={{ ...me, auth_kind: "token" }} path="/" navigate={vi.fn()} onLogout={vi.fn()} />);
  expect(Stream.instances).toHaveLength(0);
  view.unmount();
});

it("keeps the board usable without EventSource support", async () => {
  vi.stubGlobal("EventSource", undefined);
  vi.stubGlobal("fetch", vi.fn(async () => json({ tasks: [], projects: [], tags: [], next_cursor: null })));
  renderWithClient(<AppShell me={me} path="/" navigate={vi.fn()} onLogout={vi.fn()} />);
  expect(await screen.findByText("0 件事")).toBeInTheDocument();
});
