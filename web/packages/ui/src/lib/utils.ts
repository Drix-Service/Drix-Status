import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

export type Status = "up" | "degraded" | "down" | "pending" | "paused" | "maintenance";

export const statusLabel: Record<Status, string> = {
  up: "Operational",
  degraded: "Degraded",
  down: "Down",
  pending: "Pending",
  paused: "Paused",
  maintenance: "Maintenance",
};

export function formatLatency(ms: number | null | undefined): string {
  if (ms == null || ms < 0) return "—";
  if (ms < 1) return `${ms.toFixed(2)} ms`;
  if (ms < 1000) return `${Math.round(ms)} ms`;
  return `${(ms / 1000).toFixed(ms < 10000 ? 2 : 1)} s`;
}

export function formatUptime(pct: number | null | undefined): string {
  if (pct == null || pct < 0) return "—";
  if (pct >= 100) return "100%";
  return `${pct.toFixed(2)}%`;
}

const rtf = new Intl.RelativeTimeFormat("en", { numeric: "auto", style: "short" });

export function timeAgo(input: string | number | Date | null | undefined, now = Date.now()): string {
  if (!input) return "never";
  const t = new Date(input).getTime();
  const diff = Math.round((t - now) / 1000);
  const abs = Math.abs(diff);
  if (abs < 5) return "just now";
  if (abs < 60) return rtf.format(diff, "second");
  if (abs < 3600) return rtf.format(Math.round(diff / 60), "minute");
  if (abs < 86400) return rtf.format(Math.round(diff / 3600), "hour");
  return rtf.format(Math.round(diff / 86400), "day");
}

export function formatDuration(fromIso: string | null | undefined, to = Date.now()): string {
  if (!fromIso) return "—";
  let s = Math.max(0, Math.floor((to - new Date(fromIso).getTime()) / 1000));
  const d = Math.floor(s / 86400);
  s -= d * 86400;
  const h = Math.floor(s / 3600);
  s -= h * 3600;
  const m = Math.floor(s / 60);
  if (d > 0) return `${d}d ${h}h`;
  if (h > 0) return `${h}h ${m}m`;
  if (m > 0) return `${m}m`;
  return `${s}s`;
}

const dateFmt = new Intl.DateTimeFormat("en", { month: "short", day: "numeric", year: "numeric" });
const dateTimeFmt = new Intl.DateTimeFormat("en", {
  month: "short",
  day: "numeric",
  hour: "2-digit",
  minute: "2-digit",
});

export const formatDate = (d: string | Date) => dateFmt.format(new Date(d));
export const formatDateTime = (d: string | Date) => dateTimeFmt.format(new Date(d));
