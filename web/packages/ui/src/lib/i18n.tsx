import { createContext, useContext } from "react";
import type { Status } from "./utils";

/**
 * Texts used inside shared components. English by default (the admin
 * never sets anything); the status page provides a translated set.
 */
export type UiText = {
  status: Record<Status, string>;
  overall: Record<"operational" | "degraded" | "partial_outage" | "major_outage" | "maintenance" | "unknown", string>;
  live: string;
  reconnecting: string;
  uptimeHistory: (days: number) => string;
  noData: string;
  uptime: string;
  failedChecks: string;
  slowChecks: string;
  noIncidents: string;
  more: (n: number) => string;
  daysAgo: (days: number) => string;
  today: string;
  noChecks: string;
  average: string;
  max: string;
  failed: string;
  /** BCP 47 locale for dates and numbers. */
  locale: string;
};

export const englishUi: UiText = {
  status: { up: "Operational", degraded: "Degraded", down: "Down", pending: "Pending", paused: "Paused", maintenance: "Maintenance" },
  overall: {
    operational: "All systems operational",
    degraded: "Degraded performance",
    partial_outage: "Partial outage",
    major_outage: "Major outage",
    maintenance: "Scheduled maintenance in progress",
    unknown: "Waiting for first checks",
  },
  live: "Live",
  reconnecting: "Reconnecting",
  uptimeHistory: (n) => `Uptime history for the last ${n} days`,
  noData: "No data recorded",
  uptime: "Uptime",
  failedChecks: "Failed checks",
  slowChecks: "Slow checks",
  noIncidents: "No incidents",
  more: (n) => `+${n} more`,
  daysAgo: (n) => `${n} days ago`,
  today: "Today",
  noChecks: "No checks in this period yet",
  average: "Average",
  max: "Max",
  failed: "Failed",
  locale: "en",
};

const Ctx = createContext<UiText>(englishUi);

export const UiTextProvider = Ctx.Provider;
export const useUiText = () => useContext(Ctx);
