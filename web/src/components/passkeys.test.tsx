import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { LoginForm } from "./login-form";
import { SettingsPage } from "./settings-page";
import { json, me, renderWithClient } from "../test/fixtures";

const bytes = (values: number[]) => new Uint8Array(values).buffer;
const assertion = {
  id: "-_8", type: "public-key", rawId: bytes([251, 255]), authenticatorAttachment: "platform",
  getClientExtensionResults: () => ({}),
  response: { clientDataJSON: bytes([1, 2]), authenticatorData: bytes([3, 4]), signature: bytes([5, 6]), userHandle: bytes([7, 8]) },
} as unknown as PublicKeyCredential;
const attestation = {
  ...assertion,
  getClientExtensionResults: () => ({ credProps: { rk: true } }),
  response: { clientDataJSON: bytes([1, 2]), attestationObject: bytes([9, 10]), getTransports: () => ["internal"] },
} as unknown as PublicKeyCredential;

function nativeCredentials() {
  vi.stubGlobal("isSecureContext", true);
  vi.stubGlobal("PublicKeyCredential", class {});
  const credentials = { get: vi.fn<(options?: CredentialRequestOptions) => Promise<Credential | null>>().mockResolvedValue(assertion), create: vi.fn<(options?: CredentialCreationOptions) => Promise<Credential | null>>().mockResolvedValue(attestation) };
  vi.stubGlobal("navigator", { credentials });
  return credentials;
}

function backend() {
  let passkeys: Array<{ id: string; name: string; created_at: string; last_used_at: string | null }> = [];
  const writes: Array<{ path: string; body: Record<string, unknown> }> = [];
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input);
    if (init?.method) writes.push({ path, body: JSON.parse(String(init.body ?? "{}")) as Record<string, unknown> });
    if (path.endsWith("/auth/passkey/begin")) return json({ challenge_id: "login-challenge", publicKey: { challenge: "AQID", rpId: "localhost", userVerification: "required", allowCredentials: [{ id: "-_8", type: "public-key", transports: ["internal"] }] } });
    if (path.endsWith("/auth/passkey/finish")) return json({ user: { username: "admin" } });
    if (path.endsWith("/auth/sudo")) return json({});
    if (path.endsWith("/passkeys/register/begin")) return json({ challenge_id: "registration-challenge", publicKey: { challenge: "AQID", rp: { name: "Tack", id: "localhost" }, user: { id: "BAUG", name: "admin", displayName: "admin" }, pubKeyCredParams: [{ type: "public-key", alg: -7 }], excludeCredentials: [{ id: "-_8", type: "public-key" }], authenticatorSelection: { userVerification: "required" }, extensions: { credProps: true } } });
    if (path.endsWith("/passkeys/register/finish")) { passkeys = [{ id: "key-1", name: "我的钥匙", created_at: "2026-11-16T06:00:00Z", last_used_at: null }]; return json({ passkey: passkeys[0] }, 201); }
    if (path.endsWith("/passkeys/key-1") && init?.method === "DELETE") { passkeys = []; return json(null, 204); }
    if (path.endsWith("/passkeys")) return json({ passkeys });
    if (path.endsWith("/sessions")) return json({ sessions: [] });
    return json({});
  }));
  return writes;
}

afterEach(() => vi.unstubAllGlobals());

