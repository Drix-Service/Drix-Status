import { useEffect, useRef, useState } from "react";

/**
 * Subscribes to a Server-Sent Events endpoint. EventSource reconnects on its
 * own; we only track connection state and hand parsed messages to `onMessage`.
 */
export function useEventStream<T>(url: string, onMessage: (msg: T) => void, enabled = true) {
  const [connected, setConnected] = useState(false);
  const handler = useRef(onMessage);
  handler.current = onMessage;

  useEffect(() => {
    if (!enabled) return;
    const es = new EventSource(url, { withCredentials: true });
    es.addEventListener("ready", () => setConnected(true));
    es.onopen = () => setConnected(true);
    es.onerror = () => setConnected(false);
    es.onmessage = (e) => {
      try {
        handler.current(JSON.parse(e.data) as T);
      } catch {
        /* ignore malformed */
      }
    };
    return () => es.close();
  }, [url, enabled]);

  return connected;
}

export type LiveUpdate = {
  id: number;
  status: "up" | "degraded" | "down" | "pending" | "paused";
  prev?: string;
  latency_ms: number;
  at: number;
  message?: string;
  changed?: boolean;
};

export type Broadcast = { type: "status"; items: LiveUpdate[] } | { type: "agents" };
