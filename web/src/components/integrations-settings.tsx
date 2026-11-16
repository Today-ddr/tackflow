import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, errorMessage } from "../lib/api";
import type { Webhook, WebhookDelivery } from "../lib/integrations";
import { formatDateTime } from "../lib/format";
import { QueryFeedback } from "./query-feedback";
import { SecretNotice } from "./secret-notice";
import { SudoDialog } from "./sudo-dialog";
import { Button } from "./ui/button";
import { Dialog } from "./ui/dialog";
import { Input } from "./ui/input";

const eventOptions = [{ value: "task.*", label: "任务变化" }, { value: "project.*", label: "项目变化" }, { value: "tag.*", label: "标签变化" }, { value: "*", label: "全部变化" }];
type WebhookAction = { kind: "delete" | "toggle"; webhook: Webhook };

export function IntegrationsSettings() {
  const client = useQueryClient();
  const [editor, setEditor] = useState<Webhook | null | undefined>(undefined);
  const [action, setAction] = useState<WebhookAction | null>(null);
  const [deliveries, setDeliveries] = useState<Webhook | null>(null);
  const [secret, setSecret] = useState("");
  const query = useQuery({ queryKey: ["webhooks"], queryFn: ({ signal }) => api<{ webhooks: Webhook[] }>("/webhooks", { signal }) });
  const mutation = useMutation({
    mutationFn: (action: WebhookAction) => api<{ webhook: Webhook } | void>(`/webhooks/${action.webhook.id}`, action.kind === "delete" ? { method: "DELETE" } : { method: "PATCH", body: JSON.stringify({ enabled: !action.webhook.enabled }) }),
    onSuccess: () => client.invalidateQueries({ queryKey: ["webhooks"] }),
  });
  return <div className="settings-grid">
    <section className="settings-card settings-card-wide">
      <div className="settings-card-heading"><h2>Webhook</h2><div className="settings-button-row"><button className="text-button" type="button" disabled={query.isFetching} onClick={() => void query.refetch()}>刷新</button><Button variant="quiet" type="button" onClick={() => setEditor(null)}>新建 Webhook</Button></div></div>
      <p className="settings-copy">任务变化时通知接收服务。签名密钥仅在创建后显示一次，请妥善保存。</p>
      <QueryFeedback pending={query.isPending} error={query.error} retry={() => void query.refetch()} />
      {mutation.error ? <p className="form-error" role="alert">{errorMessage(mutation.error)}</p> : null}
      {secret ? <SecretNotice key={secret} secret={secret} onDismiss={() => setSecret("")} /> : null}
      {query.data?.webhooks.length === 0 ? <p className="muted-copy">尚未添加 Webhook。</p> : null}
      <div className="management-list">{query.data?.webhooks.map((webhook) => <div className="webhook-item" key={webhook.id}>
        <div className="management-main"><strong>{webhook.name} · {webhook.enabled ? "已启用" : "已停用"}</strong><small>{webhook.url}</small><small>{webhook.events.map((event) => eventOptions.find((item) => item.value === event)?.label ?? event).join("、")} · {deliveryStatus(webhook.last_status)}{webhook.pending_count ? ` · ${webhook.pending_count} 次待投递` : ""}</small>{webhook.last_error ? <small className="form-error">{webhook.last_error}</small> : null}</div>
        <div className="settings-button-row"><button className="text-button" type="button" aria-label={`投递记录 ${webhook.name}`} onClick={() => setDeliveries(webhook)}>投递记录</button><button className="text-button" type="button" disabled={mutation.isPending} aria-label={`编辑 Webhook ${webhook.name}`} onClick={() => setEditor(webhook)}>编辑</button><button className="text-button" type="button" disabled={mutation.isPending} aria-label={`${webhook.enabled ? "停用" : "启用"} Webhook ${webhook.name}`} onClick={() => { mutation.reset(); setAction({ kind: "toggle", webhook }); }}>{webhook.enabled ? "停用" : "启用"}</button><button className="text-button text-button-danger" type="button" disabled={mutation.isPending} aria-label={`删除 Webhook ${webhook.name}`} onClick={() => { mutation.reset(); setAction({ kind: "delete", webhook }); }}>删除</button></div>
      </div>)}</div>
    </section>
    <section className="settings-card settings-card-wide"><div className="settings-card-heading"><h2>Codeg bridge</h2></div><p className="settings-copy">需要在运行 Codeg 的电脑上单独配置本地 CLI。只有待办中明确开启「允许 agent 领取」（agent_ready）的任务才会交给它执行。</p><p className="settings-copy">执行结果和关联链接会回到任务详情；此页面不会启动 agent。</p></section>
    {editor !== undefined ? <WebhookEditor webhook={editor} onClose={() => setEditor(undefined)} onCreated={setSecret} /> : null}
    {deliveries ? <DeliveryDialog webhook={deliveries} onClose={() => setDeliveries(null)} /> : null}
    <SudoDialog open={action !== null} title={action?.kind === "delete" ? "删除 Webhook" : `${action?.webhook.enabled ? "停用" : "启用"} Webhook`} description={action?.kind === "delete" ? "删除后将停止待投递通知，任务会保留。" : "确认身份后更改通知状态。停用后停止待投递通知。"} onClose={() => setAction(null)} onSuccess={() => { if (action) mutation.mutate(action); }} />
  </div>;
}

