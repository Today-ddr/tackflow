import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { SettingsPage } from "./settings-page";
import { createQueryClient } from "../app/query";
import { json, me, project, renderWithClient, tag, task } from "../test/fixtures";

afterEach(() => vi.unstubAllGlobals());

function mount(path: string) {
  const client = createQueryClient();
  client.setQueryData(["tasks"], { tasks: [], next_cursor: null });
  client.setQueryData(["task", 1], { task: task(), events: [], links: [] });
  if (path !== "trash") client.setQueryData(["trash"], { pages: [], pageParams: [] });
  const view = renderWithClient(<SettingsPage me={me} path={`/settings/${path}`} navigate={vi.fn()} onLogout={vi.fn()} />, client);
  return { ...view, user: userEvent.setup() };
}

describe("metadata management", () => {
  function backend() {
    let projects = [project, { ...project, id: 3, name: "旧项目", archived_at: "2026-11-01T00:00:00Z" }];
    let tags = [tag];
    const writes: Array<{ url: string; body: Record<string, unknown> }> = [];
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const body = JSON.parse(String(init?.body ?? "{}")) as Record<string, unknown>;
      if (init?.method && init.method !== "GET") writes.push({ url, body });
      if (url.endsWith("/auth/sudo")) return json({});
      if (url.endsWith("/projects?include_archived=1")) return json({ projects });
      if (url.includes("/projects/") && init?.method === "PATCH") {
        projects = projects.map((item) => item.id === Number(url.split("/").at(-1)) ? { ...item, ...body, archived_at: body.archived === false ? null : body.archived === true ? "2026-11-16T00:00:00Z" : item.archived_at } as typeof project : item);
        return json(projects.find((item) => item.id === Number(url.split("/").at(-1))));
      }
      if (url.endsWith("/tags/2") && init?.method === "PATCH") { tags = [{ ...tag, ...body } as typeof tag]; return json(tags[0]); }
      if (url.endsWith("/tags/2") && init?.method === "DELETE") { tags = []; return json(null, 204); }
      if (url.endsWith("/tags") && init?.method === "POST") { tags = [...tags, { ...tag, ...body, id: 4 } as typeof tag]; return json(tags[1], 201); }
      if (url.endsWith("/projects") && init?.method === "POST") { projects = [...projects, { ...project, ...body, id: 4 } as typeof project]; return json(projects[2], 201); }
      if (url.endsWith("/tags")) return json({ tags });
      return json({});
    }));
    return writes;
  }

  it("edits project and tag metadata, retaining task caches for refetch", async () => {
    const writes = backend();
    const { client, user } = mount("organize");
    await user.click(await screen.findByRole("button", { name: "编辑项目 Codeg" }));
    await user.clear(screen.getByLabelText("项目名称"));
    await user.type(screen.getByLabelText("项目名称"), "新项目名");
    await user.selectOptions(screen.getByLabelText("颜色"), "sky");
    await user.click(screen.getByRole("button", { name: "保存" }));
    expect(await screen.findByRole("button", { name: "编辑项目 新项目名" })).toBeInTheDocument();
    expect(writes[0].body).toEqual({ name: "新项目名", color: "sky" });
    expect(client.getQueryState(["tasks"])?.isInvalidated).toBe(true);
    expect(client.getQueryState(["task", 1])?.isInvalidated).toBe(true);
    await user.click(screen.getByRole("button", { name: "编辑标签 bug" }));
    await user.clear(screen.getByLabelText("标签名称"));
    await user.type(screen.getByLabelText("标签名称"), "urgent");
    await user.click(screen.getByRole("button", { name: "保存" }));
    expect(await screen.findByRole("button", { name: "编辑标签 urgent" })).toBeInTheDocument();
  });

  it("creates projects and tags from the same settings tab", async () => {
    backend();
    const { user } = mount("organize");
    for (const kind of ["项目", "标签"]) {
      await user.click(await screen.findByRole("button", { name: `新建${kind}` }));
      await user.type(screen.getByLabelText(`${kind}名称`), `我的${kind}`);
      await user.click(screen.getByRole("button", { name: "保存" }));
      expect(await screen.findByRole("button", { name: `编辑${kind} 我的${kind}` })).toBeInTheDocument();
    }
  });

  it("archives and restores a project only after sudo verification", async () => {
    const writes = backend();
    const { user } = mount("organize");
    for (const [action, name, archived] of [["归档", "Codeg", true], ["恢复", "旧项目", false]] as const) {
      await user.click(await screen.findByRole("button", { name: `${action}项目 ${name}` }));
      expect(writes.filter((write) => write.url.includes("/projects/"))).toHaveLength(action === "归档" ? 0 : 1);
      await user.type(screen.getByLabelText("当前密码"), "password");
      await user.click(screen.getByRole("button", { name: "验证" }));
      await waitFor(() => expect(writes.at(-1)?.body).toEqual({ archived }));
    }
    expect(await screen.findByRole("button", { name: "归档项目 旧项目" })).toBeInTheDocument();
  });

  it("warns about scoped token revocation before deleting a tag and refreshes token metadata", async () => {
    const writes = backend();
    const { user, client } = mount("organize");
    client.setQueryData(["tokens"], { tokens: [{ id: 1, name: "受限脚本", prefix: "tk_prefix", scopes: ["read"], project_ids: [], tag_ids: [2], created_at: "2026-11-16T06:00:00Z", last_used_at: null }] });
    await user.click(await screen.findByRole("button", { name: "删除标签 bug" }));
    const dialog = screen.getByRole("dialog", { name: "删除标签" });
    expect(dialog).toHaveTextContent("任务会保留");
    expect(dialog).toHaveTextContent(/API 令牌.*撤销/);
    expect(dialog).toHaveTextContent(/脚本.*失效/);
    await user.click(within(dialog).getByRole("button", { name: "取消" }));
    expect(writes).toHaveLength(0);
    expect(client.getQueryState(["tokens"])?.isInvalidated).toBe(false);
    await user.click(screen.getByRole("button", { name: "删除标签 bug" }));
    await user.click(within(screen.getByRole("dialog", { name: "删除标签" })).getByRole("button", { name: "删除" }));
    await waitFor(() => expect(screen.queryByRole("button", { name: "删除标签 bug" })).not.toBeInTheDocument());
    expect(writes.map((write) => write.url)).toEqual(["/api/v1/tags/2"]);
    expect(client.getQueryState(["tokens"])?.isInvalidated).toBe(true);
  });
});

