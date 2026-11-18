import { useRef, useState } from "react";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vitest";
import { CaptureComposer } from "./capture-composer";
import { json, project, renderWithClient, tag, task } from "../test/fixtures";

afterEach(() => vi.unstubAllGlobals());

function Composer() {
  const [text, setText] = useState("");
  const inputRef = useRef<HTMLTextAreaElement>(null);
  return <CaptureComposer text={text} onTextChange={setText} inputRef={inputRef} projects={[project]} tags={[tag]} autoFocus />;
}

it("submits capture grammar once and restores an empty focused composer", async () => {
  const calls: RequestInit[] = [];
  let finish: ((value: Response) => void) | undefined;
  vi.stubGlobal("fetch", vi.fn((_input: RequestInfo | URL, init?: RequestInit) => {
    if (init) calls.push(init);
    return new Promise<Response>((resolve) => { finish = resolve; });
  }));
  renderWithClient(<Composer />);
  const user = userEvent.setup();
  const input = screen.getByRole("textbox", { name: "记一笔" });
  expect(input).toHaveFocus();
  await user.type(input, "整理看板 #ui !p2 ^明天");
  expect(screen.getByText("#ui")).toBeInTheDocument();
  fireEvent.keyDown(input, { key: "Enter", ctrlKey: true, isComposing: true });
  expect(calls).toHaveLength(0);
  fireEvent.keyDown(input, { key: "Enter", ctrlKey: true });
  await waitFor(() => expect(calls).toHaveLength(1));
  fireEvent.keyDown(input, { key: "Enter", ctrlKey: true });
  expect(calls).toHaveLength(1);
  expect(JSON.parse(String(calls[0].body))).toEqual({ text: "整理看板 #ui !p2 ^明天" });
  finish?.(json(task(), 201));
  await waitFor(() => expect(input).toHaveValue(""));
  expect(input).toHaveFocus();
});

it("keeps failed input for retry", async () => {
  vi.stubGlobal("fetch", vi.fn(async () => json({ error: { message: "暂时无法保存" } }, 503)));
  renderWithClient(<Composer />);
  const user = userEvent.setup();
  await user.type(screen.getByRole("textbox", { name: "记一笔" }), "不能丢失的草稿");
  await user.click(screen.getByRole("button", { name: "存进收件箱" }));
  expect(await screen.findByRole("alert")).toHaveTextContent("暂时无法保存");
  expect(screen.getByRole("textbox", { name: "记一笔" })).toHaveValue("不能丢失的草稿");
});

it("adds project, tag, priority and due date from menus while keeping manual text editable", async () => {
  renderWithClient(<Composer />);
  const user = userEvent.setup();
  const input = screen.getByRole("textbox", { name: "记一笔" });
  await user.type(input, "整理看板");

  await user.click(screen.getByRole("button", { name: /^选择项目/ }));
  await user.click(screen.getByRole("menuitemradio", { name: "Codeg" }));
  expect(input).toHaveValue("整理看板 @Codeg");
  expect(input).toHaveFocus();

  await user.click(screen.getByRole("button", { name: /^选择标签/ }));
  await user.click(screen.getByRole("menuitemcheckbox", { name: "#bug" }));
  expect(input).toHaveValue("整理看板 @Codeg #bug");

  await user.click(screen.getByRole("button", { name: /^选择优先级/ }));
  await user.click(screen.getByRole("menuitemradio", { name: "P2 中" }));
  expect(input).toHaveValue("整理看板 @Codeg #bug !p2");

  await user.click(screen.getByRole("button", { name: /^选择日期/ }));
  await user.click(screen.getByRole("menuitemradio", { name: "明天" }));
  expect(input).toHaveValue("整理看板 @Codeg #bug !p2 ^明天");

  await user.type(input, " 补充");
  expect(input).toHaveValue("整理看板 @Codeg #bug !p2 ^明天 补充");
});

it("removes an already selected tag from its menu", async () => {
  renderWithClient(<Composer />);
  const user = userEvent.setup();
  const input = screen.getByRole("textbox", { name: "记一笔" });
  await user.type(input, "整理看板 #bug");
  await user.click(screen.getByRole("button", { name: /^选择标签/ }));
  const selected = screen.getByRole("menuitemcheckbox", { name: "#bug" });
  expect(selected).toHaveAttribute("aria-checked", "true");
  await user.click(selected);
  expect(input).toHaveValue("整理看板");
});
