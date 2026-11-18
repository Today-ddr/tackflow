import { expect, it } from "vitest";
import {
  isCaptureTokenName,
  setCaptureDue,
  setCapturePriority,
  setCaptureProject,
  toggleCaptureTag,
} from "./capture-edit";

it("sets, replaces and clears a project on the first line only", () => {
  expect(setCaptureProject("修登录", "codeg")).toBe("修登录 @codeg");
  expect(setCaptureProject("修登录 @codeg", "api")).toBe("修登录 @api");
  expect(setCaptureProject("修登录 @codeg @api", "web")).toBe("修登录 @web");
  expect(setCaptureProject("修登录 @codeg\n备注里保留 @codeg", null)).toBe("修登录\n备注里保留 @codeg");
});

it("toggles tags without matching longer tag names", () => {
  expect(toggleCaptureTag("修登录 #bug", "bug")).toBe("修登录");
  expect(toggleCaptureTag("修登录 #bugfix", "bug")).toBe("修登录 #bugfix #bug");
  expect(toggleCaptureTag("修登录", "前端")).toBe("修登录 #前端");
});

it("replaces and clears priority and due date", () => {
  expect(setCapturePriority("修登录 !p1", 3)).toBe("修登录 !p3");
  expect(setCapturePriority("修登录 !p1 !p3", 2)).toBe("修登录 !p2");
  expect(setCapturePriority("修登录 !p1", 0)).toBe("修登录");
  expect(setCaptureDue("修登录 ^今天", "明天")).toBe("修登录 ^明天");
  expect(setCaptureDue("修登录 ^今天 ^11-20", "后天")).toBe("修登录 ^后天");
  expect(setCaptureDue("修登录 ^今天", null)).toBe("修登录");
});

it("keeps notes intact when editing headline tokens", () => {
  expect(setCaptureDue("修登录\n第二行 ^今天", "明天")).toBe("修登录 ^明天\n第二行 ^今天");
});

it("rejects names the capture grammar cannot represent", () => {
  expect(isCaptureTokenName("codeg")).toBe(true);
  expect(isCaptureTokenName("codeg-2")).toBe(true);
  expect(isCaptureTokenName("设计 系统")).toBe(false);
  expect(isCaptureTokenName("客户/项目")).toBe(false);
});
