import { QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import App from "./App";
import { createQueryClient } from "./query";

function jsonResponse(body: unknown, status: number): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function renderApp() {
  return render(
    <QueryClientProvider client={createQueryClient()}>
      <App />
    </QueryClientProvider>,
  );
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("Tack login", () => {
  it("shows credentials before a session exists", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        jsonResponse(
          { error: { code: "unauthorized", message: "请先登录", request_id: "req_test" } },
          401,
        ),
      ),
    );

    renderApp();

    expect(await screen.findByLabelText("用户名")).toBeInTheDocument();
    expect(screen.getByLabelText("密码")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "进入看板" })).toBeInTheDocument();
  });

  it("renders the shell after a successful login", async () => {
    let loginCalls = 0;
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input);
        if (url.endsWith("/api/v1/auth/me")) {
          if (loginCalls === 0) {
            return jsonResponse(
              { error: { code: "unauthorized", message: "请先登录", request_id: "req_one" } },
              401,
            );
          }
          return jsonResponse(
            { user: { username: "admin" }, sudo_expires_at: null },
            200,
          );
        }
        if (url.endsWith("/api/v1/auth/login")) {
          loginCalls++;
          return jsonResponse({ user: { username: "admin" } }, 200);
        }
        return jsonResponse({ error: { code: "not_found", message: "不存在", request_id: "req" } }, 404);
      }),
    );

    renderApp();
    const user = userEvent.setup();
    const username = await screen.findByLabelText("用户名");
    await user.clear(username);
    await user.type(username, "admin");
    await user.type(screen.getByLabelText("密码"), "correct horse battery staple");
    await user.click(screen.getByRole("button", { name: "进入看板" }));

    expect(await screen.findByText("admin")).toBeInTheDocument();
    expect(screen.getAllByText("收件箱").length).toBeGreaterThan(0);
    expect(screen.getByRole("button", { name: "退出" })).toBeInTheDocument();
  });

  it("keeps the form usable after a rejected password", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input);
        if (url.endsWith("/api/v1/auth/me")) {
          return jsonResponse(
            { error: { code: "unauthorized", message: "请先登录", request_id: "req_one" } },
            401,
          );
        }
        return jsonResponse(
          { error: { code: "invalid_credentials", message: "用户名或密码错误", request_id: "req_two" } },
          401,
        );
      }),
    );

    renderApp();
    const user = userEvent.setup();
    const username = await screen.findByLabelText("用户名");
    await user.clear(username);
    await user.type(username, "admin");
    await user.type(screen.getByLabelText("密码"), "wrong");
    await user.click(screen.getByRole("button", { name: "进入看板" }));

    await waitFor(() => {
      expect(screen.getByRole("alert")).toHaveTextContent("用户名或密码错误");
    });
    expect(screen.getByLabelText("用户名")).toBeEnabled();
  });
});
