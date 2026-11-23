import { useMemo, useState } from "react";
import { Link, useNavigate, useParams } from "react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { ChevronLeftIcon, EyeOffIcon, PauseIcon, PencilIcon, PlayIcon, ShieldCheckIcon, Trash2Icon } from "lucide-react";
import { cn, formatDateTime, formatDuration, formatLatency, formatUptime, timeAgo } from "@drix/ui/lib/utils";
import { Button } from "@drix/ui/components/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle, Skeleton, Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@drix/ui/components/display";
import { Tabs, TabsList, TabsTrigger } from "@drix/ui/components/menu";
import { ConfirmDialog } from "@drix/ui/components/overlay";
import { MonitorTypeIcon, StatusPill, monitorTypeLabel } from "@drix/ui/drix/status";
import { LatencyChart } from "@drix/ui/drix/charts";
import { api, type Event, type LatencyResponse, type LocationsResponse, type Monitor, type ResultRow } from "../api";
import { keys, useMe } from "../lib/queries";
import { MonitorSheet } from "../components/MonitorForm";
import { CodeBlock, CopyButton, PageSkeleton } from "../components/Page";
import { EventList } from "./Overview";
import { useCan } from "../lib/roles";
import { AckBar } from "../components/Ack";

type Range = "1h" | "24h" | "7d" | "30d";

