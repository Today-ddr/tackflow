import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vitest";
import { SettingsPage } from "./settings-page";
import { json, me, renderWithClient } from "../test/fixtures";

afterEach(() => { vi.unstubAllGlobals(); vi.restoreAllMocks(); });

it("filters correlated diagnostics and copies machine-readable JSONL", async () => {
  const calls: string[] = [];
  const entry = { time: "2026-11-18T00:00:00Z", level: "WARN", event: "request", request_id: "req_0123456789abcdef", route: "POST /api/v1/tokens", method: "POST", status: 422, error_code: "invalid_scopes", duration_ms: 8 };
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    calls.push(url);
    if (url.includes("/logs")) return json({ entries: [entry] });
    return json({});
  }));
  const user = userEvent.setup();
  const clipboard = vi.spyOn(navigator.clipboard, "writeText").mockResolvedValue(undefined);
  renderWithClient(<SettingsPage me={me} path="/settings/logs" navigate={vi.fn()} onLogout={vi.fn()} />);
  expect(await screen.findByText("invalid_scopes")).toBeInTheDocument();
  await user.selectOptions(screen.getByLabelText("日志级别"), "WARN");
  await user.type(screen.getByLabelText("请求 ID"), entry.request_id);
  await user.click(screen.getByRole("button", { name: "应用筛选" }));
  await waitFor(() => expect(calls.some((url) => url.includes("level=WARN") && url.includes(entry.request_id))).toBe(true));
  await user.click(screen.getByRole("button", { name: "复制 JSONL" }));
  await screen.findByText("日志已复制");
  const copied = clipboard.mock.calls.at(-1)?.[0] ?? "";
  expect(JSON.parse(copied.trim())).toEqual(entry);
});

it("shows copy failures and preserves the readable log entries", async () => {
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => String(input).includes("/logs") ? json({ entries: [{ time: "2026-11-18T00:00:00Z", level: "INFO", event: "request", route: "GET /api/v1/tasks", status: 200 }] }) : json({})));
  const user = userEvent.setup();
  vi.spyOn(navigator.clipboard, "writeText").mockRejectedValue(new Error("clipboard blocked"));
  renderWithClient(<SettingsPage me={me} path="/settings/logs" navigate={vi.fn()} onLogout={vi.fn()} />);
  await screen.findByText("GET /api/v1/tasks");
  await user.click(screen.getByRole("button", { name: "复制 JSONL" }));
  expect(await screen.findByRole("alert")).toHaveTextContent("复制失败");
  expect(screen.getByText("GET /api/v1/tasks")).toBeInTheDocument();
});