describe("native Passkey flow", () => {
  it("keeps password login available when unsupported or insecure", () => {
    vi.stubGlobal("isSecureContext", false);
    renderWithClient(<LoginForm onSuccess={vi.fn()} />);
    expect(screen.getByLabelText("密码")).toBeEnabled();
    expect(screen.queryByRole("button", { name: "使用 Passkey 登录" })).not.toBeInTheDocument();
    expect(screen.getByText(/仍可使用密码/)).toBeInTheDocument();
  });

  it("decodes assertion options and sends standard base64url credential JSON", async () => {
    const user = userEvent.setup();
    const native = nativeCredentials();
    const writes = backend();
    const success = vi.fn();
    renderWithClient(<LoginForm onSuccess={success} />);
    await user.click(screen.getByRole("button", { name: "使用 Passkey 登录" }));
    await waitFor(() => expect(success).toHaveBeenCalledWith({ user: { username: "admin" } }));
    const options = native.get.mock.calls[0][0];
    expect(options?.publicKey?.challenge).toEqual(bytes([1, 2, 3]));
    expect(options?.publicKey?.allowCredentials?.[0].id).toEqual(bytes([251, 255]));
    expect(options?.signal).toBeInstanceOf(AbortSignal);
    expect(writes.at(-1)?.body).toEqual({ challenge_id: "login-challenge", credential: { id: "-_8", rawId: "-_8", type: "public-key", authenticatorAttachment: "platform", clientExtensionResults: {}, response: { clientDataJSON: "AQI", authenticatorData: "AwQ", signature: "BQY", userHandle: "Bwg" } } });
  });

  it("cancels cleanly without submitting a finish request and allows retry", async () => {
    const user = userEvent.setup();
    const native = nativeCredentials();
    native.get.mockRejectedValueOnce(new DOMException("User cancelled", "NotAllowedError"));
    const writes = backend();
    const success = vi.fn();
    renderWithClient(<LoginForm onSuccess={success} />);
    await user.click(screen.getByRole("button", { name: "使用 Passkey 登录" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(/取消|超时/);
    expect(writes.some((write) => write.path.endsWith("/finish"))).toBe(false);
    expect(screen.getByRole("button", { name: "进入看板" })).toBeEnabled();
    await user.click(screen.getByRole("button", { name: "使用 Passkey 登录" }));
    await waitFor(() => expect(success).toHaveBeenCalledOnce());
  });

  it("aborts the native prompt on unmount without authenticating late", async () => {
    const user = userEvent.setup();
    const native = nativeCredentials();
    native.get.mockImplementation((options) => new Promise((_resolve, reject) => options?.signal?.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError")))));
    const writes = backend();
    const success = vi.fn();
    const view = renderWithClient(<LoginForm onSuccess={success} />);
    await user.click(screen.getByRole("button", { name: "使用 Passkey 登录" }));
    await waitFor(() => expect(native.get).toHaveBeenCalledOnce());
    const signal = native.get.mock.calls[0][0]?.signal;
    view.unmount();
    expect(signal?.aborted).toBe(true);
    expect(success).not.toHaveBeenCalled();
    expect(writes.some((write) => write.path.endsWith("/finish"))).toBe(false);
  });

  it("enrolls, lists and removes a Passkey with sudo and serialized attestation", async () => {
    const user = userEvent.setup();
    const native = nativeCredentials();
    const writes = backend();
    renderWithClient(<SettingsPage me={me} path="/settings/account" navigate={vi.fn()} onLogout={vi.fn()} />);
    await user.type(await screen.findByLabelText("Passkey 名称"), "我的钥匙");
    await user.click(screen.getByRole("button", { name: "添加 Passkey" }));
    await user.type(screen.getByLabelText("当前密码", { selector: "#sudo-password" }), "password");
    await user.click(screen.getByRole("button", { name: "验证" }));
    expect(await screen.findByRole("button", { name: "移除 Passkey 我的钥匙" })).toBeInTheDocument();
    const options = native.create.mock.calls[0][0]?.publicKey;
    expect(options?.challenge).toEqual(bytes([1, 2, 3]));
    expect(options?.user.id).toEqual(bytes([4, 5, 6]));
    expect(options?.excludeCredentials?.[0].id).toEqual(bytes([251, 255]));
    expect(writes.find((write) => write.path.endsWith("/register/begin"))?.body).toEqual({ name: "我的钥匙" });
    expect(writes.find((write) => write.path.endsWith("/register/finish"))?.body).toMatchObject({ challenge_id: "registration-challenge", credential: { rawId: "-_8", response: { clientDataJSON: "AQI", attestationObject: "CQo", transports: ["internal"] }, clientExtensionResults: { credProps: { rk: true } } } });
    await user.click(screen.getByRole("button", { name: "移除 Passkey 我的钥匙" }));
    await user.type(screen.getByLabelText("当前密码", { selector: "#sudo-password" }), "password");
    await user.click(screen.getByRole("button", { name: "验证" }));
    await waitFor(() => expect(screen.queryByRole("button", { name: "移除 Passkey 我的钥匙" })).not.toBeInTheDocument());
    expect(writes.filter((write) => write.path.endsWith("/auth/sudo"))).toHaveLength(2);
  });
});
