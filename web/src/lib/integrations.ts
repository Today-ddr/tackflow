export interface Webhook {
  id: number;
  name: string;
  url: string;
  events: string[];
  enabled: boolean;
  created_at: string;
  last_status?: string;
  last_error?: string;
  pending_count?: number;
}

export interface WebhookDelivery {
  id: number;
  event_id: number;
  status: string;
  attempts: number;
  next_attempt_at: string | null;
  last_error: string;
  delivered_at: string | null;
}

export function safeExternalURL(value: string | null): string | null {
  if (!value || Array.from(value).some((character) => character.charCodeAt(0) < 32 || character.charCodeAt(0) === 127)) return null;
  try {
    const url = new URL(value);
    return ["http:", "https:", "codeg:"].includes(url.protocol) && url.hostname && !url.username && !url.password ? url.href : null;
  } catch {
    return null;
  }
}