export default function MonitorDetailPage() {
  const id = Number(useParams().id);
  const navigate = useNavigate();
  const qc = useQueryClient();
  const [range, setRange] = useState<Range>("24h");
  const [editing, setEditing] = useState(false);
  const [confirm, setConfirm] = useState(false);
  const canEdit = useCan()("editor");

  const { data: m, isLoading, isError } = useQuery({ queryKey: keys.monitor(id), queryFn: () => api.get<Monitor>(`/monitors/${id}`), refetchInterval: 15_000 });
  const latency = useQuery({
    queryKey: ["latency", id, range],
    queryFn: () => api.get<LatencyResponse>(`/monitors/${id}/latency?range=${range}`),
    refetchInterval: 30_000,
  });
  const results = useQuery({ queryKey: ["results", id], queryFn: () => api.get<ResultRow[]>(`/monitors/${id}/results?limit=25`), refetchInterval: 15_000 });
  const events = useQuery({ queryKey: keys.events(id), queryFn: () => api.get<Event[]>(`/events?monitor_id=${id}&limit=20`) });

  const action = useMutation({
    mutationFn: (a: "pause" | "resume" | "delete") => api.post("/monitors/bulk", { action: a, ids: [id] }),
    onSuccess: (_, a) => {
      qc.invalidateQueries({ queryKey: keys.monitors });
      qc.invalidateQueries({ queryKey: keys.monitor(id) });
      if (a === "delete") {
        toast.success("Monitor deleted");
        navigate("/monitors");
      } else toast.success(a === "pause" ? "Monitor paused" : "Monitor resumed");
    },
    onError: (e) => toast.error(e.message),
  });

  const stats = useMemo(() => {
    const pts = (latency.data?.points ?? []).filter((p) => p.total > p.fails);
    const all = latency.data?.points ?? [];
    if (!all.length) return null;
    const total = all.reduce((s, p) => s + p.total, 0);
    const fails = all.reduce((s, p) => s + p.fails, 0);
    return {
      avg: pts.length ? pts.reduce((s, p) => s + p.avg * (p.total - p.fails), 0) / Math.max(1, total - fails) : 0,
      max: pts.length ? Math.max(...pts.map((p) => p.max)) : 0,
      uptime: total ? ((total - fails) / total) * 100 : -1,
      checks: total,
    };
  }, [latency.data]);

  if (isLoading) return <PageSkeleton />;
  if (isError || !m) {
    return (
      <div className="py-20 text-center text-sm text-muted-foreground">
        Monitor not found. <Link to="/monitors" className="font-medium text-foreground underline">Back to monitors</Link>
      </div>
    );
  }

  const certDays = m.live.cert_expiry ? Math.floor((new Date(m.live.cert_expiry).getTime() - Date.now()) / 86_400_000) : null;

  return (
    <>
      <Link to="/monitors" className="mb-4 inline-flex items-center gap-1 text-[13px] text-muted-foreground hover:text-foreground">
        <ChevronLeftIcon className="size-4" /> Monitors
      </Link>
      <div className="mb-6 flex flex-wrap items-start justify-between gap-4">
        <div className="flex min-w-0 items-start gap-3">
          <span className="mt-0.5 flex size-10 shrink-0 items-center justify-center rounded-lg border bg-muted/40">
            <MonitorTypeIcon type={m.type} className="size-5" />
          </span>
          <div className="min-w-0">
            <div className="flex flex-wrap items-center gap-2">
              <h1 className="truncate text-lg font-semibold tracking-tight">{m.name}</h1>
              <StatusPill status={m.live.status} />
              {m.groups.length === 0 && (
                <span className="inline-flex items-center gap-1 text-xs text-muted-foreground">
                  <EyeOffIcon className="size-3" /> Hidden
                </span>
              )}
            </div>
            <p className="mt-0.5 truncate font-mono text-[12.5px] text-muted-foreground">{m.target}</p>
          </div>
        </div>
        {canEdit && <div className="flex gap-2">
          <Button variant="outline" onClick={() => action.mutate(m.enabled ? "pause" : "resume")}>
            {m.enabled ? <PauseIcon /> : <PlayIcon />} {m.enabled ? "Pause" : "Resume"}
          </Button>
          <Button variant="outline" onClick={() => setEditing(true)}>
            <PencilIcon /> Edit
          </Button>
          <Button variant="ghost" size="icon" onClick={() => setConfirm(true)} aria-label="Delete monitor" className="text-muted-foreground hover:text-destructive">
            <Trash2Icon />
          </Button>
        </div>}
      </div>

      <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
        <Stat label="Current response" value={m.live.status === "down" ? "—" : formatLatency(m.live.latency_ms)} sub={m.live.last_checked ? `checked ${timeAgo(m.live.last_checked)}` : "waiting for first check"} />
        <Stat label={m.live.status === "down" ? "Down for" : "Up for"} value={m.live.since ? formatDuration(m.live.since) : "—"} sub={m.live.since ? `since ${formatDateTime(m.live.since)}` : undefined} tone={m.live.status === "down" ? "down" : undefined} />
        <Stat label="Uptime (30 days)" value={formatUptime(m.uptime_30)} sub={stats ? `${formatUptime(stats.uptime)} in selected range` : undefined} />
        <Stat label="Checked by" value={m.owner || "—"} sub={`every ${m.interval_s}s · ${m.retries} ${m.retries === 1 ? "retry" : "retries"}`} />
      </div>

      {m.live.message && (
        <div className={cn("mt-3 rounded-lg border px-3 py-2 font-mono text-[12px]", m.live.status === "down" ? "border-down/25 bg-down/[0.05] text-down" : "bg-muted/40 text-muted-foreground")}>
          {m.live.message}
        </div>
      )}

      <AckBar monitor={m} />

      <div className="mt-6 grid gap-6 lg:grid-cols-[1fr_20rem]">
        <div className="grid min-w-0 content-start gap-6">
          <Card>
            <CardHeader>
              <div>
                <CardTitle>Response time</CardTitle>
                <CardDescription className="tabular">
                  {stats ? `avg ${formatLatency(stats.avg)} · max ${formatLatency(stats.max)} · ${stats.checks} checks` : "No data yet"}
                </CardDescription>
              </div>
              <Tabs value={range} onValueChange={(v) => setRange(v as Range)}>
                <TabsList>
                  {(["1h", "24h", "7d", "30d"] as const).map((r) => (
                    <TabsTrigger key={r} value={r}>
                      {r}
                    </TabsTrigger>
                  ))}
                </TabsList>
              </Tabs>
            </CardHeader>
            <CardContent>
              {latency.isLoading ? <Skeleton className="h-[220px]" /> : <LatencyChart points={latency.data?.points ?? []} range={range} height={220} />}
            </CardContent>
          </Card>

          {m.multi_location && <LocationsCard id={id} />}

          <Card className="overflow-hidden">
            <CardHeader>
              <div>
                <CardTitle>Recent checks</CardTitle>
                <CardDescription>The last 25 results across all workers.</CardDescription>
              </div>
            </CardHeader>
            <Table>
              <TableHeader>
                <TableRow className="hover:bg-transparent">
                  <TableHead className="pl-4">Time</TableHead>
                  <TableHead>Result</TableHead>
                  <TableHead>Response</TableHead>
                  <TableHead className="hidden sm:table-cell">Worker</TableHead>
                  <TableHead className="hidden md:table-cell">Message</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {results.data?.map((r, i) => (
                  <TableRow key={`${r.checked_at}-${i}`}>
                    <TableCell className="tabular pl-4 text-xs text-muted-foreground">{formatDateTime(r.checked_at)}</TableCell>
                    <TableCell>
                      <StatusPill status={r.status} label={r.status === "up" ? "OK" : r.status === "degraded" ? "Slow" : "Failed"} />
                    </TableCell>
                    <TableCell className="tabular text-xs">{r.status === "down" ? "—" : formatLatency(r.latency_ms)}</TableCell>
                    <TableCell className="hidden text-xs text-muted-foreground sm:table-cell">{r.agent_name || "—"}</TableCell>
                    <TableCell className="hidden max-w-xs truncate font-mono text-[11.5px] text-muted-foreground md:table-cell">{r.message}</TableCell>
                  </TableRow>
                ))}
                {!results.data?.length && (
                  <TableRow className="hover:bg-transparent">
                    <TableCell colSpan={5} className="py-8 text-center text-[13px] text-muted-foreground">
                      No checks recorded yet.
                    </TableCell>
                  </TableRow>
                )}
              </TableBody>
            </Table>
          </Card>
        </div>

        <div className="grid min-w-0 content-start gap-6">
          {m.type === "push" && m.push_token && <HeartbeatCard monitor={m} canEdit={canEdit} />}
          <Card>
            <CardHeader>
              <CardTitle>Configuration</CardTitle>
            </CardHeader>
            <CardContent>
              <dl className="grid gap-2.5 text-[13px]">
                <Row k="Type" v={monitorTypeLabel(m.type)} />
                <Row k="Groups" v={m.groups.length ? m.groups.map((g) => g.name).join(", ") : "Private"} />
                <Row k="Interval" v={`${m.interval_s}s`} />
                <Row k="Timeout" v={`${m.timeout_ms} ms`} />
                <Row k="Slow threshold" v={m.degraded_ms ? `${m.degraded_ms} ms` : "Off"} />
                {m.type === "http" && <Row k="Method" v={m.config.method || "GET"} />}
                {m.type === "http" && m.config.keyword && <Row k="Must contain" v={m.config.keyword} />}
                {m.type === "dns" && <Row k="Record" v={m.config.record_type || "A"} />}
                {m.type === "dns" && <Row k="Resolver" v={m.config.resolver || "1.1.1.1"} />}
                {m.type === "ssl" && <Row k="Warn / down below" v={`${m.config.warn_days || 14} / ${m.config.crit_days || 7} days`} />}
                {m.type === "json" && <Row k="Assertions" v={String(m.config.assertions?.length ?? 0)} />}
                {m.type === "smtp" && <Row k="STARTTLS" v={m.config.starttls ? "Required" : "No"} />}
                {m.type === "grpc" && <Row k="Service" v={m.config.service || "(whole server)"} />}
                {m.type === "push" && <Row k="Grace period" v={`${m.config.grace_s ?? 0}s`} />}
              </dl>
              {certDays !== null && (
                <div className={cn("mt-4 flex items-center gap-2 rounded-md border px-2.5 py-2 text-xs", certDays < 14 ? "border-degraded/40 bg-degraded/10" : "bg-muted/40")}>
                  <ShieldCheckIcon className={cn("size-4", certDays < 14 ? "text-degraded" : "text-up")} />
                  TLS certificate expires in <span className="tabular font-medium">{certDays} days</span>
                </div>
              )}
            </CardContent>
          </Card>
          <Card>
            <CardHeader>
              <CardTitle>Status changes</CardTitle>
            </CardHeader>
            <EventList events={events.data ?? []} compact />
          </Card>
        </div>
      </div>

      <MonitorSheet open={editing} onOpenChange={setEditing} monitor={m} />
      <ConfirmDialog
        open={confirm}
        onOpenChange={setConfirm}
        title="Delete this monitor?"
        description="Check history and uptime data are removed permanently."
        confirmLabel="Delete"
        destructive
        onConfirm={() => action.mutate("delete")}
      />
    </>
  );
}

