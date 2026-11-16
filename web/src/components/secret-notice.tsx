import { useState } from "react";
import { errorMessage } from "../lib/api";

export function SecretNotice({ secret, onDismiss }: { secret: string; onDismiss: () => void }) {
  const [message, setMessage] = useState("");
  const [error, setError] = useState("");
  const copy = async () => {
    setError("");
    setMessage("");
    try {
      if (!navigator.clipboard?.writeText) throw new Error("当前环境无法复制，请手动保存");
      await navigator.clipboard.writeText(secret);
      setMessage("已复制");
    } catch (error) { setError(`复制失败：${errorMessage(error)}`); }
  };
  return <div className="secret-notice"><div className="plain-token" role="status"><span>只显示这一次，请保存：</span><code>{secret}</code><button type="button" onClick={() => void copy()}>复制</button><button type="button" onClick={onDismiss}>已保存，隐藏</button></div>{message ? <p className="settings-copy" role="status">{message}</p> : null}{error ? <p className="form-error" role="alert">{error}</p> : null}</div>;
}
