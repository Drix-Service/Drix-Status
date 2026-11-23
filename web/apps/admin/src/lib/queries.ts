import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useRef } from "react";
import { useEventStream, type Broadcast } from "@drix/ui/lib/sse";
import { api, type AgentsResponse, type Me, type Page, type Section, type Monitor, type Overview, type Settings } from "../api";

export const keys = {
  me: ["me"] as const,
  monitors: ["monitors"] as const,
  monitor: (id: number) => ["monitor", id] as const,
  groups: ["groups"] as const,
  branding: ["branding"] as const,
  agents: ["agents"] as const,
  overview: ["overview"] as const,
  settings: ["settings"] as const,
  events: (monitorId?: number) => ["events", monitorId ?? 0] as const,
};

export const useMe = () => useQuery({ queryKey: keys.me, queryFn: () => api.get<Me>("/auth/me"), retry: false, staleTime: 60_000 });
export const useMonitors = () => useQuery({ queryKey: keys.monitors, queryFn: () => api.get<Monitor[]>("/monitors"), refetchInterval: 30_000 });
export const useGroups = () => useQuery({ queryKey: keys.groups, queryFn: () => api.get<{ groups: Section[] }>("/groups") });
export const useBranding = () => useQuery({ queryKey: keys.branding, queryFn: () => api.get<{ page: Page; url: string }>("/branding") });
export const useAgents = () => useQuery({ queryKey: keys.agents, queryFn: () => api.get<AgentsResponse>("/agents"), refetchInterval: 10_000 });
export const useOverview = () => useQuery({ queryKey: keys.overview, queryFn: () => api.get<Overview>("/overview"), refetchInterval: 30_000 });
export const useSettings = () => useQuery({ queryKey: keys.settings, queryFn: () => api.get<Settings>("/settings") });

/**
 * One SSE connection for the whole admin app. Status updates are patched
 * directly into the monitors cache (no refetch), and dependent views are
 * refreshed at most every couple of seconds.
 */
export function useLiveAdmin(enabled: boolean) {
  const qc = useQueryClient();
  const pending = useRef<number | null>(null);

  const schedule = (fn: () => void) => {
    if (pending.current) return;
    pending.current = window.setTimeout(() => {
      pending.current = null;
      fn();
    }, 2000);
  };

  return useEventStream<Broadcast>(
    "/api/admin/stream",
    (msg) => {
      if (msg.type === "agents") {
        qc.invalidateQueries({ queryKey: keys.agents });
        qc.invalidateQueries({ queryKey: keys.overview });
        return;
      }
      const byId = new Map(msg.items.map((i) => [i.id, i]));
      let changed = false;
      qc.setQueryData<Monitor[]>(keys.monitors, (prev) =>
        prev?.map((m) => {
          const u = byId.get(m.id);
          if (!u || !m.enabled) return m;
          if (u.changed) changed = true;
          const spark = [...(m.spark ?? []), u.status === "down" || u.latency_ms <= 0 ? -1 : u.latency_ms].slice(-30);
          return {
            ...m,
            spark,
            live: {
              ...m.live,
              status: u.status,
              latency_ms: u.latency_ms,
              message: u.message,
              last_checked: new Date(u.at).toISOString(),
              since: u.changed ? new Date(u.at).toISOString() : m.live.since,
            },
          };
        }),
      );
      for (const id of byId.keys()) {
        if (qc.getQueryData(keys.monitor(id))) schedule(() => qc.invalidateQueries({ queryKey: ["monitor"] }));
      }
      if (changed) {
        schedule(() => {
          qc.invalidateQueries({ queryKey: keys.overview });
          qc.invalidateQueries({ queryKey: ["events"] });
        });
      }
    },
    enabled,
  );
}
