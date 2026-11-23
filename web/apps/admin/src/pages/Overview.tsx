import { Link } from "react-router";
import { ArrowRightIcon, CheckCircle2Icon, PlusIcon } from "lucide-react";
import { cn, formatDuration, formatLatency, timeAgo, statusLabel } from "@drix/ui/lib/utils";
import { Button } from "@drix/ui/components/button";
import { Card, CardContent, CardHeader, CardTitle, CardDescription, EmptyState } from "@drix/ui/components/display";
import { StatusDot, StatusPill, StatusIcon } from "@drix/ui/drix/status";
import { Sparkline } from "@drix/ui/drix/sparkline";
import { PageHeader, PageSkeleton } from "../components/Page";
import { useAgents, useMonitors, useOverview } from "../lib/queries";
import { ackWho, type Event } from "../api";
import { Can, useCan } from "../lib/roles";
import { useAcknowledge } from "../components/Ack";

export default function OverviewPage() {
  const { data: o, isLoading } = useOverview();
  const { data: monitors } = useMonitors();
  const canEdit = useCan()("editor");
  const ack = useAcknowledge();
  const { data: agents } = useAgents();

  if (isLoading || !o) return <PageSkeleton />;

  const attention = (monitors ?? [])
    .filter((m) => m.enabled && (m.live.status === "down" || m.live.status === "degraded"))
    .sort((a, b) => (a.live.status === "down" ? -1 : 1) - (b.live.status === "down" ? -1 : 1));
  const active = o.monitors.enabled;
  const healthyPct = active ? Math.round((o.monitors.up / active) * 100) : 0;

  return (
    <>
      <PageHeader
        title="Overview"
        description="Live health of every monitor and the worker cluster."
        actions={
          <Can role="editor">
            <Button asChild>
              <Link to="/monitors?new=1">
                <PlusIcon /> New monitor
              </Link>
            </Button>
          </Can>
        }
      />

      <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
        <Kpi
          label="Operational"
          value={`${o.monitors.up}`}
          suffix={`/ ${active}`}
          foot={<HealthBar up={o.monitors.up} degraded={o.monitors.degraded} down={o.monitors.down} total={active} />}
          hint={`${healthyPct}% healthy`}
        />
        <Kpi
          label="Incidents"
          value={`${o.monitors.down}`}
          tone={o.monitors.down ? "down" : undefined}
          hint={o.monitors.degraded ? `${o.monitors.degraded} degraded` : o.monitors.down ? "Monitors currently down" : "Nothing is down"}
        />
        <Kpi
          label="Workers online"
          value={`${o.agents_online}`}
          suffix={`/ ${o.agents_total}`}
          tone={o.agents_total > 0 && o.agents_online === 0 ? "down" : undefined}
          hint={`${Math.round(o.checks_per_min)} checks / min across the cluster`}
        />
        <Kpi label="Avg response" value={formatLatency(o.avg_latency_ms)} hint={`${o.viewers} live viewer${o.viewers === 1 ? "" : "s"}`} />
      </div>

      <div className="mt-6 grid gap-6 lg:grid-cols-[1fr_22rem]">
        <Card>
          <CardHeader>
            <div>
              <CardTitle>Needs attention</CardTitle>
              <CardDescription>Monitors that are down or slow right now.</CardDescription>
            </div>
            <Button asChild variant="ghost" size="sm">
              <Link to="/monitors">
                All monitors <ArrowRightIcon />
              </Link>
            </Button>
          </CardHeader>
          {attention.length === 0 ? (
            <EmptyState
              className="py-10"
              icon={<StatusIcon status="up" className="size-6 [&_svg]:size-3.5" />}
              title={active ? "Everything is running smoothly" : "No monitors yet"}
              description={active ? "All active monitors are passing their checks." : "Create a monitor to start checking your services."}
            />
          ) : (
            <ul className="divide-y border-t">
              {attention.map((m) => (
                <li key={m.id} className="flex items-center transition-colors hover:bg-muted/40">
                  <Link to={`/monitors/${m.id}`} className="flex min-w-0 flex-1 items-center gap-3 py-3 pl-4">
                    <StatusDot status={m.live.status} />
                    <div className="min-w-0 flex-1">
                      <p className="truncate text-[13px] font-medium">{m.name}</p>
                      <p className="truncate font-mono text-[11.5px] text-muted-foreground">{m.live.message || m.target}</p>
                      {m.ack && (
                        <p className="mt-0.5 inline-flex items-center gap-1 text-[11.5px] text-muted-foreground">
                          <CheckCircle2Icon className="size-3 text-up" /> Acknowledged by {ackWho(m.ack)}
                        </p>
                      )}
                    </div>
                    <Sparkline values={m.spark ?? []} width={72} className="hidden sm:block" />
                    <div className="w-24 text-right">
                      <StatusPill status={m.live.status} />
                      <p className="tabular mt-1 text-[11px] text-muted-foreground">for {formatDuration(m.live.since)}</p>
                    </div>
                  </Link>
                  <div className={cn("w-4 shrink-0", attention.some((x) => x.live.status === "down") && canEdit && "sm:flex sm:w-36 sm:justify-end sm:pr-4")}>
                    {canEdit && m.live.status === "down" && !m.ack && (
                      <Button size="sm" variant="outline" className="hidden sm:inline-flex" disabled={ack.isPending} onClick={() => ack.mutate(m.id)}>
                        Acknowledge
                      </Button>
                    )}
                  </div>
                </li>
              ))}
            </ul>
          )}
        </Card>

        <div className="grid content-start gap-6">
          <Card>
            <CardHeader>
              <div>
                <CardTitle>Workers</CardTitle>
                <CardDescription>Checks are split automatically.</CardDescription>
              </div>
              <Button asChild variant="ghost" size="sm">
                <Link to="/workers">
                  Manage <ArrowRightIcon />
                </Link>
              </Button>
            </CardHeader>
            <CardContent className="grid gap-2.5">
              {!agents?.agents.length ? (
                <p className="text-[13px] text-muted-foreground">
                  No workers yet.{" "}
                  <Link to="/workers?new=1" className="font-medium text-foreground underline underline-offset-4">
                    Add one
                  </Link>{" "}
                  to start running checks.
                </p>
              ) : (
                agents.agents.map((a) => (
                  <div key={a.id} className="flex items-center gap-2.5 text-[13px]">
                    <StatusDot status={a.disabled ? "paused" : a.online ? "up" : "down"} size="sm" />
                    <span className="truncate font-medium">{a.name}</span>
                    <span className="truncate text-xs text-muted-foreground">{a.region}</span>
                    <span className="tabular ml-auto text-xs text-muted-foreground">
                      {a.online ? `${a.owned} monitors` : a.last_seen_at ? `seen ${timeAgo(a.last_seen_at)}` : "never connected"}
                    </span>
                  </div>
                ))
              )}
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <div>
                <CardTitle>Recent events</CardTitle>
                <CardDescription>Status changes across all monitors.</CardDescription>
              </div>
            </CardHeader>
            <EventList events={o.events} />
          </Card>
        </div>
      </div>
    </>
  );
}

