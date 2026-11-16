import { useEffect } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { cancelTaskDataRequests, invalidateTaskData } from "./task-cache";

export function useTaskEvents(enabled: boolean) {
  const client = useQueryClient();
  useEffect(() => {
    if (!enabled || typeof EventSource === "undefined") return;
    const source = new EventSource("/api/v1/events");
    let active = true;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const synchronize = async () => {
      await cancelTaskDataRequests(client);
      if (active) await invalidateTaskData(client);
    };
    const scheduleSync = () => {
      clearTimeout(timer);
      timer = setTimeout(() => { void synchronize(); }, 200);
    };
    const onChange = (event: MessageEvent<string>) => {
      try {
        const data: unknown = JSON.parse(event.data);
        if (!data || typeof data !== "object" || !("resource" in data) ||
            !["task", "project", "tag"].includes(String(data.resource))) return;
      } catch {
        return;
      }
      scheduleSync();
    };
    source.addEventListener("ready", scheduleSync);
    source.addEventListener("change", onChange);
    return () => {
      active = false;
      clearTimeout(timer);
      source.removeEventListener("ready", scheduleSync);
      source.removeEventListener("change", onChange);
      source.close();
    };
  }, [client, enabled]);
}
