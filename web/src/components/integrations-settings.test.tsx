import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vitest";
import { SettingsPage } from "./settings-page";
import { json, me, renderWithClient, task } from "../test/fixtures";
import { TaskSheet } from "./task-sheet";

afterEach(() => vi.unstubAllGlobals());

it("creates, edits, disables and deletes webhooks with sudo; secrets stay out of lists", async () => {
  const user = userEvent.setup();
  const clipboard = vi.spyOn(navigator.clipboard, "writeText").mockRejectedValue(new Error("复制权限被拒绝"));
  let hooks: Array<{ id: number; name: string; url: string; events: string[]; enabled: boolean; created_at: string; pending_count: number }> = [];
  const writes: Array<{ path: string; method?: string; body: Record<string, unknown> }> = [];
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input);
    const body = JSON.parse(String(init?.body ?? "{}")) as Record<string, unknown>;
    if (init?.method) writes.push({ path, method: init.method, body });
    if (path.endsWith("/auth/sudo")) return json({});
    if (path.endsWith("/webhooks") && init?.method === "POST") {
      hooks = [{ id: 1, name: String(body.name), url: String(body.url), events: body.events as string[], enabled: true, created_at: "2026-11-16T06:00:00Z", pending_count: 0 }];
      return json({ webhook: hooks[0], secret: "wh_only_once" }, 201);
    }
    if (path.endsWith("/webhooks/1") && init?.method === "PATCH") { hooks = [{ ...hooks[0], ...body }]; return json({ webhook: hooks[0] }); }
    if (path.endsWith("/webhooks/1") && init?.method === "DELETE") { hooks = []; return json(null, 204); }
    if (path.endsWith("/webhooks")) return json({ webhooks: hooks });
    return json({});
  }));
  const element = <SettingsPage me={me} path="/settings/integrations" navigate={vi.fn()} onLogout={vi.fn()} />;
  const view = renderWithClient(element);
  await user.click(await screen.findByRole("button", { name: "新建 Webhook" }));
  await user.type(screen.getByLabelText("Webhook 名称"), "通知");
  await user.type(screen.getByLabelText("接收地址"), "https://hooks.example.com/tack");
  await user.click(screen.getByRole("button", { name: "保存 Webhook" }));
  expect(writes).toHaveLength(0);
  await user.type(screen.getByLabelText("当前密码"), "password");
  await user.click(screen.getByRole("button", { name: "验证" }));
  expect(await screen.findByText("wh_only_once")).toBeInTheDocument();
  expect(writes[1]).toMatchObject({ method: "POST", body: { name: "通知", url: "https://hooks.example.com/tack", events: ["task.*"] } });
  await user.click(screen.getByRole("button", { name: "复制" }));
  expect(await screen.findByRole("alert")).toHaveTextContent("复制权限被拒绝");
  clipboard.mockResolvedValue();
  await user.click(screen.getByRole("button", { name: "复制" }));
  expect(await screen.findByText("已复制")).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "已保存，隐藏" }));
  expect(screen.queryByText("wh_only_once")).not.toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "编辑 Webhook 通知" }));
  await user.clear(screen.getByLabelText("Webhook 名称"));
  await user.type(screen.getByLabelText("Webhook 名称"), "新通知");
  await user.click(screen.getByLabelText("标签变化"));
  await user.click(screen.getByRole("button", { name: "保存 Webhook" }));
  await user.type(screen.getByLabelText("当前密码"), "password");
  await user.click(screen.getByRole("button", { name: "验证" }));
  await user.click(await screen.findByRole("button", { name: "停用 Webhook 新通知" }));
  await user.type(screen.getByLabelText("当前密码"), "password");
  await user.click(screen.getByRole("button", { name: "验证" }));
  expect(await screen.findByRole("button", { name: "启用 Webhook 新通知" })).toBeInTheDocument();
  expect(writes.at(-1)).toMatchObject({ method: "PATCH", body: { enabled: false } });
  view.unmount();
  renderWithClient(element);
  await user.click(await screen.findByRole("button", { name: "删除 Webhook 新通知" }));
  expect(screen.queryByText("wh_only_once")).not.toBeInTheDocument();
  await user.type(screen.getByLabelText("当前密码"), "password");
  await user.click(screen.getByRole("button", { name: "验证" }));
  expect(await screen.findByText("尚未添加 Webhook。")).toBeInTheDocument();
});

it("recovers list errors and shows delivery retry status without inventing retry endpoints", async () => {
  let fail = true;
  let attempts = 2;
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
    const path = String(input);
    if (path.endsWith("/webhooks")) return fail ? json({ error: { message: "集成暂不可用" } }, 503) : json({ webhooks: [{ id: 1, name: "通知", url: "https://hooks.example.com", events: ["task.*"], enabled: true, created_at: "2026-11-16T06:00:00Z", last_status: "pending", pending_count: 1, last_error: "HTTP 503" }] });
    if (path.endsWith("/deliveries")) return json({ deliveries: [{ id: 8, event_id: 5, status: "pending", attempts, next_attempt_at: "2026-11-16T10:00:00Z", last_error: "HTTP 503", delivered_at: null }] });
    return json({});
  }));
  const user = userEvent.setup();
  renderWithClient(<SettingsPage me={me} path="/settings/integrations" navigate={vi.fn()} onLogout={vi.fn()} />);
  expect(await screen.findByRole("alert")).toHaveTextContent("集成暂不可用");
  fail = false;
  await user.click(screen.getByRole("button", { name: "重试" }));
  await user.click(await screen.findByRole("button", { name: "投递记录 通知" }));
  const dialog = screen.getByRole("dialog", { name: "投递记录" });
  expect(await within(dialog).findByText(/已尝试 2 次/)).toBeInTheDocument();
  expect(within(dialog).getByText(/下次重试/)).toBeInTheDocument();
  expect(within(dialog).getByText("HTTP 503")).toBeInTheDocument();
  attempts = 3;
  await user.click(within(dialog).getByRole("button", { name: "刷新" }));
  expect(await within(dialog).findByText(/已尝试 3 次/)).toBeInTheDocument();
  expect(screen.getByText(/单独配置本地 CLI/)).toBeInTheDocument();
  expect(screen.getByText(/agent_ready/)).toBeInTheDocument();
});

it("only renders safe HTTP external task links", async () => {
  vi.stubGlobal("fetch", vi.fn(async () => json({ task: task(), events: [], links: [
    { id: 1, kind: "codeg_work_task", external_id: "17", url: "http://localhost:8088/workspace?task=17", created_at: "2026-11-16T06:00:00Z" },
    { id: 2, kind: "github_issue", external_id: "9", url: "https://github.com/example/repo/issues/9", created_at: "2026-11-16T06:00:00Z" },
    { id: 3, kind: "bad", external_id: "x", url: "javascript:alert(1)", created_at: "2026-11-16T06:00:00Z" },
    { id: 4, kind: "bad", external_id: "y", url: "https://user:secret@example.com", created_at: "2026-11-16T06:00:00Z" },
  ] })));
  renderWithClient(<TaskSheet taskID={1} projects={[]} onClose={vi.fn()} />);
  await waitFor(() => expect(screen.getAllByRole("link")).toHaveLength(2));
  expect(screen.getByRole("link", { name: /Codeg.*17/ })).toHaveAttribute("href", "http://localhost:8088/workspace?task=17");
  for (const link of screen.getAllByRole("link")) expect(link).toHaveAttribute("rel", "noopener noreferrer");
});
