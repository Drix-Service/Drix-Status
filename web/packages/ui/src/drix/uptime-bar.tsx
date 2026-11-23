import * as React from "react";
import { cn } from "../lib/utils";
import { useUiText } from "../lib/i18n";

export type DayBucket = {
  /** ISO date (YYYY-MM-DD) */
  d: string;
  /** uptime percent, -1 = no data */
  u: number;
  /** failed checks */
  x?: number;
  /** degraded checks */
  g?: number;
  /** total checks */
  n?: number;
  /** incidents and maintenance on this day */
  i?: { id: number; title: string; impact: "none" | "minor" | "major" | "critical"; kind: "incident" | "maintenance" }[];
};

function barColor(day: DayBucket): string {
  // A declared incident colors the day by its impact, even if checks passed.
  const inc = day.i?.filter((x) => x.kind === "incident") ?? [];
  if (inc.some((x) => x.impact === "critical" || x.impact === "major")) return "bg-down";
  if (inc.some((x) => x.impact === "minor") && (day.u < 0 || day.u >= 98)) return "bg-degraded";
  if (day.i?.some((x) => x.kind === "maintenance") && (day.u < 0 || day.u >= 99.9)) return "bg-maintenance";
  if (day.u < 0) return "bg-nodata";
  if (day.u >= 99.9 && !day.g) return "bg-up";
  if (day.u >= 99.9) return "bg-up/70";
  if (day.u >= 98) return "bg-degraded";
  return "bg-down";
}

/**
 * 90-day uptime history. Shows 90/60/30 bars depending on width. Uses one
 * shared hover card per row instead of a tooltip per bar, so hundreds of rows
 * stay cheap to render.
 */
export function UptimeBar({ days, className }: { days: DayBucket[]; className?: string }) {
  const [hover, setHover] = React.useState<{ i: number; x: number } | null>(null);
  const ref = React.useRef<HTMLDivElement>(null);
  const n = days.length;
  const ui = useUiText();
  const dayFmt = React.useMemo(() => new Intl.DateTimeFormat(ui.locale, { month: "short", day: "numeric", year: "numeric", timeZone: "UTC" }), [ui.locale]);

  const onMove = (e: React.PointerEvent) => {
    const el = ref.current;
    if (!el) return;
    const bars = el.querySelectorAll<HTMLElement>("[data-bar]");
    const x = e.clientX;
    for (const b of bars) {
      const r = b.getBoundingClientRect();
      if (r.width > 0 && x >= r.left - 1 && x <= r.right + 1) {
        const rect = el.getBoundingClientRect();
        setHover({ i: Number(b.dataset.bar), x: r.left + r.width / 2 - rect.left });
        return;
      }
    }
  };

  const day = hover ? days[hover.i] : null;

  return (
    <div className={cn("relative", className)}>
      <div
        ref={ref}
        className="flex h-8 items-stretch gap-[2px]"
        onPointerMove={onMove}
        onPointerLeave={() => setHover(null)}
        role="img"
        aria-label={ui.uptimeHistory(n)}
      >
        {days.map((d, i) => (
          <div
            key={d.d}
            data-bar={i}
            className={cn(
              "min-w-0 flex-1 rounded-[2px] transition-opacity",
              barColor(d),
              // Responsive: 30 bars on phones, 60 on tablets, 90 on desktop.
              i < n - 60 && "hidden md:block",
              i >= n - 60 && i < n - 30 && "hidden sm:block",
              hover && hover.i !== i && "opacity-60",
            )}
          />
        ))}
      </div>
      {day && hover && (
        <div
          className="pointer-events-none absolute bottom-full z-20 mb-2 w-52 -translate-x-1/2 rounded-lg border bg-popover p-2.5 text-xs shadow-lg"
          style={{ left: Math.max(104, Math.min(hover.x, (ref.current?.clientWidth ?? 0) - 104)) }}
        >
          <p className="font-medium">{dayFmt.format(new Date(day.d + "T12:00:00Z"))}</p>
          {day.u < 0 ? (
            <p className="mt-1 text-muted-foreground">{ui.noData}</p>
          ) : (
            <div className="mt-1.5 grid gap-1 text-muted-foreground">
              <div className="flex justify-between">
                <span>{ui.uptime}</span>
                <span className="tabular font-medium text-foreground">{day.u.toFixed(2)}%</span>
              </div>
              {!!day.x && (
                <div className="flex justify-between">
                  <span>{ui.failedChecks}</span>
                  <span className="tabular font-medium text-down">{day.x}</span>
                </div>
              )}
              {!!day.g && (
                <div className="flex justify-between">
                  <span>{ui.slowChecks}</span>
                  <span className="tabular font-medium text-foreground">{day.g}</span>
                </div>
              )}
              {!day.x && !day.g && !day.i?.length && <p>{ui.noIncidents}</p>}
            </div>
          )}
          {!!day.i?.length && (
            <ul className="mt-2 grid gap-1 border-t pt-2">
              {day.i.slice(0, 3).map((x) => (
                <li key={x.id} className="flex items-start gap-1.5 text-foreground">
                  <span
                    className={cn(
                      "mt-1 size-1.5 shrink-0 rounded-full",
                      x.kind === "maintenance" ? "bg-maintenance" : x.impact === "minor" || x.impact === "none" ? "bg-degraded" : "bg-down",
                    )}
                  />
                  <span className="line-clamp-2">{x.title}</span>
                </li>
              ))}
              {day.i.length > 3 && <li className="text-muted-foreground">{ui.more(day.i.length - 3)}</li>}
            </ul>
          )}
        </div>
      )}
    </div>
  );
}

export function UptimeLegend({ days, center }: { days: number; center?: React.ReactNode }) {
  const ui = useUiText();
  return (
    <div className="mt-1.5 grid grid-cols-[1fr_auto_1fr] items-center gap-3 text-[11px] text-muted-foreground">
      <span className="flex items-center gap-2">
        <span className="sm:hidden">{ui.daysAgo(30)}</span>
        <span className="hidden sm:inline md:hidden">{ui.daysAgo(60)}</span>
        <span className="hidden md:inline">{ui.daysAgo(days)}</span>
        <span className="hidden h-px flex-1 bg-border sm:block" />
      </span>
      <span>{center}</span>
      <span className="flex items-center gap-2">
        <span className="hidden h-px flex-1 bg-border sm:block" />
        <span className="ml-auto">{ui.today}</span>
      </span>
    </div>
  );
}