it("paginates trash, retries errors and restores immediately with cache invalidation", async () => {
  let restored = false;
  let failing = true;
  const deleted = { ...task(), deleted_at: "2026-11-15T06:00:00Z" };
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    if (url.includes("/trash?")) {
      if (url.includes("cursor=older")) return failing ? json({ error: { message: "加载失败" } }, 503) : json({ tasks: [{ ...deleted, id: 2, title: "更早的卡" }], next_cursor: null });
      return json({ tasks: restored ? [] : [deleted], next_cursor: restored ? null : "older" });
    }
    if (url.endsWith("/tasks/1/restore") && init?.method === "POST") { restored = true; return json(task()); }
    return json({});
  }));
  const { user, client } = mount("trash");
  expect(await screen.findByText("修复登录")).toBeInTheDocument();
  expect(screen.getByText(/30 天/)).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "加载更多" }));
  expect(await screen.findByRole("alert")).toHaveTextContent("加载失败");
  failing = false;
  await user.click(screen.getByRole("button", { name: "重试" }));
  expect(await screen.findByText("更早的卡")).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "恢复任务 修复登录" }));
  await waitFor(() => expect(restored).toBe(true));
  await waitFor(() => expect(screen.queryByText("修复登录")).not.toBeInTheDocument());
  expect(client.getQueryState(["tasks"])?.isInvalidated).toBe(true);
});

it("sends token resource scopes, displays their names and keeps the secret once-only", async () => {
  let payload: Record<string, unknown> | undefined;
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    if (url.includes("/projects")) return json({ projects: [project] });
    if (url.endsWith("/tags")) return json({ tags: [tag] });
    if (url.endsWith("/tokens") && init?.method === "POST") { payload = JSON.parse(String(init.body)) as Record<string, unknown>; return json({ token: "tk_secret_once" }, 201); }
    if (url.endsWith("/tokens")) return json({ tokens: [{ id: 1, name: "受限脚本", prefix: "tk_prefix", scopes: ["read"], project_ids: [1], tag_ids: [2], created_at: "2026-11-16T06:00:00Z", last_used_at: null }] });
    return json({});
  }));
  const view = mount("tokens");
  await view.user.type(screen.getByLabelText("名称"), "agent");
  await view.user.click(await screen.findByLabelText("项目：Codeg"));
  await view.user.click(screen.getByLabelText("标签：bug"));
  await view.user.click(screen.getByRole("button", { name: "生成令牌" }));
  expect(await screen.findByText("tk_secret_once")).toBeInTheDocument();
  expect(payload).toMatchObject({ name: "agent", project_ids: [1], tag_ids: [2] });
  expect(screen.getByText(/项目：Codeg · 标签：bug/)).toBeInTheDocument();
  view.unmount();
  mount("tokens");
  await screen.findByText("受限脚本");
  expect(screen.queryByText("tk_secret_once")).not.toBeInTheDocument();
});

it("sends empty resource arrays when a token is unrestricted", async () => {
  let payload: Record<string, unknown> | undefined;
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    if (String(input).endsWith("/tokens") && init?.method === "POST") { payload = JSON.parse(String(init.body)) as Record<string, unknown>; return json({ token: "tk_unrestricted" }, 201); }
    return json({ tokens: [], projects: [], tags: [] });
  }));
  const { user } = mount("tokens");
  await user.type(screen.getByLabelText("名称"), "我的脚本");
  await user.click(screen.getByRole("button", { name: "生成令牌" }));
  await screen.findByText("tk_unrestricted");
  expect(payload).toMatchObject({ project_ids: [], tag_ids: [] });
});

it("keeps archived project names readable but prevents selecting them for a new token", async () => {
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
    if (String(input).includes("/projects")) return json({ projects: [{ ...project, archived_at: "2026-11-16T06:00:00Z" }] });
    if (String(input).endsWith("/tokens")) return json({ tokens: [{ id: 1, name: "既有令牌", prefix: "tk_prefix", scopes: ["read"], project_ids: [1], tag_ids: [], created_at: "2026-11-16T06:00:00Z", last_used_at: null }] });
    return json({ tags: [] });
  }));
  mount("tokens");
  expect(await screen.findByLabelText("项目：Codeg")).toBeDisabled();
  expect(screen.getByText(/项目：Codeg · 标签：不限/)).toBeInTheDocument();
});