function WebhookEditor({ webhook, onClose, onCreated }: { webhook: Webhook | null; onClose: () => void; onCreated: (secret: string) => void }) {
  const client = useQueryClient();
  const [name, setName] = useState(webhook?.name ?? "");
  const [url, setURL] = useState(webhook?.url ?? "");
  const [events, setEvents] = useState(webhook?.events ?? ["task.*"]);
  const [sudo, setSudo] = useState(false);
  const mutation = useMutation({
    mutationFn: () => api<{ webhook: Webhook; secret?: string }>(webhook ? `/webhooks/${webhook.id}` : "/webhooks", { method: webhook ? "PATCH" : "POST", body: JSON.stringify({ name: name.trim(), url: url.trim(), events }) }),
    onSuccess: async (result) => {
      if (result.secret) onCreated(result.secret);
      await client.invalidateQueries({ queryKey: ["webhooks"] });
      onClose();
    },
  });
  return <><Dialog open title={webhook ? "编辑 Webhook" : "新建 Webhook"} onClose={() => { if (!mutation.isPending) onClose(); }} footer={<div className="dialog-actions"><Button variant="quiet" type="button" disabled={mutation.isPending} onClick={onClose}>取消</Button><Button type="submit" form="webhook-form" disabled={!name.trim() || !url.trim() || !events.length || mutation.isPending}>{mutation.isPending ? "保存中…" : "保存 Webhook"}</Button></div>}>
    <form id="webhook-form" className="form-stack" onSubmit={(event) => { event.preventDefault(); if (name.trim() && url.trim() && events.length && !mutation.isPending) setSudo(true); }}>
      <div className="field"><label htmlFor="webhook-name">Webhook 名称</label><Input id="webhook-name" autoFocus maxLength={80} required value={name} onChange={(event) => setName(event.target.value)} /></div>
      <div className="field"><label htmlFor="webhook-url">接收地址</label><Input id="webhook-url" type="url" required placeholder="https://" value={url} onChange={(event) => setURL(event.target.value)} /></div>
      <p className="settings-copy">使用公网 HTTPS 地址。接收端需用签名密钥验证通知。</p>
      <fieldset className="resource-scope"><legend>订阅变化</legend><div className="scope-row">{eventOptions.map((item) => <label className="check-chip" key={item.value}><input type="checkbox" checked={events.includes(item.value)} onChange={(event) => setEvents((current) => event.target.checked ? item.value === "*" ? ["*"] : [...current.filter((value) => value !== "*"), item.value] : current.filter((value) => value !== item.value))} />{item.label}</label>)}</div></fieldset>
      {mutation.error ? <p className="form-error" role="alert">{errorMessage(mutation.error)}</p> : null}
    </form>
  </Dialog><SudoDialog open={sudo} title="保存 Webhook" onClose={() => setSudo(false)} onSuccess={() => mutation.mutate()} /></>;
}

function DeliveryDialog({ webhook, onClose }: { webhook: Webhook; onClose: () => void }) {
  const query = useQuery({ queryKey: ["webhooks", webhook.id, "deliveries"], queryFn: ({ signal }) => api<{ deliveries: WebhookDelivery[] }>(`/webhooks/${webhook.id}/deliveries`, { signal }), refetchInterval: 15_000 });
  return <Dialog open title="投递记录" subtitle={webhook.name} onClose={onClose} footer={<div className="dialog-actions"><Button variant="quiet" type="button" onClick={onClose}>关闭</Button><Button type="button" disabled={query.isFetching} onClick={() => void query.refetch()}>刷新</Button></div>}>
    <p className="settings-copy">失败后自动退避重试，最多尝试 5 次。停用后停止待投递通知。</p>
    <QueryFeedback pending={query.isPending} error={query.error} retry={() => void query.refetch()} />
    {query.data?.deliveries.length === 0 ? <p className="muted-copy">还没有投递记录。</p> : null}
    <div className="management-list">{query.data?.deliveries.map((delivery) => <div className="webhook-item" key={delivery.id}><div className="management-main"><strong>#{delivery.id} · {deliveryStatus(delivery.status)}</strong><small>已尝试 {delivery.attempts} 次 · 事件 #{delivery.event_id}</small>{delivery.status === "pending" && delivery.next_attempt_at ? <small>下次重试 {formatDateTime(delivery.next_attempt_at)}</small> : null}{delivery.delivered_at ? <small>送达于 {formatDateTime(delivery.delivered_at)}</small> : null}{delivery.last_error ? <small className="form-error">{delivery.last_error}</small> : null}</div></div>)}</div>
  </Dialog>;
}

function deliveryStatus(status?: string): string {
  switch (status) {
    case "pending": return "等待投递";
    case "in_flight": return "投递中";
    case "delivered": return "已送达";
    case "failed": return "投递失败";
    case "cancelled": return "已停止";
    default: return status || "尚未投递";
  }
}
