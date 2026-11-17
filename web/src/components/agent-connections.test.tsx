import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vitest";
import { SettingsPage } from "./settings-page";
import { json, me, project, renderWithClient, tag } from "../test/fixtures";

const key = { id: 10, name: "Agent A", prefix: "tk_prefix", scopes: ["read"], project_ids: [1], tag_ids: [2], created_at: "2026-11-16T06:00:00Z", last_used_at: null, expires_at: null };
const metadata = { api_base_url: "https://tack.example/api/v1", auth_kind: "session", transport: "rest", mcp_supported: false, token: null };

afterEach(() => { vi.unstubAllGlobals(); vi.restoreAllMocks(); });

function mount(path = "connections") {
  const view = renderWithClient(<SettingsPage me={me} path={`/settings/${path}`} navigate={vi.fn()} onLogout={vi.fn()} />);
  return { ...view, user: userEvent.setup() };
}

function backend(options: { rejectCheck?: boolean; failResources?: boolean; sessionCheck?: boolean } = {}) {
  const calls: Array<{ url: string; init?: RequestInit }> = [];
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    calls.push({ url, init });
    if (url.includes("/projects")) return options.failResources ? json({ error: { message: "分类加载失败" } }, 503) : json({ projects: [project] });
    if (url.endsWith("/tags")) return json({ tags: [tag] });
    if (url.endsWith("/agent/connection")) {
      if (!new Headers(init?.headers).has("Authorization")) return json(metadata);
      if (options.rejectCheck) return json({ error: { message: "API 令牌无效" } }, 401);
      return json(options.sessionCheck ? metadata : { ...metadata, auth_kind: "token", token: key });
    }
    if (url.endsWith("/tokens") && init?.method === "POST") return json({ token: "tk_one_time_secret", api_token: key }, 201);
    if (url.endsWith("/tokens")) return json({ tokens: [key] });
    return json({});
  }));
  return calls;
}

it("creates a scoped key with a purpose and expiry without caching its plaintext", async () => {
  const calls = backend();
  const { user, client } = mount();
  expect(await screen.findByLabelText("用途")).toHaveValue("read");
  await user.type(screen.getByLabelText("名称"), "CI Agent");
  expect(document.querySelector("#connection-password")).toBeNull();
  await user.selectOptions(screen.getByLabelText("用途"), "write");
  await user.selectOptions(screen.getByLabelText("有效期"), "7");
  await user.click(await screen.findByLabelText("项目：Codeg"));
  await user.click(screen.getByLabelText("标签：bug"));
  const before = Date.now();
  await user.click(screen.getByRole("button", { name: "生成 API Key" }));
  await screen.findByText("tk_one_time_secret");
  const payload = JSON.parse(String(calls.find((call) => call.url.endsWith("/tokens") && call.init?.method === "POST")?.init?.body)) as Record<string, unknown>;
  expect(payload).toMatchObject({ name: "CI Agent", scopes: ["write"], project_ids: [1], tag_ids: [2] });
  expect(payload).not.toHaveProperty("password");
  expect(Date.parse(String(payload.expires_at)) - before).toBeGreaterThan(6.99 * 86400000);
  expect(Date.parse(String(payload.expires_at)) - before).toBeLessThan(7.01 * 86400000);
  const cache = JSON.stringify(client.getMutationCache().getAll().map((mutation) => mutation.state));
  expect(cache).not.toContain("tk_one_time_secret");
  await user.click(screen.getByRole("button", { name: "已保存，隐藏" }));
  expect(screen.queryByText("tk_one_time_secret")).not.toBeInTheDocument();
  expect(screen.getByLabelText("待检查的 API Key")).toHaveValue("");
});

it("copies per-key instructions with correct permissions and no credential", async () => {
  backend();
  const { user } = mount();
  const clipboard = vi.spyOn(navigator.clipboard, "writeText").mockResolvedValue(undefined);
  await user.click(await screen.findByRole("button", { name: "接入说明 Agent A" }));
  await user.click(screen.getByRole("button", { name: "复制 Agent 说明" }));
  const copied = clipboard.mock.calls.at(-1)?.[0] ?? "";
  expect(copied).toContain("https://tack.example/api/v1");
  expect(copied).toContain("TACK_API_TOKEN");
  expect(copied).toContain("project_ids=[1]");
  expect(copied).toContain("tag_ids=[2]");
  expect(copied).toContain("GET /tasks");
  expect(copied).not.toContain("POST /agent/claim");
  expect(copied).not.toContain("tk_prefix");
  expect(copied).not.toContain("tk_one_time_secret");
});

it("checks the provided Bearer credential with no cookie and no task writes", async () => {
  const calls = backend();
  const { user } = mount();
  await user.type(await screen.findByLabelText("待检查的 API Key"), "tk_supplied_key");
  await user.click(screen.getByRole("button", { name: "检查连接" }));
  expect(await screen.findByText(/认证通过.*Agent A/)).toBeInTheDocument();
  const checked = calls.find((call) => new Headers(call.init?.headers).has("Authorization"));
  expect(checked?.url).toBe("/api/v1/agent/connection");
  expect(checked?.init?.credentials).toBe("omit");
  expect(checked?.init?.redirect).toBe("error");
  expect(new Headers(checked?.init?.headers).get("Authorization")).toBe("Bearer tk_supplied_key");
  expect(calls.every((call) => !call.init?.method || call.init.method === "GET")).toBe(true);
  expect(screen.getByLabelText("待检查的 API Key")).toHaveValue("");
});

it.each([{ rejectCheck: true }, { sessionCheck: true }])("does not report a key connected on rejected or session authentication: %o", async (options) => {
  backend(options);
  const { user } = mount();
  await user.type(await screen.findByLabelText("待检查的 API Key"), "tk_bad_key");
  await user.click(screen.getByRole("button", { name: "检查连接" }));
  expect(await screen.findByRole("alert")).toBeInTheDocument();
  expect(screen.queryByText(/认证通过/)).not.toBeInTheDocument();
});

it("blocks creation until resource metadata has loaded", async () => {
  backend({ failResources: true });
  const { user } = mount("tokens");
  await user.type(screen.getByLabelText("名称"), "unsafe default");
  expect(await screen.findByText("分类加载失败")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "生成 API Key" })).toBeDisabled();
});

it("presents API key details as labeled, scannable columns", async () => {
  backend();
  mount();
  expect(await screen.findByText("Agent A")).toBeInTheDocument();
  expect(screen.getAllByText("资源范围")).toHaveLength(2);
  expect(screen.getByText("项目：Codeg · 标签：bug")).toBeInTheDocument();
  expect(screen.getAllByText("不过期")).toHaveLength(2);
  expect(screen.getByText("尚未使用")).toBeInTheDocument();
  expect(screen.getByText("tk_prefix…")).toBeInTheDocument();
});

it("reports a failed copy instead of pretending it succeeded", async () => {
  backend();
  const { user } = mount();
  vi.spyOn(navigator.clipboard, "writeText").mockRejectedValue(new Error("clipboard blocked"));
  await user.click(await screen.findByRole("button", { name: "接入说明 Agent A" }));
  await user.click(screen.getByRole("button", { name: "复制 Agent 说明" }));
  await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent("复制失败"));
});