function Stat({ label, value, sub, tone }: { label: string; value: string; sub?: string; tone?: "down" }) {
  return (
    <Card className="p-4">
      <p className="text-xs font-medium text-muted-foreground">{label}</p>
      <p className={cn("tabular mt-1 truncate text-xl font-semibold tracking-tight", tone === "down" && "text-down")}>{value}</p>
      {sub && <p className="mt-0.5 truncate text-xs text-muted-foreground">{sub}</p>}
    </Card>
  );
}

function Row({ k, v }: { k: string; v: string }) {
  return (
    <div className="flex items-baseline justify-between gap-4">
      <dt className="text-muted-foreground">{k}</dt>
      <dd className="truncate text-right font-medium">{v}</dd>
    </div>
  );
}

function HeartbeatCard({ monitor: m, canEdit }: { monitor: Monitor; canEdit: boolean }) {
  const qc = useQueryClient();
  const { data: me } = useMe();
  const base = (me?.public_url ?? location.origin).replace(/\/$/, "");
  const url = `${base}/api/push/${m.push_token}`;
  const every = cronFor(m.interval_s);
  const rotate = useMutation({
    mutationFn: () => api.post<Monitor>(`/monitors/${m.id}/push-token`),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: keys.monitor(m.id) });
      toast.success("New heartbeat URL created", { description: "Update your job; the old URL no longer works." });
    },
  });
  return (
    <Card>
      <CardHeader>
        <div>
          <CardTitle>Heartbeat URL</CardTitle>
          <CardDescription>Call it whenever the job finishes.</CardDescription>
        </div>
      </CardHeader>
      <CardContent className="grid min-w-0 gap-3 [&>*]:min-w-0">
        <div className="flex items-center gap-1 rounded-lg border bg-muted/50 py-1 pr-1 pl-2.5">
          <code className="min-w-0 flex-1 truncate font-mono text-[11.5px]">{url}</code>
          <CopyButton value={url} />
        </div>
        <div className="grid gap-1.5">
          <p className="text-xs font-medium">Shell / cron</p>
          <CodeBlock code={`${every} /path/to/job.sh && curl -fsS -m 10 ${url}`} />
        </div>
        <div className="grid gap-1.5">
          <p className="text-xs font-medium">Report a failure or timing</p>
          <CodeBlock code={`curl -fsS "${url}?status=down&msg=disk%20full"\ncurl -fsS "${url}?ping=1840"   # took 1.84s`} />
        </div>
        {canEdit && (
          <Button variant="ghost" size="sm" className="w-fit text-muted-foreground" onClick={() => rotate.mutate()} disabled={rotate.isPending}>
            Create a new URL
          </Button>
        )}
      </CardContent>
    </Card>
  );
}