function Kpi({
  label,
  value,
  suffix,
  hint,
  tone,
  foot,
}: {
  label: string;
  value: string;
  suffix?: string;
  hint?: string;
  tone?: "down";
  foot?: React.ReactNode;
}) {
  return (
    <Card className="flex flex-col gap-1 p-4">
      <p className="text-xs font-medium text-muted-foreground">{label}</p>
      <p className={cn("tabular text-2xl font-semibold tracking-tight", tone === "down" && "text-down")}>
        {value}
        {suffix && <span className="ml-1 text-sm font-normal text-muted-foreground">{suffix}</span>}
      </p>
      {foot}
      {hint && <p className="text-xs text-muted-foreground">{hint}</p>}
    </Card>
  );
}

function HealthBar({ up, degraded, down, total }: { up: number; degraded: number; down: number; total: number }) {
  if (!total) return <div className="my-1 h-1.5 rounded-full bg-muted" />;
  const pct = (n: number) => `${(n / total) * 100}%`;
  return (
    <div className="my-1 flex h-1.5 overflow-hidden rounded-full bg-muted">
      <div className="bg-up" style={{ width: pct(up) }} />
      <div className="bg-degraded" style={{ width: pct(degraded) }} />
      <div className="bg-down" style={{ width: pct(down) }} />
    </div>
  );
}

export function EventList({ events, compact }: { events: Event[]; compact?: boolean }) {
  if (!events.length) return <CardContent className="text-[13px] text-muted-foreground">No status changes yet.</CardContent>;
  return (
    <ol className={cn("grid border-t", compact ? "" : "max-h-[22rem] overflow-y-auto")}>
      {events.map((e) => (
        <li key={e.id} className="flex items-start gap-2.5 border-b px-4 py-2.5 last:border-0">
          <StatusIcon status={e.to_status} className="mt-0.5 size-4 [&_svg]:size-2.5" />
          <div className="min-w-0 flex-1">
            <p className="text-[13px] leading-snug">
              <Link to={`/monitors/${e.monitor_id}`} className="font-medium hover:underline">
                {e.monitor_name}
              </Link>{" "}
              <span className="text-muted-foreground">
                {statusLabel[e.from_status]?.toLowerCase() ?? e.from_status} → {statusLabel[e.to_status]?.toLowerCase() ?? e.to_status}
              </span>
            </p>
            {e.message && <p className="truncate font-mono text-[11px] text-muted-foreground">{e.message}</p>}
          </div>
          <time className="tabular shrink-0 text-[11px] text-muted-foreground" title={new Date(e.created_at).toLocaleString()}>
            {timeAgo(e.created_at)}
          </time>
        </li>
      ))}
    </ol>
  );
}
