import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Copy, Download, RefreshCw } from "lucide-react";
import { api, errorMessage } from "../lib/api";
import { formatDateTime } from "../lib/format";
import { QueryFeedback } from "./query-feedback";

interface LogEntry {
  time: string;
  level: string;
  event: string;
  request_id?: string;
  method?: string;
  route?: string;
  status?: number;
  duration_ms?: number;
  error_code?: string;
  error?: string;
  count?: number;
}

export function LogSettings() {
  const [level, setLevel] = useState("");
  const [requestID, setRequestID] = useState("");
  const [filter, setFilter] = useState({ level: "", requestID: "" });
  const [search, setSearch] = useState("");
  const [copied, setCopied] = useState(false);
  const [copyError, setCopyError] = useState("");
  const logs = useQuery({
    queryKey: ["logs", filter],
    queryFn: ({ signal }) => {
      const query = new URLSearchParams({ limit: "500" });
      if (filter.level) query.set("level", filter.level);
      if (filter.requestID) query.set("request_id", filter.requestID);
      return api<{ entries: LogEntry[] }>(`/logs?${query}`, { signal, cache: "no-store" });
    },
    gcTime: 0,
  });
  const entries = (logs.data?.entries ?? []).filter((entry) => !search.trim() || JSON.stringify(entry).toLowerCase().includes(search.trim().toLowerCase()));
  const jsonl = entries.map((entry) => JSON.stringify(entry)).join("\n") + (entries.length ? "\n" : "");

  const copy = async () => {
    setCopied(false);
    setCopyError("");
    try {
      if (!navigator.clipboard?.writeText) throw new Error("请下载文件或手动选择日志");
      await navigator.clipboard.writeText(jsonl);
      setCopied(true);
    } catch (error) {
      setCopyError(`复制失败：${errorMessage(error)}`);
    }
  };

  const download = () => {
    const url = URL.createObjectURL(new Blob([jsonl], { type: "application/x-ndjson;charset=utf-8" }));
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = `tack-logs-${new Date().toISOString().slice(0, 10)}.jsonl`;
    anchor.click();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  };

  return <section className="log-settings" aria-labelledby="logs-title">
    <div className="settings-card-heading"><h2 id="logs-title">诊断日志</h2><button className="text-button" type="button" disabled={logs.isFetching} onClick={() => { setCopied(false); void logs.refetch(); }}><RefreshCw size={14} aria-hidden />刷新</button></div>
    <p className="settings-copy">记录服务请求、错误和维护事件，按时间倒序。复制 JSONL 可直接交给 AI 排查；请求 ID 可关联页面报错。最多保留 3 MiB，当前可查最近 500 条。</p>
    <form className="log-filters" onSubmit={(event) => { event.preventDefault(); setCopied(false); setFilter({ level, requestID: requestID.trim() }); }}>
      <label>日志级别<select value={level} onChange={(event) => setLevel(event.target.value)}><option value="">全部级别</option><option value="ERROR">错误</option><option value="WARN">警告</option><option value="INFO">信息</option></select></label>
      <label>请求 ID<input value={requestID} onChange={(event) => setRequestID(event.target.value)} placeholder="req_…" spellCheck={false} autoCapitalize="none" /></label>
      <button className="quiet-button" type="submit">应用筛选</button>
    </form>
    <div className="log-toolbar">
      <input type="search" aria-label="搜索日志内容" placeholder="搜索路由、错误码或事件…" value={search} onChange={(event) => { setSearch(event.target.value); setCopied(false); }} />
      <button className="quiet-button" type="button" disabled={!entries.length || logs.isFetching} onClick={() => void copy()}><Copy size={14} aria-hidden />复制 JSONL</button>
      <button className="quiet-button" type="button" disabled={!entries.length || logs.isFetching} onClick={download}><Download size={14} aria-hidden />下载</button>
    </div>
    <QueryFeedback pending={logs.isPending} error={logs.error} retry={() => void logs.refetch()} />
    {copied ? <p className="settings-copy" role="status">日志已复制</p> : null}
    {copyError ? <p className="form-error" role="alert">{copyError}</p> : null}
    {!logs.isPending && !logs.error && !entries.length ? <p className="log-empty">没有匹配的日志。</p> : null}
    <div className="log-list" aria-label="日志条目">
      {entries.map((entry, index) => <details className={`log-entry log-${entry.level.toLowerCase()}`} key={`${entry.time}-${entry.request_id ?? entry.event}-${index}`}>
        <summary><time dateTime={entry.time}>{formatDateTime(entry.time)}</time><span className="log-level">{entry.level}</span><strong>{entry.route || entry.event}</strong>{entry.status ? <span className="log-status">{entry.status}</span> : null}{entry.error_code ? <span className="log-error-code">{entry.error_code}</span> : null}</summary>
        <pre>{JSON.stringify(entry, null, 2)}</pre>
      </details>)}
    </div>
    <p className="settings-meta">不记录任务正文、查询参数、密码、Cookie 或 API Key。日志为 Tack 服务端事件；浏览器控制台、updater 与代理日志仍需分别查看。重启后从轮转文件恢复最近记录。</p>
  </section>;
}