/** A cron schedule matching the interval (minute and hour fields stay within range). */
function cronFor(seconds: number): string {
  const min = Math.round(seconds / 60);
  if (min >= 1440 && min % 1440 === 0) return "0 0 * * *";
  if (min >= 60 && min % 60 === 0) return min === 60 ? "0 * * * *" : `0 */${min / 60} * * *`;
  if (min > 1 && min < 60 && 60 % min === 0) return `*/${min} * * * *`;
  return "* * * * *";
}

/** Status and response time per location, for monitors checked from every location. */
function LocationsCard({ id }: { id: number }) {
  const { data, isLoading } = useQuery({
    queryKey: ["locations", id],
    queryFn: () => api.get<LocationsResponse>(`/monitors/${id}/locations`),
    refetchInterval: 15_000,
  });
  const locs = data?.locations ?? [];
  return (
    <Card className="overflow-hidden">
      <CardHeader>
        <div>
          <CardTitle>Locations</CardTitle>
          <CardDescription>Checked from every location. It's marked down when most locations fail.</CardDescription>
        </div>
      </CardHeader>
      <Table>
        <TableHeader>
          <TableRow className="hover:bg-transparent">
            <TableHead className="pl-4">Location</TableHead>
            <TableHead>Status</TableHead>
            <TableHead>Now</TableHead>
            <TableHead className="hidden sm:table-cell">Avg (24h)</TableHead>
            <TableHead className="hidden sm:table-cell">Uptime (24h)</TableHead>
            <TableHead className="hidden md:table-cell">Message</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {locs.map((l) => (
            <TableRow key={l.location} className={cn(l.stale && "opacity-50")}>
              <TableCell className="pl-4 text-[13px] font-medium">
                {l.location}
                {l.stale && <span className="ml-1.5 text-xs font-normal text-muted-foreground">(no recent checks)</span>}
              </TableCell>
              <TableCell>
                <StatusPill status={l.status} />
              </TableCell>
              <TableCell className="tabular text-xs">{l.status === "down" || !l.latency_ms ? "—" : formatLatency(l.latency_ms)}</TableCell>
              <TableCell className="tabular hidden text-xs sm:table-cell">{l.avg_24h_ms ? formatLatency(l.avg_24h_ms) : "—"}</TableCell>
              <TableCell className="tabular hidden text-xs sm:table-cell">{formatUptime(l.uptime_24h)}</TableCell>
              <TableCell className="hidden max-w-xs truncate font-mono text-[11.5px] text-muted-foreground md:table-cell">{l.message}</TableCell>
            </TableRow>
          ))}
          {!locs.length && (
            <TableRow className="hover:bg-transparent">
              <TableCell colSpan={6} className="py-8 text-center text-[13px] text-muted-foreground">
                {isLoading ? "Loading…" : "Waiting for the first checks from each location."}
              </TableCell>
            </TableRow>
          )}
        </TableBody>
      </Table>
    </Card>
  );
}
