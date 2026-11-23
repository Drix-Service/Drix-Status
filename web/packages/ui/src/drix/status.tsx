import * as React from "react";
import {
  BracesIcon,
  CheckIcon,
  GlobeIcon,
  HeartPulseIcon,
  MailIcon,
  MinusIcon,
  NetworkIcon,
  PauseIcon,
  RadioTowerIcon,
  ServerCogIcon,
  ShieldCheckIcon,
  TriangleAlertIcon,
  WaypointsIcon,
  WrenchIcon,
  XIcon,
} from "lucide-react";
import { cn, type Status } from "../lib/utils";
import { useUiText } from "../lib/i18n";

const dotColor: Record<Status, string> = {
  up: "bg-up",
  degraded: "bg-degraded",
  down: "bg-down",
  pending: "bg-pending",
  paused: "bg-pending",
  maintenance: "bg-maintenance",
};

/**
 * Status dot. Down monitors get a soft expanding ring so they stand out in
 * long lists without screaming; `live` adds the ring for healthy ones too.
 */
export function StatusDot({
  status,
  live,
  size = "md",
  className,
}: {
  status: Status;
  live?: boolean;
  size?: "sm" | "md" | "lg";
  className?: string;
}) {
  const ui = useUiText();
  const s = size === "sm" ? "size-1.5" : size === "lg" ? "size-2.5" : "size-2";
  const ring = status === "down" || (live && status !== "paused" && status !== "pending");
  return (
    <span className={cn("relative inline-flex shrink-0", s, className)} aria-label={ui.status[status]}>
      {ring && <span className={cn("absolute inset-0 rounded-full animate-pulse-ring motion-reduce:hidden", dotColor[status])} />}
      <span className={cn("relative inline-flex rounded-full", s, dotColor[status])} />
    </span>
  );
}

const pillCls: Record<Status, string> = {
  up: "text-up bg-up/10 border-up/20",
  degraded: "text-[color-mix(in_oklch,var(--degraded)_70%,var(--foreground))] bg-degraded/12 border-degraded/30",
  down: "text-down bg-down/10 border-down/20",
  pending: "text-muted-foreground bg-muted border-transparent",
  paused: "text-muted-foreground bg-muted border-transparent",
  maintenance: "text-maintenance bg-maintenance/10 border-maintenance/20",
};

/** Compact status label with dot. */
export function StatusPill({ status, className, label }: { status: Status; className?: string; label?: string }) {
  const ui = useUiText();
  return (
    <span
      className={cn(
        "inline-flex h-5 items-center gap-1.5 rounded-full border px-2 text-[11px] font-medium whitespace-nowrap",
        pillCls[status],
        className,
      )}
    >
      <StatusDot status={status} size="sm" />
      {label ?? ui.status[status]}
    </span>
  );
}

export function StatusIcon({ status, className }: { status: Status; className?: string }) {
  const Icon = { up: CheckIcon, degraded: TriangleAlertIcon, down: XIcon, pending: MinusIcon, paused: PauseIcon, maintenance: WrenchIcon }[status];
  const bg = { up: "bg-up", degraded: "bg-degraded", down: "bg-down", pending: "bg-pending", paused: "bg-pending", maintenance: "bg-maintenance" }[status];
  return (
    <span className={cn("inline-flex size-5 shrink-0 items-center justify-center rounded-full text-white", bg, className)}>
      <Icon className="size-3" strokeWidth={3} />
    </span>
  );
}

export type Overall = "operational" | "degraded" | "partial_outage" | "major_outage" | "maintenance" | "unknown";

const overallStatus: Record<Overall, Status> = {
  operational: "up",
  degraded: "degraded",
  partial_outage: "down",
  major_outage: "down",
  maintenance: "maintenance",
  unknown: "pending",
};

const bannerTint: Record<Status, string> = {
  up: "border-up/25 bg-up/[0.06]",
  degraded: "border-degraded/35 bg-degraded/[0.08]",
  down: "border-down/25 bg-down/[0.06]",
  pending: "border-border bg-muted/40",
  paused: "border-border bg-muted/40",
  maintenance: "border-maintenance/25 bg-maintenance/[0.06]",
};

/** Overall system state, shown at the top of the status page. */
export function StatusBanner({
  overall,
  subtitle,
  right,
  className,
}: {
  overall: Overall;
  subtitle?: React.ReactNode;
  right?: React.ReactNode;
  className?: string;
}) {
  const ui = useUiText();
  const status = overallStatus[overall];
  return (
    <div className={cn("flex items-center gap-3.5 rounded-xl border px-4 py-3.5 sm:px-5 sm:py-4", bannerTint[status], className)}>
      <StatusIcon status={status} className="size-6 [&_svg]:size-3.5" />
      <div className="min-w-0 flex-1">
        <p className="text-[15px] font-semibold tracking-tight sm:text-base">{ui.overall[overall]}</p>
        {subtitle && <p className="mt-0.5 text-[13px] text-muted-foreground">{subtitle}</p>}
      </div>
      {right}
    </div>
  );
}

const typeMeta = {
  http: { icon: GlobeIcon, label: "HTTP" },
  tcp: { icon: NetworkIcon, label: "TCP" },
  ping: { icon: RadioTowerIcon, label: "Ping" },
  dns: { icon: WaypointsIcon, label: "DNS" },
  ssl: { icon: ShieldCheckIcon, label: "SSL certificate" },
  json: { icon: BracesIcon, label: "JSON API" },
  smtp: { icon: MailIcon, label: "SMTP" },
  grpc: { icon: ServerCogIcon, label: "gRPC" },
  push: { icon: HeartPulseIcon, label: "Heartbeat" },
} as const;

export type MonitorType = keyof typeof typeMeta;

export function MonitorTypeIcon({ type, className }: { type: MonitorType; className?: string }) {
  const Icon = typeMeta[type]?.icon ?? GlobeIcon;
  return <Icon className={cn("size-4 text-muted-foreground", className)} />;
}

export const monitorTypeLabel = (t: MonitorType) => typeMeta[t]?.label ?? t;

/** "Live" indicator for streaming connections. */
export function LiveIndicator({ connected, className }: { connected: boolean; className?: string }) {
  const ui = useUiText();
  return (
    <span className={cn("inline-flex items-center gap-1.5 text-xs text-muted-foreground", className)}>
      <StatusDot status={connected ? "up" : "pending"} live={connected} size="sm" />
      {connected ? ui.live : ui.reconnecting}
    </span>
  );
}
