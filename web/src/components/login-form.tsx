import { useMutation } from "@tanstack/react-query";
import { useState } from "react";
import { ArrowRight } from "lucide-react";

import { api, ApiError, type LoginResponse } from "../lib/api";
import { Button } from "./ui/button";
import { Input } from "./ui/input";
import { loginWithPasskey, supportsPasskeys } from "../lib/passkeys";
import { usePasskey } from "../lib/use-passkey";

interface LoginFormProps {
  onSuccess: (result: LoginResponse) => void;
}

export function LoginForm({ onSuccess }: LoginFormProps) {
  const [username, setUsername] = useState("admin");
  const [password, setPassword] = useState("");
  const passkey = usePasskey(loginWithPasskey, onSuccess);
  const mutation = useMutation({
    mutationFn: (credentials: { username: string; password: string }) =>
      api<LoginResponse>("/auth/login", {
        method: "POST",
        body: JSON.stringify(credentials),
      }),
    onSuccess,
  });

  const errorMessage =
    mutation.error instanceof ApiError
      ? mutation.error.message
      : mutation.error
        ? "暂时连不上 Tack，请稍后再试"
        : "";

  return (
    <main className="login-page">
      <section className="login-card" aria-labelledby="login-title">
        <div className="login-intro">
          <p className="eyebrow">个人任务看板</p>
          <h1 id="login-title">Tack</h1>
          <p className="login-tagline">
            把念头钉住。写下，归档，继续。
          </p>
        </div>

        <form
          className="login-form"
          onSubmit={(event) => {
            event.preventDefault();
            if (!passkey.pending && !mutation.isPending) mutation.mutate({ username, password });
          }}
        >
          <div className="field">
            <label htmlFor="username">用户名</label>
            <Input
              id="username"
              name="username"
              autoComplete="username"
              autoFocus
              value={username}
              onChange={(event) => setUsername(event.target.value)}
            />
          </div>
          <div className="field">
            <label htmlFor="password">密码</label>
            <Input
              id="password"
              name="password"
              type="password"
              autoComplete="current-password"
              value={password}
              onChange={(event) => setPassword(event.target.value)}
            />
          </div>
          {errorMessage ? (
            <p className="form-error" role="alert">
              {errorMessage}
            </p>
          ) : null}
          <Button type="submit" disabled={mutation.isPending || passkey.pending}>
            {mutation.isPending ? "正在进入…" : "进入看板"}
            {!mutation.isPending ? <ArrowRight size={16} aria-hidden /> : null}
          </Button>
        </form>

        <div className="passkey-login">
          {supportsPasskeys() ? <div className="settings-button-row"><Button variant="quiet" type="button" disabled={passkey.pending || mutation.isPending} onClick={() => void passkey.run()}>{passkey.pending ? "等待设备确认…" : "使用 Passkey 登录"}</Button>{passkey.pending ? <button className="text-button" type="button" onClick={passkey.cancel}>取消</button> : null}</div> : <p className="settings-copy">当前浏览器或连接不支持 Passkey，仍可使用密码登录。</p>}
          {passkey.error ? <p className="form-error" role="alert">{passkey.error}</p> : null}
        </div>

        <p className="login-note">单用户 · 数据留在自己的服务器</p>
      </section>
    </main>
  );
}
