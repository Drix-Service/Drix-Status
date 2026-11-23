import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { formatLatency, formatUptime } from "@drix/ui/lib/utils";
import { LatencyChart } from "@drix/ui/drix/charts";
import { Skeleton } from "@drix/ui/components/display";
import { Tabs, TabsList, TabsTrigger } from "@drix/ui/components/menu";
import { StatusDot } from "@drix/ui/drix/status";
import { fetchLatency, fetchLocations, type PublicMonitor, type Range } from "./api";
import { useI18n } from "./i18n";

export default function MonitorDetail({ monitor }: { monitor: PublicMonitor }) {
  const [range, setRange] = useState<Range>("24h");
  const { m } = useI18n();
  const { data, isLoading } = useQuery({
    queryKey: ["latency", monitor.id, range],
    queryFn: () => fetchLatency(monitor.id, range),
    staleTime: 30_000,
  });
  const stats = useMemo(() => {
    const pts = (data?.points ?? []).filter((p) => p.total > p.fails);
    if (!pts.length) return null;
    const avg = pts.reduce((s, p) => s + p.avg, 0) / pts.length;
    return { avg, max: Math.max(...pts.map((p) => p.max)), min: Math.min(...pts.map((p) => p.min)) };
  }, [data]);

  return (
    <div className="mt-4 rounded-lg border bg-subtle p-3 sm:p-4">
      <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
        <div className="flex gap-5 text-xs">
          <Stat label={m.avgResponse} value={formatLatency(stats?.avg)} />
          <Stat label={m.fastest} value={formatLatency(stats?.min)} />
          <Stat label={m.slowest} value={formatLatency(stats?.max)} />
          <Stat label={m.uptime30} value={formatUptime(monitor.uptime_30)} />
        </div>
        <Tabs value={range} onValueChange={(v) => setRange(v as Range)}>
          <TabsList>
            <TabsTrigger value="24h">24h</TabsTrigger>
            <TabsTrigger value="7d">7d</TabsTrigger>
            <TabsTrigger value="30d">30d</TabsTrigger>
          </TabsList>
        </Tabs>
      </div>
      {isLoading ? <Skeleton className="h-[160px] w-full" /> : <LatencyChart points={data?.points ?? []} range={range} height={160} />}
      {monitor.locations && <Locations id={monitor.id} />}
    </div>
  );
}

/** Where the monitor is checked from, with each location's status. */
function Locations({ id }: { id: number }) {
  const { data } = useQuery({ queryKey: ["locations", id], queryFn: () => fetchLocations(id), refetchInterval: 30_000 });
  const locs = (data?.locations ?? []).filter((l) => !l.stale);
  const { m } = useI18n();
  if (!locs.length) return null;
  return (
    <div className="mt-4 border-t pt-3">
      <p className="mb-2 text-xs text-muted-foreground">{m.checkedFrom}</p>
      <ul className="grid gap-1.5 sm:grid-cols-2">
        {locs.map((l) => (
          <li key={l.location} className="flex items-center gap-2 text-[13px]">
            <StatusDot status={l.status} size="sm" />
            <span className="min-w-0 flex-1 truncate">{l.location}</span>
            <span className="tabular text-xs text-muted-foreground">{l.status === "down" ? m.status.down : formatLatency(l.latency_ms)}</span>
          </li>
        ))}
      </ul>
    </div>
  );
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <p className="text-muted-foreground">{label}</p>
      <p className="tabular mt-0.5 font-medium">{value}</p>
    </div>
  );
}
