import type { Impact, IncidentKind, IncidentStatus } from "../api";

export const incidentStatuses: IncidentStatus[] = ["investigating", "identified", "monitoring", "resolved"];
export const maintenanceStatuses: IncidentStatus[] = ["scheduled", "in_progress", "completed", "cancelled"];
export const statusesFor = (k: IncidentKind) => (k === "maintenance" ? maintenanceStatuses : incidentStatuses);

export const statusLabel: Record<IncidentStatus, string> = {
  investigating: "Investigating",
  identified: "Identified",
  monitoring: "Monitoring",
  resolved: "Resolved",
  scheduled: "Scheduled",
  in_progress: "In progress",
  completed: "Completed",
  cancelled: "Cancelled",
};

export const statusHint: Partial<Record<IncidentStatus, string>> = {
  investigating: "Something is wrong and we're looking into it",
  identified: "We know the cause and are working on a fix",
  monitoring: "A fix is in place and we're watching",
  resolved: "Everything is back to normal",
};

export const impacts: { value: Impact; label: string; hint: string; dot: string }[] = [
  { value: "minor", label: "Minor", hint: "Some users or features affected", dot: "bg-degraded" },
  { value: "major", label: "Major", hint: "A large part of the service is down", dot: "bg-down" },
  { value: "critical", label: "Critical", hint: "Everything is down", dot: "bg-down" },
  { value: "none", label: "None", hint: "Informational only", dot: "bg-pending" },
];

export const impactDot = (kind: IncidentKind, impact: Impact) =>
  kind === "maintenance" ? "bg-maintenance" : (impacts.find((i) => i.value === impact)?.dot ?? "bg-pending");

/** "2026-11-22T14:30" for <input type="datetime-local"> in the viewer's time zone. */
export function toLocalInput(iso: string | null | undefined): string {
  if (!iso) return "";
  const d = new Date(iso);
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

export const fromLocalInput = (v: string) => (v ? new Date(v).toISOString() : null);

const windowFmt = new Intl.DateTimeFormat(undefined, { weekday: "short", month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" });
const timeFmt = new Intl.DateTimeFormat(undefined, { hour: "2-digit", minute: "2-digit" });
export function windowLabel(start: string | null, end: string | null) {
  if (!start || !end) return "";
  const s = new Date(start);
  const e = new Date(end);
  return s.toDateString() === e.toDateString() ? `${windowFmt.format(s)} – ${timeFmt.format(e)}` : `${windowFmt.format(s)} – ${windowFmt.format(e)}`;
}
