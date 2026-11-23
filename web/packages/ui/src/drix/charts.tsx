import * as React from "react";
import { Area, AreaChart, CartesianGrid, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import { cn, formatLatency } from "../lib/utils";
import { useUiText } from "../lib/i18n";

// Kept here too so existing imports keep working; the file has no chart library.
export { Sparkline } from "./sparkline";

export type LatencyPoint = { t: string; avg: number; max: number; min: number; fails: number; total: number };

function formats(locale: string) {
  return {
    tickTime: new Intl.DateTimeFormat(locale, { hour: "2-digit", minute: "2-digit" }),
    tickDay: new Intl.DateTimeFormat(locale, { month: "short", day: "numeric" }),
    tipFmt: new Intl.DateTimeFormat(locale, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" }),
  };
}

/** Response time area chart (average with a lighter max band). */
export function LatencyChart({
  points,
  range,
  height = 180,
  className,
}: {
  points: LatencyPoint[];
  range: "1h" | "24h" | "7d" | "30d";
  height?: number;
  className?: string;
}) {
  const id = React.useId().replace(/:/g, "");
  const ui = useUiText();
  const { tickTime, tickDay, tipFmt } = React.useMemo(() => formats(ui.locale), [ui.locale]);
  const data = points.map((p) => ({ ...p, ts: new Date(p.t).getTime() }));
  const short = range === "1h" || range === "24h";

  if (!data.length) {
    return (
      <div style={{ height }} className={cn("flex items-center justify-center rounded-lg border border-dashed text-xs text-muted-foreground", className)}>
        {ui.noChecks}
      </div>
    );
  }

  return (
    <div style={{ height }} className={cn("w-full text-muted-foreground", className)}>
      <ResponsiveContainer width="100%" height="100%">
        <AreaChart data={data} margin={{ top: 6, right: 4, bottom: 0, left: 0 }}>
          <defs>
            <linearGradient id={`avg-${id}`} x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor="var(--chart, var(--brand))" stopOpacity={0.22} />
              <stop offset="100%" stopColor="var(--chart, var(--brand))" stopOpacity={0} />
            </linearGradient>
          </defs>
          <CartesianGrid vertical={false} stroke="var(--border)" strokeDasharray="0" />
          <XAxis
            dataKey="ts"
            type="number"
            scale="time"
            domain={["dataMin", "dataMax"]}
            tickFormatter={(v) => (short ? tickTime : tickDay).format(new Date(v))}
            tickLine={false}
            axisLine={false}
            minTickGap={48}
            fontSize={11}
            stroke="currentColor"
          />
          <YAxis
            width={44}
            tickLine={false}
            axisLine={false}
            fontSize={11}
            stroke="currentColor"
            tickFormatter={(v) => (v >= 1000 ? `${(v / 1000).toFixed(1)}s` : `${Math.round(v)}ms`)}
          />
          <Tooltip
            cursor={{ stroke: "var(--border)", strokeWidth: 1 }}
            content={({ active, payload }) => {
              if (!active || !payload?.length) return null;
              const p = payload[0].payload as LatencyPoint & { ts: number };
              return (
                <div className="min-w-40 rounded-lg border bg-popover p-2.5 text-xs text-popover-foreground shadow-lg">
                  <p className="mb-1.5 font-medium">{tipFmt.format(new Date(p.ts))}</p>
                  <div className="grid gap-1 text-muted-foreground">
                    <Row label={ui.average} value={formatLatency(p.avg)} swatch="bg-[var(--chart,var(--brand))]" />
                    <Row label={ui.max} value={formatLatency(p.max)} swatch="bg-[color-mix(in_oklch,var(--chart,var(--brand))_30%,transparent)]" />
                    {p.fails > 0 && <Row label={ui.failed} value={`${p.fails} / ${p.total}`} swatch="bg-down" />}
                  </div>
                </div>
              );
            }}
          />
          <Area type="monotone" dataKey="max" stroke="none" fill="var(--chart, var(--brand))" fillOpacity={0.07} isAnimationActive={false} />
          <Area
            type="monotone"
            dataKey="avg"
            stroke="var(--chart, var(--brand))"
            strokeWidth={1.5}
            fill={`url(#avg-${id})`}
            isAnimationActive={false}
            dot={false}
            activeDot={{ r: 3, strokeWidth: 0, fill: "var(--chart, var(--brand))" }}
          />
        </AreaChart>
      </ResponsiveContainer>
    </div>
  );
}

function Row({ label, value, swatch }: { label: string; value: string; swatch: string }) {
  return (
    <div className="flex items-center justify-between gap-4">
      <span className="flex items-center gap-1.5">
        <span className={cn("size-2 rounded-[2px]", swatch)} />
        {label}
      </span>
      <span className="tabular font-medium text-foreground">{value}</span>
    </div>
  );
}
