import { fireEvent, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vitest";
import App from "../app/App";
import { json, me, renderWithClient } from "../test/fixtures";

afterEach(() => {
  vi.unstubAllGlobals();
  window.history.replaceState(null, "", "/");
});

function mountConnections() {
  window.history.replaceState(null, "", "/settings/connections");
  vi.stubGlobal("scrollTo", vi.fn());
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    if (url.endsWith("/auth/me")) return json(me);
    if (url.includes("/tasks?")) return json({ tasks: [], next_cursor: null });
    if (url.includes("/projects")) return json({ projects: [] });
    if (url.endsWith("/tags")) return json({ tags: [] });
    if (url.endsWith("/tokens")) return json({ tokens: [] });
    if (url.endsWith("/agent/connection")) return json({ api_base_url: "https://tack.example/api/v1", auth_kind: "session", transport: "rest", mcp_supported: false, token: null });
    if (url.endsWith("/update/status")) return json({ current_version: "0.2.0", latest_version: null, update_available: false, interval_hours: 24, last_checked_at: null, last_error: "", release_body: "", release_url: "", published_at: null, configured: false });
    return json({ error: { code: "not_found", message: "不存在", request_id: "req_test" } }, 404);
  }));
  renderWithClient(<App />);
  return userEvent.setup();
}

it.each([
  { key: "n" },
  { key: "k", metaKey: true },
  { key: "k", ctrlKey: true },
])("keeps the agent guide open instead of handling global shortcut %o", async (shortcut) => {
  const user = mountConnections();
  await user.click(await screen.findByRole("button", { name: "查看通用说明" }));
  const guide = screen.getByRole("dialog", { name: "Agent 接入说明" });
  fireEvent.keyDown(within(guide).getByRole("button", { name: "复制 Agent 说明" }), shortcut);
  expect(window.location.pathname).toBe("/settings/connections");
  expect(guide).toBeInTheDocument();
  expect(screen.queryByRole("dialog", { name: "搜索任务" })).not.toBeInTheDocument();
});

it.each(["composition", "handled"])("ignores %s key events without leaving the connection form", async (mode) => {
  mountConnections();
  await screen.findByRole("button", { name: "查看通用说明" });
  const event = new KeyboardEvent("keydown", { key: "n", bubbles: true, cancelable: true, isComposing: mode === "composition" });
  if (mode === "handled") event.preventDefault();
  fireEvent(document.body, event);
  expect(window.location.pathname).toBe("/settings/connections");
  expect(screen.getByRole("textbox", { name: "名称" })).toBeInTheDocument();
});

it("restores command palette and capture shortcuts after the guide closes", async () => {
  const user = mountConnections();
  await user.click(await screen.findByRole("button", { name: "查看通用说明" }));
  await user.click(within(screen.getByRole("dialog", { name: "Agent 接入说明" })).getAllByRole("button", { name: "关闭" })[0]);
  expect(screen.queryByRole("dialog", { name: "Agent 接入说明" })).not.toBeInTheDocument();
  fireEvent.keyDown(document.body, { key: "k", metaKey: true });
  expect(screen.getByRole("dialog", { name: "搜索任务" })).toBeInTheDocument();
  fireEvent.keyDown(screen.getByRole("textbox", { name: "搜索任务" }), { key: "k", metaKey: true });
  expect(screen.queryByRole("dialog", { name: "搜索任务" })).not.toBeInTheDocument();
  fireEvent.keyDown(document.body, { key: "n" });
  expect(await screen.findByRole("textbox", { name: "记一笔" })).toBeInTheDocument();
  expect(window.location.pathname).toBe("/capture");
});
