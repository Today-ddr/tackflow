import { useRef, useState } from "react";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vitest";
import { CaptureComposer } from "./capture-composer";
import { json, renderWithClient, task } from "../test/fixtures";

afterEach(() => vi.unstubAllGlobals());

function Composer() {
  const [text, setText] = useState("");
  const inputRef = useRef<HTMLTextAreaElement>(null);
  return <CaptureComposer text={text} onTextChange={setText} inputRef={inputRef} autoFocus />;
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
