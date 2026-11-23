import type { Status } from "@drix/ui/lib/utils";
import type { LatencyPoint } from "@drix/ui/drix/charts";

export class ApiError extends Error {
  status: number;
  fields?: Record<string, string>;
  code?: string;
  constructor(status: number, message: string, fields?: Record<string, string>, code?: string) {
    super(message);
    this.code = code;
    this.status = status;
    this.fields = fields;
  }
}

async function request<T>(method: string, url: string, body?: unknown): Promise<T> {
  const res = await fetch(`/api/admin${url}`, {
    method,
    credentials: "same-origin",
    headers: body !== undefined ? { "Content-Type": "application/json" } : undefined,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  if (res.status === 204) return undefined as T;
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    if (res.status === 401 && !url.startsWith("/auth/login") && !url.startsWith("/auth/passkey")) {
      window.dispatchEvent(new CustomEvent("drix:unauthorized"));
    }
    if (data.code === "two_factor_setup_required") {
      window.dispatchEvent(new CustomEvent("drix:2fa-required"));
    }
    throw new ApiError(res.status, data.error ?? res.statusText, data.fields, data.code);
  }
  return data as T;
}

export const api = {
  get: <T>(url: string) => request<T>("GET", url),
  post: <T>(url: string, body?: unknown) => request<T>("POST", url, body ?? {}),
  put: <T>(url: string, body?: unknown) => request<T>("PUT", url, body ?? {}),
  del: <T = void>(url: string) => request<T>("DELETE", url),
};

/* ---------------- Types ---------------- */

export type MonitorType = "http" | "tcp" | "ping" | "dns" | "ssl" | "json" | "smtp" | "grpc" | "push";

export type Role = "owner" | "admin" | "editor" | "viewer";
export type User = {
  id: number;
  email: string;
  name: string;
  role: Role;
  created_at: string;
  last_login_at: string | null;
  two_factor?: boolean;
};
export type Me = { user: User; public_url: string; status_url: string; version: string; two_factor_setup_required?: boolean };

export type Passkey = { id: number; name: string; created_at: string; last_used_at: string | null };
export type TwoFactorState = { totp: boolean; passkeys: Passkey[]; recovery_left: number; required: boolean; can_totp: boolean };
export type LoginResult = User | { two_factor: true; ticket: string; methods: ("totp" | "passkey" | "recovery")[] };

export type ApiKey = {
  id: number;
  name: string;
  prefix: string;
  role: Exclude<Role, "owner">;
  created_by: number;
  created_by_email: string;
  created_at: string;
  last_used_at: string | null;
  token?: string; // only right after creating
};

export type Invite = {
  id: number;
  email: string;
  role: Role;
  expires_at: string;
  created_at: string;
};
export type TeamResponse = { users: User[]; invites: Invite[] };
export type AuditEntry = {
  id: number;
  user_id: number | null;
  user_email: string;
  action: string;
  target_type: string;
  target_id: string;
  summary: string;
  ip: string;
  created_at: string;
};

export type Page = {
  id: number;
  slug: string;
  name: string;
  description: string;
  logo_url: string;
  website_url: string;
  accent_color: string;
  footer_text: string;
  show_latency: boolean;
  language: string;
  custom_domain: string | null;
  is_default: boolean;
  position: number;
  created_at: string;
  url: string;
  sections: number;
  monitors: number;
  section_list: { id: number; name: string }[];
  subscribers: number;
};

export type PageMonitor = { monitor_id: number; display_name: string | null };
export type Section = {
  id: number;
  page_id: number;
  name: string;
  description: string;
  position: number;
  collapsed: boolean;
  monitors: PageMonitor[];
};
export type GroupRef = { id: number; name: string };

export type HTTPConfig = {
  method?: string;
  expect_status_min?: number;
  expect_status_max?: number;
  keyword?: string;
  keyword_absent?: string;
  headers?: Record<string, string>;
  body?: string;
  follow_redirects?: boolean;
  ignore_tls?: boolean;
};
export type TCPConfig = { port?: number };
export type PingConfig = { count?: number };
export type DNSConfig = { record_type?: string; resolver?: string; expect?: string };
export type SSLConfig = { warn_days?: number; crit_days?: number; server_name?: string };
export type JSONAssertion = { path: string; op: "equals" | "not_equals" | "contains" | "gt" | "lt" | "exists" | "not_exists"; value?: string };
export type JSONConfig = { assertions?: JSONAssertion[] };
export type SMTPConfig = { starttls?: boolean; ignore_tls?: boolean };
export type GRPCConfig = { service?: string; tls?: boolean; ignore_tls?: boolean };
export type PushConfig = { grace_s?: number };
export type MonitorConfig = HTTPConfig & TCPConfig & PingConfig & DNSConfig & SSLConfig & JSONConfig & SMTPConfig & GRPCConfig & PushConfig;

export type LiveStatus = {
  status: Status;
  latency_ms: number;
  message?: string;
  code?: number;
  last_checked?: string;
  since?: string;
  cert_expiry?: string;
  agent?: string;
};

export type MonitorAck = { by?: string; via?: string; at: string };
export const ackWho = (a: MonitorAck) => a.by || (a.via ? `via ${a.via}` : "someone");

export type Monitor = {
  id: number;
  name: string;
  description: string;
  type: MonitorType;
  target: string;
  config: MonitorConfig;
  interval_s: number;
  timeout_ms: number;
  retries: number;
  degraded_ms: number;
  enabled: boolean;
  multi_location: boolean;
  show_locations: boolean;
  /** May post automatic incidents on the status page. */
  auto_incident: boolean;
  position: number;
  rev: number;
  created_at: string;
  updated_at: string;
  live: LiveStatus;
  spark: number[] | null;
  uptime_30: number;
  /** Who acknowledged the current outage (down monitors only). */
  ack?: MonitorAck;
  owner?: string;
  groups: GroupRef[];
  push_token?: string;
  channel_ids: number[];
};

export type MonitorInput = Pick<
  Monitor,
  "name" | "description" | "type" | "target" | "config" | "interval_s" | "timeout_ms" | "retries" | "degraded_ms" | "enabled" | "multi_location" | "show_locations" | "auto_incident"
> & { section_id?: number | null; channel_ids?: number[] };

export type CheckResult = {
  ok: boolean;
  lat: number;
  msg?: string;
  code?: number;
  cert?: string;
  at: string;
};

export type ResultRow = {
  monitor_id: number;
  checked_at: string;
  status: Status;
  latency_ms: number;
  message: string;
  agent_id: string | null;
  agent_name: string;
};

export type Event = {
  id: number;
  monitor_id: number;
  monitor_name: string;
  from_status: Status;
  to_status: Status;
  message: string;
  created_at: string;
};

export type LiveAgent = {
  id: string;
  server: string;
  hostname: string;
  version: string;
  os: string;
  arch: string;
  remote_addr: string;
  connected_at: string;
  last_seen: string;
  running: number;
  in_flight: number;
  buffered: number;
};

export type Agent = {
  id: string;
  name: string;
  region: string;
  token_prefix: string;
  disabled: boolean;
  created_at: string;
  last_seen_at: string | null;
  last_hostname: string;
  last_version: string;
  online: boolean;
  live?: LiveAgent;
  owned: number;
};

export type AgentsResponse = { agents: Agent[]; total_monitors: number; public_url: string };

export type Overview = {
  monitors: { total: number; enabled: number; paused: number; up: number; degraded: number; down: number; pending: number };
  groups: number;
  agents_online: number;
  agents_total: number;
  avg_latency_ms: number;
  checks_per_min: number;
  events: Event[];
  viewers: number;
  incidents: Incident[];
};

export type Settings = {
  retention_days: number;
  default_interval_s: number;
  default_timeout_ms: number;
};

export type LatencyResponse = { range: string; bucket_s: number; points: LatencyPoint[] };

export type Impact = "none" | "minor" | "major" | "critical";
export type IncidentKind = "incident" | "maintenance";
export type IncidentStatus =
  | "investigating"
  | "identified"
  | "monitoring"
  | "resolved"
  | "scheduled"
  | "in_progress"
  | "completed"
  | "cancelled";

export type IncidentUpdate = { id: number; status: IncidentStatus; body: string; created_by: number | null; author: string; created_at: string };

export type Incident = {
  id: number;
  kind: IncidentKind;
  title: string;
  status: IncidentStatus;
  impact: Impact;
  scheduled_start: string | null;
  scheduled_end: string | null;
  auto_transition: boolean;
  notify_subscribers: boolean;
  /** Posted by Drix because a public monitor was down. */
  auto?: boolean;
  created_by: number | null;
  created_at: string;
  updated_at: string;
  resolved_at: string | null;
  updates: IncidentUpdate[];
  monitors: { monitor_id: number; impact: "degraded" | "down" | "maintenance" }[];
  page_ids: number[];
};

export type ChannelType = "email" | "discord" | "slack" | "webhook" | "telegram" | "teams" | "pagerduty" | "opsgenie" | "ntfy" | "pushover";
export type AlertEvent = "down" | "recovered" | "degraded" | "incident" | "cert_expiring";
export type Channel = {
  id: number;
  name: string;
  type: ChannelType;
  config: { to?: string[]; url?: string; secret?: string; error?: string } & Record<string, unknown>;
  events: AlertEvent[];
  apply_to_all: boolean;
  remind_every_min: number;
  escalate_after_min: number;
  enabled: boolean;
  created_at: string;
  monitor_ids: number[];
};
export type Delivery = {
  id: number;
  channel_id: number | null;
  channel_name: string;
  monitor_id: number | null;
  event: string;
  title: string;
  status: "sent" | "failed";
  error: string;
  attempts: number;
  created_at: string;
};
export type EmailSettings = {
  host: string;
  port: number;
  username: string;
  from: string;
  tls: "starttls" | "tls" | "none";
  has_password: boolean;
  source: "saved" | "env" | "none";
  can_store_secrets: boolean;
};

export type MonitorLocation = {
  location: string;
  status: Status;
  latency_ms: number;
  message?: string;
  last_checked?: string;
  stale?: boolean;
  avg_24h_ms: number;
  uptime_24h: number;
};
export type LocationsResponse = { multi_location: boolean; available: string[]; locations: MonitorLocation[] };

export type BackupFile = { name: string; size: number; created_at: string; uploaded: boolean };
export type BackupInfo = {
  enabled: boolean;
  time: string;
  keep: number;
  s3: { enabled: boolean; endpoint: string; region: string; bucket: string; prefix: string; access_key: string; has_secret: boolean };
  unavailable?: string;
  can_store_secrets: boolean;
  status: {
    last_at: string | null;
    last_file: string;
    last_size: number;
    last_error: string;
    last_error_at: string | null;
    uploaded: boolean;
    upload_error: string;
  };
  files: BackupFile[];
};
