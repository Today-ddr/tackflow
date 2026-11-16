import { useEffect, useRef, useState } from "react";
import { passkeyError } from "./passkeys";

export function usePasskey<Result>(action: (signal: AbortSignal) => Promise<Result>, onSuccess: (result: Result) => void) {
  const controller = useRef<AbortController | null>(null);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");
  useEffect(() => () => { controller.current?.abort(); controller.current = null; }, []);

  const run = async () => {
    if (controller.current) return;
    const active = new AbortController();
    controller.current = active;
    setPending(true);
    setError("");
    try {
      const result = await action(active.signal);
      if (!active.signal.aborted && controller.current === active) onSuccess(result);
    } catch (error) {
      if (controller.current === active) setError(passkeyError(error));
    } finally {
      if (controller.current === active) { controller.current = null; setPending(false); }
    }
  };
  return { pending, error, run, cancel: () => controller.current?.abort() };
}
