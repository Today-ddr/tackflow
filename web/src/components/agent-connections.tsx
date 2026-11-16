import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, errorMessage, type APIToken, type Project, type Tag } from "../lib/api";
import { buildAgentGuide, checkAgentConnection, type AgentConnection, type ConnectionResources } from "../lib/agent-connections";
import { formatDateTime } from "../lib/format";
import { QueryFeedback } from "./query-feedback";
import { SecretNotice } from "./secret-notice";
import { SudoDialog } from "./sudo-dialog";
import { Button } from "./ui/button";
import { Dialog } from "./ui/dialog";
import { Input } from "./ui/input";
import { useToast } from "./toast";

const purposes = [
  { value: "read", label: "只读助手", scopes: ["read"], description: "读取任务、分类和导出，用于查询与总结。" },
  { value: "capture", label: "快速收集", scopes: ["capture"], description: "只向收件箱记一笔，适合快捷指令和通知脚本。" },
  { value: "write", label: "任务执行", scopes: ["write"], description: "读取、创建、修改和删除范围内任务，含收集、领取与回写；不限资源时也可维护分类。" },
  { value: "custom", label: "自定义", scopes: ["read"], description: "按需组合权限；write 已包含 read 和 capture。" },
];

export function AgentConnections() {
  const client = useQueryClient();
  const { notify } = useToast();
  const [name, setName] = useState("");
  const [purpose, setPurpose] = useState("read");
  const [scopes, setScopes] = useState<string[]>(["read"]);
  const [days, setDays] = useState("30");
  const [projectIDs, setProjectIDs] = useState<number[]>([]);
  const [tagIDs, setTagIDs] = useState<number[]>([]);
  const [password, setPassword] = useState("");
  const [revealed, setRevealed] = useState<{ secret: string; id: number } | null>(null);
  const [checkKey, setCheckKey] = useState("");
  const [pendingRevoke, setPendingRevoke] = useState<APIToken | null>(null);
  const [guideToken, setGuideToken] = useState<APIToken | null | undefined>(undefined);
  const projects = useQuery({ queryKey: ["projects", "all"], queryFn: ({ signal }) => api<{ projects: Project[] }>("/projects?include_archived=1", { signal }) });
  const tags = useQuery({ queryKey: ["tags"], queryFn: ({ signal }) => api<{ tags: Tag[] }>("/tags", { signal }) });
  const tokens = useQuery({ queryKey: ["tokens"], queryFn: ({ signal }) => api<{ tokens: APIToken[] }>("/tokens", { signal }) });
  const connection = useQuery({ queryKey: ["agent-connection"], queryFn: ({ signal }) => api<AgentConnection>("/agent/connection", { signal, cache: "no-store" }) });
  const baseURL = connection.data?.api_base_url;
  const resourcesReady = Boolean(projects.data && tags.data && !projects.error && !tags.error);
  const check = useMutation({
    gcTime: 0,
    mutationFn: async () => {
      try { return await checkAgentConnection(checkKey); }
      finally { setCheckKey(""); }
    },
    onSuccess: () => { void client.invalidateQueries({ queryKey: ["tokens"] }); },
  });
  const create = useMutation({
    gcTime: 0,
    mutationFn: async () => {
      const result = await api<{ token: string; api_token: APIToken }>("/tokens", {
        method: "POST",
        body: JSON.stringify({ name: name.trim(), scopes, password, project_ids: projectIDs, tag_ids: tagIDs, expires_at: Number(days) ? new Date(Date.now() + Number(days) * 86400000).toISOString() : null }),
      });
      setRevealed({ secret: result.token, id: result.api_token.id });
      setCheckKey(result.token);
      check.reset();
    },
    onSuccess: () => {
      setName("");
      setPassword("");
      setProjectIDs([]);
      setTagIDs([]);
      void client.invalidateQueries({ queryKey: ["tokens"] });
      notify("API Key 已生成，请保存后交给对应 Agent", "success");
    },
  });
  const revoke = useMutation({
    mutationFn: (id: number) => api<void>(`/tokens/${id}`, { method: "DELETE" }),
    onSuccess: (_result, id) => {
      if (revealed?.id === id) setRevealed(null);
      setCheckKey("");
      check.reset();
      void client.invalidateQueries({ queryKey: ["tokens"] });
      notify("API Key 已撤销", "success");
    },
  });

  return <div className="settings-grid agent-connections">
    <section className="settings-card settings-card-wide">
      <div className="settings-card-heading"><h2>Agent 连接</h2><span className="status-pill">REST API v1</span></div>
      <p className="settings-copy">每个 Agent 或脚本使用独立 API Key，便于限制权限、查看最近请求和单独撤销。</p>
      <ol className="connection-steps"><li>生成专用 Key，选择所需权限与范围。</li><li>在 Agent 的环境变量中保存 Key，复制接入说明。</li><li>先检查认证，再按需读取、收集或领取任务。</li></ol>
      <QueryFeedback pending={connection.isPending} error={connection.error} retry={() => void connection.refetch()} />
      {baseURL ? <div className="connection-address"><span>API 地址</span><code>{baseURL}</code><Button variant="quiet" type="button" onClick={() => setGuideToken(null)}>查看通用说明</Button></div> : null}
      <p className="settings-copy">当前支持有 HTTP 或终端能力的 Agent。MCP 尚未启用；聊天客户端的 MCP 地址栏不能直接填写此 API 地址。</p>
    </section>

    <section className="settings-card settings-card-wide">
      <div className="settings-card-heading"><h2>API Keys</h2><button className="text-button" type="button" disabled={tokens.isFetching} onClick={() => void tokens.refetch()}>刷新</button></div>
      <QueryFeedback pending={tokens.isPending} error={tokens.error} retry={() => void tokens.refetch()} />
      {tokens.data?.tokens.length === 0 ? <p className="muted-copy">还没有 API Key。在下方为第一个 Agent 生成一枚。</p> : null}
      <div className="management-list">{tokens.data?.tokens.map((token) => <div className="management-item" key={token.id}>
        <div className="management-main"><strong>{token.name}</strong><small>{token.prefix}… · {token.scopes.join(" / ")}</small>
          <small>项目：{token.project_ids.length ? token.project_ids.map((id) => projects.data?.projects.find((item) => item.id === id)?.name ?? `#${id}`).join("、") : "不限"} · 标签：{token.tag_ids.length ? token.tag_ids.map((id) => tags.data?.tags.find((item) => item.id === id)?.name ?? `#${id}`).join("、") : "不限"}</small>
          <small>{token.expires_at ? `${Date.parse(token.expires_at) <= Date.now() ? "已过期" : "到期"} ${formatDateTime(token.expires_at)}` : "不过期"} · {token.last_used_at ? `最近请求 ${formatDateTime(token.last_used_at)}` : "尚未使用"}</small>
        </div>
        <div className="settings-button-row"><button className="text-button" type="button" disabled={!baseURL} aria-label={`接入说明 ${token.name}`} onClick={() => setGuideToken(token)}>接入说明</button><button className="text-button text-button-danger" type="button" disabled={revoke.isPending} aria-label={`撤销 ${token.name}`} onClick={() => setPendingRevoke(token)}>撤销</button></div>
      </div>)}</div>
      <p className="settings-meta">最近请求代表这枚 Key 曾被使用，不代表 Agent 在线。更换 Key：新建 → 在 Agent 中替换并检查 → 撤销旧 Key；正在执行的领取绑定旧 Key，请先结束当前工作。</p>
      {revoke.error ? <p className="form-error" role="alert">{errorMessage(revoke.error)}</p> : null}
    </section>

    <section className="settings-card settings-card-wide">
      <div className="settings-card-heading"><h2>生成 API Key</h2></div>
      <form className="token-create" onSubmit={(event) => { event.preventDefault(); if (resourcesReady && name.trim() && scopes.length && !create.isPending && !revealed) create.mutate(); }}>
        <div className="form-row">
          <div className="field"><label htmlFor="connection-name">名称</label><Input id="connection-name" maxLength={80} required value={name} onChange={(event) => setName(event.target.value)} placeholder="例如：桌面 Agent、iPhone 快捷指令" /></div>
          <div className="field"><label htmlFor="connection-purpose">用途</label><select id="connection-purpose" className="settings-select" value={purpose} onChange={(event) => { setPurpose(event.target.value); setScopes(purposes.find((item) => item.value === event.target.value)?.scopes ?? ["read"]); }}>{purposes.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}</select></div>
        </div>
        <p className="settings-copy">{purposes.find((item) => item.value === purpose)?.description}</p>
        {purpose === "custom" ? <fieldset className="resource-scope"><legend>权限</legend><div className="scope-row">{["read", "capture", "write"].map((scope) => <label className="check-chip" key={scope}><input type="checkbox" checked={scopes.includes(scope)} onChange={(event) => setScopes((current) => event.target.checked ? [...current, scope] : current.filter((item) => item !== scope))} />{scope}</label>)}</div></fieldset> : null}
        <div className="form-row">
          <div className="field"><label htmlFor="connection-expiry">有效期</label><select id="connection-expiry" className="settings-select" value={days} onChange={(event) => setDays(event.target.value)}>{[7, 30, 90, 365, 0].map((value) => <option key={value} value={value}>{value ? `${value} 天` : "不过期"}</option>)}</select></div>
          <div className="field"><label htmlFor="connection-password">当前密码</label><Input id="connection-password" type="password" autoComplete="current-password" value={password} onChange={(event) => setPassword(event.target.value)} placeholder="最近已验证可留空" /></div>
        </div>
        <p className="settings-copy">项目和标签不选表示该维度不限。同一组匹配任意一项；两组均选择时必须同时满足。建议只开放所需项目。</p>
        <QueryFeedback pending={projects.isPending || tags.isPending} error={projects.error ?? tags.error} retry={() => { void projects.refetch(); void tags.refetch(); }} />
        <fieldset className="resource-scope"><legend>项目范围（可选）</legend><div className="scope-row">{projects.data?.projects.map((project) => <label className="check-chip" key={project.id}><input type="checkbox" aria-label={`项目：${project.name}`} disabled={Boolean(project.archived_at)} checked={projectIDs.includes(project.id)} onChange={(event) => setProjectIDs((current) => event.target.checked ? [...current, project.id] : current.filter((id) => id !== project.id))} />{project.name}{project.archived_at ? "（已归档）" : ""}</label>)}</div>{projects.data?.projects.length === 0 ? <p className="settings-meta">尚无项目，可先在「项目与标签」中创建。</p> : null}</fieldset>
        <fieldset className="resource-scope"><legend>标签范围（可选）</legend><div className="scope-row">{tags.data?.tags.map((tag) => <label className="check-chip" key={tag.id}><input type="checkbox" aria-label={`标签：${tag.name}`} checked={tagIDs.includes(tag.id)} onChange={(event) => setTagIDs((current) => event.target.checked ? [...current, tag.id] : current.filter((id) => id !== tag.id))} />#{tag.name}</label>)}</div>{tags.data?.tags.length === 0 ? <p className="settings-meta">尚无标签。</p> : null}</fieldset>
        <p className="settings-meta">受限 Key 只能使用已有分类；capture 文本需带授权的 @项目 / #标签。Key 无权管理账户、Key、Webhook 或系统更新。</p>
        {create.error ? <p className="form-error" role="alert">{errorMessage(create.error)}</p> : null}
        <Button type="submit" disabled={!resourcesReady || !name.trim() || !scopes.length || create.isPending || Boolean(revealed)}>{create.isPending ? "生成中…" : "生成 API Key"}</Button>
      </form>
      {revealed ? <SecretNotice key={revealed.secret} secret={revealed.secret} onDismiss={() => { setRevealed(null); setCheckKey(""); check.reset(); }} /> : null}
    </section>

    <section className="settings-card settings-card-wide">
      <div className="settings-card-heading"><h2>检查连接</h2></div>
      <p className="settings-copy">仅向当前 Tack 发送一次认证请求，不创建或领取任务。远程 Agent 还需在自己的运行环境中执行说明里的检查命令。</p>
      <form className="form-stack" onSubmit={(event) => { event.preventDefault(); if (checkKey.trim() && !check.isPending) check.mutate(); }}>
        <div className="field"><label htmlFor="connection-check">待检查的 API Key</label><Input id="connection-check" type="password" autoComplete="off" spellCheck={false} disabled={check.isPending} value={checkKey} onChange={(event) => { setCheckKey(event.target.value); check.reset(); }} placeholder="粘贴已保存的 Key；检查后清空" /></div>
        <Button variant="quiet" type="submit" disabled={!checkKey.trim() || check.isPending}>{check.isPending ? "检查中…" : "检查连接"}</Button>
      </form>
      {check.data?.token ? <p className="settings-copy" role="status">认证通过 · {check.data.token.name} · {check.data.token.scopes.join(" / ")}。这只确认当前浏览器到 Tack 的连接。</p> : null}
      {check.error ? <p className="form-error" role="alert">{errorMessage(check.error)}</p> : null}
    </section>

    {guideToken !== undefined && baseURL ? <AgentGuide baseURL={baseURL} token={guideToken} resources={{ projects: projects.data?.projects ?? [], tags: tags.data?.tags ?? [] }} onClose={() => setGuideToken(undefined)} /> : null}
    <SudoDialog open={pendingRevoke !== null} title="撤销 API Key" description={`撤销 ${pendingRevoke?.name ?? ""} 后，对应 Agent 将立即失去访问权限。`} onClose={() => setPendingRevoke(null)} onSuccess={() => { if (pendingRevoke) revoke.mutate(pendingRevoke.id); }} />
  </div>;
}

function AgentGuide({ baseURL, token, resources, onClose }: { baseURL: string; token: APIToken | null; resources: ConnectionResources; onClose: () => void }) {
  const [copied, setCopied] = useState(false);
  const [error, setError] = useState("");
  const text = buildAgentGuide(baseURL, token, resources);
  const copy = async () => {
    setError("");
    setCopied(false);
    try {
      if (!navigator.clipboard?.writeText) throw new Error("当前环境无法复制，请手动选择文本");
      await navigator.clipboard.writeText(text);
      setCopied(true);
    } catch (error) { setError(`复制失败：${errorMessage(error)}`); }
  };
  return <Dialog open title="Agent 接入说明" subtitle={token ? token.name : "通用说明 · 执行前先确认 Key 权限"} className="connection-guide-dialog" onClose={onClose} footer={<div className="dialog-actions"><Button variant="quiet" type="button" onClick={onClose}>关闭</Button><Button type="button" onClick={() => void copy()}>复制 Agent 说明</Button></div>}>
    <p className="settings-copy">可以把说明交给其他 Agent。Key 请通过它的环境变量或秘密管理功能单独配置。</p>
    <pre className="connection-guide" tabIndex={0}>{text}</pre>
    {copied ? <p className="settings-copy" role="status">已复制，说明不含 API Key。</p> : null}
    {error ? <p className="form-error" role="alert">{error}</p> : null}
  </Dialog>;
}
