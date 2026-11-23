import type { DayBucket } from "@drix/ui/drix/uptime-bar";
import type { Overall } from "@drix/ui/drix/status";
import type { LatencyPoint } from "@drix/ui/drix/charts";
import type { Status } from "@drix/ui/lib/utils";

export type PublicMonitor = {
  id: number;
  name: string;
  description?: string;
  type: "http" | "tcp" | "ping" | "dns";
  status: Status;
  latency_ms: number;
  since?: string;
  last_checked?: string;
  uptime_30: number;
  uptime_90: number;
  days: DayBucket[];
  locations?: boolean;
};

export type PublicLocation = { location: string; status: Status; latency_ms: number; stale?: boolean; avg_24h_ms: number; uptime_24h: number };

export type PublicSection = {
  id: number;
  name: string;
  description?: string;
  collapsed: boolean;
  status: Status;
  monitors: PublicMonitor[];
};

export type PublicEvent = {
  id: number;
  monitor_id: number;
  monitor_name: string;
  from_status: Status;
  to_status: Status;
  created_at: string;
};

export type PageInfo = {
  id: number;
  slug: string;
  name: string;
  description: string;
  logo_url: string;
  website_url: string;
  accent_color: string;
  show_latency: boolean;
  footer_text: string;
  is_default: boolean;
  language?: string;
};

export type Impact = "none" | "minor" | "major" | "critical";
export type IncidentStatus =
  | "investigating"
  | "identified"
  | "monitoring"
  | "resolved"
  | "scheduled"
  | "in_progress"
  | "completed"
  | "cancelled";

export type PublicIncident = {
  id: number;
  kind: "incident" | "maintenance";
  title: string;
  status: IncidentStatus;
  impact: Impact;
  scheduled_start?: string;
  scheduled_end?: string;
  created_at: string;
  resolved_at?: string;
  updates: { id: number; status: IncidentStatus; body: string; created_at: string }[];
  affected: { id: number; name: string; impact: string }[];
};

export type Snapshot = {
  generated_at: string;
  page: PageInfo;
  overall: Overall;
  summary: { total: number; up: number; degraded: number; down: number; maintenance: number; pending: number; paused: number };
  sections: PublicSection[];
  events: PublicEvent[];
  incidents: PublicIncident[];
  maintenance: PublicIncident[];
  history: PublicIncident[];
};

export class HttpError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

async function get<T>(url: string): Promise<T> {
  const res = await fetch(url, { headers: { Accept: "application/json" } });
  if (!res.ok) throw new HttpError(res.status, `${res.status} ${res.statusText}`);
  return res.json() as Promise<T>;
}

/** API base for the status page. (The optional argument is kept for call sites; there is one page.) */
export const pageApi = (_slug?: string) => "/api/public";

export const fetchStatus = (slug?: string) => get<Snapshot>(`${pageApi(slug)}/status`);

export type Range = "24h" | "7d" | "30d";
export const fetchLocations = (id: number) => get<{ locations: PublicLocation[] }>(`/api/public/monitors/${id}/locations`);
export const fetchLatency = (id: number, range: Range) =>
  get<{ range: Range; bucket_s: number; points: LatencyPoint[] }>(`/api/public/monitors/${id}/latency?range=${range}`);

export const fetchIncident = (slug: string | undefined, id: number) => get<PublicIncident>(`${pageApi(slug)}/incidents/${id}`);
export const fetchHistory = (slug: string | undefined, before?: number) =>
  get<{ incidents: PublicIncident[]; has_more: boolean }>(`${pageApi(slug)}/history${before ? `?before=${before}` : ""}`);

/** Path prefix for links within the status site. */
export const basePath = (_slug?: string) => "";
