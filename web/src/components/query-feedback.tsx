import { errorMessage } from "../lib/api";

export function QueryFeedback({ pending, error, retry }: { pending?: boolean; error?: unknown; retry: () => void }) {
  if (error) return <div className="query-feedback" role="alert"><p>{errorMessage(error)}</p><button className="text-button" type="button" onClick={retry} disabled={pending}>重试</button></div>;
  if (pending) return <p className="muted-copy" role="status">正在读取…</p>;
  return null;
}
