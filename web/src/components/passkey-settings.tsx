import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Fingerprint } from "lucide-react";
import { api, errorMessage } from "../lib/api";
import { registerPasskey, supportsPasskeys, type Passkey } from "../lib/passkeys";
import { usePasskey } from "../lib/use-passkey";
import { formatDateTime } from "../lib/format";
import { QueryFeedback } from "./query-feedback";
import { SudoDialog } from "./sudo-dialog";
import { Input } from "./ui/input";
import { Button } from "./ui/button";
import { SettingsHeading } from "./settings-heading";

export function PasskeySettings() {
  const client = useQueryClient();
  const supported = supportsPasskeys();
  const [name, setName] = useState("");
  const [action, setAction] = useState<"register" | Passkey | null>(null);
  const query = useQuery({ queryKey: ["passkeys"], queryFn: ({ signal }) => api<{ passkeys: Passkey[] }>("/passkeys", { signal }) });
  const registration = usePasskey((signal) => registerPasskey(name.trim(), signal), () => { setName(""); void client.invalidateQueries({ queryKey: ["passkeys"] }); });
  const remove = useMutation({ mutationFn: (id: string) => api<void>(`/passkeys/${encodeURIComponent(id)}`, { method: "DELETE" }), onSuccess: () => client.invalidateQueries({ queryKey: ["passkeys"] }) });
  const pending = registration.pending || remove.isPending;
  return <section className="settings-card settings-card-wide">
    <SettingsHeading icon={Fingerprint} title="Passkey" />
    <p className="settings-copy">用设备指纹、面容或密码管理器登录。密码登录始终保留。</p>
    {supported ? <form className="form-stack" onSubmit={(event) => { event.preventDefault(); if (name.trim() && !pending) setAction("register"); }}>
      <div className="field"><label htmlFor="passkey-name">Passkey 名称</label><Input id="passkey-name" value={name} maxLength={80} placeholder="例如我的 Mac" disabled={pending} onChange={(event) => setName(event.target.value)} /></div>
      <div className="settings-button-row"><Button type="submit" disabled={!name.trim() || pending}>{registration.pending ? "等待设备确认…" : "添加 Passkey"}</Button>{registration.pending ? <Button variant="quiet" type="button" onClick={registration.cancel}>取消</Button> : null}</div>
    </form> : <p className="settings-copy">当前浏览器或连接不支持 Passkey，仍可使用密码登录。</p>}
    {registration.error || remove.error ? <p className="form-error" role="alert">{registration.error || errorMessage(remove.error)}</p> : null}
    <QueryFeedback pending={query.isPending} error={query.error} retry={() => void query.refetch()} />
    {query.data?.passkeys.length === 0 ? <p className="muted-copy">尚未添加 Passkey。</p> : null}
    <div className="management-list">{query.data?.passkeys.map((passkey) => <div className="management-item" key={passkey.id}>
      <div className="management-main"><strong>{passkey.name}</strong><small>创建于 {formatDateTime(passkey.created_at)} · {passkey.last_used_at ? `最近使用 ${formatDateTime(passkey.last_used_at)}` : "尚未使用"}</small></div>
      <button className="text-button text-button-danger" type="button" disabled={pending} aria-label={`移除 Passkey ${passkey.name}`} onClick={() => { remove.reset(); setAction(passkey); }}>移除</button>
    </div>)}</div>
    <SudoDialog open={action !== null} title={action === "register" ? "添加 Passkey" : "移除 Passkey"} description={action === "register" ? "验证密码后，在设备提示中创建 Passkey。" : "移除后这枚 Passkey 将无法登录，密码登录仍可使用。"} onClose={() => setAction(null)} onSuccess={() => { if (action === "register") void registration.run(); else if (action) remove.mutate(action.id); }} />
  </section>;
}
