import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vitest";
import { Board } from "./board";
import type { TasksResponse } from "../lib/api";
import { json, project, renderWithClient, tag, task } from "../test/fixtures";

afterEach(() => vi.unstubAllGlobals());

function TestBoard({ onOpen = vi.fn() }: { onOpen?: (id: number) => void }) {
  const [text, setText] = useState("");
  const query = useQuery<TasksResponse>({ queryKey: ["tasks"], queryFn: async () => ({ tasks: [task()], next_cursor: null }), initialData: { tasks: [task()], next_cursor: null }, enabled: false });
  return <Board tasks={query.data.tasks} projects={[project]} tags={[tag]} filter={{ kind: "all" }} query="" onOpenTask={onOpen} onOpenPalette={vi.fn()} onClearFilter={vi.fn()} captureText={text} onCaptureTextChange={setText} />;
}

it("moves a task via its status control and restores the previous state on failure", async () => {
  vi.stubGlobal("fetch", vi.fn(async () => json({ error: { message: "移动失败" } }, 503)));
  const open = vi.fn();
  renderWithClient(<TestBoard onOpen={open} />);
  const user = userEvent.setup();
  await user.selectOptions(screen.getByRole("combobox", { name: "更改状态：修复登录" }), "doing");
  await screen.findByText("移动失败");
  await waitFor(() => expect(screen.getByRole("combobox", { name: "更改状态：修复登录" })).toHaveValue("todo"));
  expect(open).not.toHaveBeenCalled();
});

it("focuses capture only from blank board space and leaves controls independent", async () => {
  const open = vi.fn();
  renderWithClient(<TestBoard onOpen={open} />);
  const user = userEvent.setup();
  const input = screen.getByRole("textbox", { name: "记一笔" });
  await user.click(screen.getByRole("region", { name: "任务看板" }));
  expect(input).toHaveFocus();
  await user.click(screen.getByRole("button", { name: "打开任务 修复登录" }));
  expect(input).not.toHaveFocus();
  expect(open).toHaveBeenCalledWith(1);
});

it("keeps only one inline task composer open across columns", async () => {
  renderWithClient(<TestBoard />);
  const user = userEvent.setup();
  const todoColumn = screen.getByRole("region", { name: "待办" });
  const inboxColumn = screen.getByRole("region", { name: "收件箱" });

  await user.click(within(todoColumn).getByRole("button", { name: "在待办新建任务" }));
  expect(within(todoColumn).getByRole("textbox", { name: "新建待办任务" })).toHaveFocus();

  await user.click(within(inboxColumn).getByRole("button", { name: "在收件箱新建任务" }));
  expect(within(inboxColumn).getByRole("textbox", { name: "新建收件箱任务" })).toHaveFocus();
  expect(screen.queryByRole("textbox", { name: "新建待办任务" })).not.toBeInTheDocument();
  expect(screen.getAllByRole("textbox", { name: /^新建.+任务$/ })).toHaveLength(1);
});

it("does not repeat a footer hint under the capture composer", () => {
  renderWithClient(<TestBoard />);
  expect(screen.queryByRole("button", { name: "输入语法" })).not.toBeInTheDocument();
  expect(screen.queryByText("⌘ / Ctrl ↵ 保存")).not.toBeInTheDocument();
  expect(screen.getByRole("textbox", { name: "记一笔" })).toHaveAttribute(
    "aria-describedby",
  );
});

it("keeps column actions in the header instead of repeating a new-task row", () => {
  renderWithClient(<TestBoard />);
  expect(screen.queryByRole("button", { name: "新建" })).not.toBeInTheDocument();
  for (const name of ["收件箱", "待办", "进行中", "已完成"]) {
    expect(screen.getByRole("button", { name: `在${name}新建任务` })).toBeInTheDocument();
  }
});
