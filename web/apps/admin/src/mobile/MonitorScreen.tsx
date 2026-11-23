import { useState } from "react";
import { useParams } from "react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { CalendarClockIcon, CheckIcon, HandIcon, PauseIcon, PlayIcon, TriangleAlertIcon } from "lucide-react";
import { cn, formatDuration, formatLatency, formatUptime, statusLabel, timeAgo } from "@drix/ui/lib/utils";
import { StatusDot, monitorTypeLabel } from "@drix/ui/drix/status";
import { LatencyChart } from "@drix/ui/drix/charts";
import { ackWho, api, type Event, type LatencyResponse, type LocationsResponse, type Monitor } from "../api";
import { keys } from "../lib/queries";
import { useCan } from "../lib/roles";
import { useAcknowledge } from "../components/Ack";
import { monitorStatus } from "./StatusScreen";
import { MaintenanceSheet, NewIncidentSheet } from "./sheets";
import { IconTile, ListGroup, NavScreen, Row, Segmented, SkeletonGroup } from "./ui";

type Range = "1h" | "24h" | "7d";

const statusColor: Record<string, string> = {
  up: "var(--ios-green)",
  down: "var(--ios-red)",
  degraded: "var(--ios-orange)",
  maintenance: "var(--ios-indigo)",
  paused: "var(--ios-gray)",
  pending: "var(--ios-gray)",
};

export default function MonitorScreen() {
  const id = Number(useParams().id);
  const can = useCan();
  const qc = useQueryClient();
  const [range, setRange] = useState<Range>("24h");
  const [sheet, setSheet] = useState<"maintenance" | "incident" | null>(null);

  const { data: m, isLoading } = useQuery({ queryKey: keys.monitor(id), queryFn: () => api.get<Monitor>(`/monitors/${id}`), refetchInterval: 15_000 });
  const latency = useQuery({ queryKey: ["latency", id, range], queryFn: () => api.get<LatencyResponse>(`/monitors/${id}/latency?range=${range}`), refetchInterval: 60_000 });
  const events = useQuery({ queryKey: keys.events(id), queryFn: () => api.get<Event[]>(`/events?monitor_id=${id}&limit=10`) });
  const locations = useQuery({
    queryKey: ["locations", id],
    queryFn: () => api.get<LocationsResponse>(`/monitors/${id}/locations`),
    refetchInterval: 15_000,
  });

  const toggle = useMutation({
    mutationFn: (action: "pause" | "resume") => api.post("/monitors/bulk", { action, ids: [id] }),
    onSuccess: (_, action) => {
      qc.invalidateQueries({ queryKey: keys.monitor(id) });
      qc.invalidateQueries({ queryKey: keys.monitors });
      toast.success(action === "pause" ? "Monitor paused" : "Monitor resumed");
    },
    onError: (e) => toast.error(e.message),
  });

  const ack = useAcknowledge();

  const refresh = () => Promise.all([qc.refetchQueries({ queryKey: keys.monitor(id) }), latency.refetch(), events.refetch()]);
  const back = { to: "/m", label: "Status" };

  if (isLoading || !m) {
    return (
      <NavScreen title="" back={back}>
        <SkeletonGroup rows={3} />
      </NavScreen>
    );
  }

  const status = monitorStatus(m);
  const since = m.live?.since;
  const target = m.target.replace(/^https?:\/\//, "");

  return (
    <NavScreen
      title={m.name}
      back={back}
      onRefresh={refresh}
      caption={
        <span className="flex items-center gap-1.5 truncate">
          {monitorTypeLabel(m.type)}
          {target && <span className="truncate">· {target}</span>}
        </span>
      }
    >
      <div className="mb-5 flex items-center gap-2 px-1 text-[15px]">
        <span className="inline-flex items-center gap-1.5 rounded-full px-2.5 py-[3px] text-[13px] font-semibold text-white" style={{ background: statusColor[status] }}>
          {statusLabel[status]}
        </span>
        {since && status !== "paused" && <span className="text-[var(--ios-label2)]">for {formatDuration(since)}</span>}
      </div>

      {(status === "down" || status === "degraded") && m.live?.message && (
        <ListGroup header="Last Error">
          <div className="px-4 py-3 font-mono text-[14px] leading-5 break-words text-[var(--ios-red)]">{m.live.message}</div>
        </ListGroup>
      )}

      {status === "down" && (m.ack || can("editor")) && (
        <ListGroup footer={m.ack ? "Reminders and escalations are paused until it recovers." : "Tells the team you're on it and stops reminders and escalations."}>
          {m.ack ? (
            <Row
              leading={<IconTile color="var(--ios-green)"><CheckIcon /></IconTile>}
              leadingKind="tile"
              title={`Acknowledged by ${ackWho(m.ack)}`}
              detail={timeAgo(m.ack.at)}
            />
          ) : (
            <Row
              leading={<IconTile color="var(--ios-orange)"><HandIcon /></IconTile>}
              leadingKind="tile"
              title="Acknowledge"
              loading={ack.isPending}
              onClick={() => ack.mutate(m.id)}
            />
          )}
        </ListGroup>
      )}

      <div className="mb-[35px] grid grid-cols-2 gap-2.5">
        <Stat label="Response" value={status === "down" || status === "paused" || !m.live?.latency_ms ? "–" : formatLatency(m.live.latency_ms)} />
        <Stat label="Uptime · 30 days" value={formatUptime(m.uptime_30)} />
        <Stat label="Last Check" value={m.live?.last_checked ? timeAgo(m.live.last_checked) : "–"} />
        <Stat label="Interval" value={m.interval_s >= 60 ? `${Math.round(m.interval_s / 60)} min` : `${m.interval_s} sec`} />
      </div>

      {m.multi_location && (
        <ListGroup header="Locations" footer="Marked down when most locations fail.">
          {locations.data?.locations.length ? (
            locations.data.locations.map((l) => (
              <Row
                key={l.location}
                leading={<StatusDot status={l.stale ? "pending" : l.status} size="lg" />}
                leadingKind="dot"
                title={l.location}
                subtitle={l.stale ? "No recent checks" : `24h: ${l.avg_24h_ms ? formatLatency(l.avg_24h_ms) + " avg" : "–"} · ${formatUptime(l.uptime_24h)} up`}
                detail={<span className="tabular-nums">{l.status === "down" ? <span className="text-[var(--ios-red)]">Down</span> : l.latency_ms ? formatLatency(l.latency_ms) : "–"}</span>}
              />
            ))
          ) : (
            <Row title={<span className="text-[var(--ios-label2)]">Waiting for the first checks</span>} />
          )}
        </ListGroup>
      )}

      <ListGroup header="Response Time">
        <div className="p-3 pt-3.5">
          <Segmented<Range>
            className="mb-3"
            value={range}
            onChange={setRange}
            options={[
              { value: "1h", label: "Hour" },
              { value: "24h", label: "Day" },
              { value: "7d", label: "Week" },
            ]}
          />
          <LatencyChart points={latency.data?.points ?? []} range={range} height={170} />
        </div>
      </ListGroup>

      {can("editor") && (
        <ListGroup>
          {m.enabled ? (
            <Row
              leading={<IconTile color="var(--ios-gray)"><PauseIcon fill="currentColor" /></IconTile>}
              leadingKind="tile"
              title="Pause Monitor"
              loading={toggle.isPending}
              onClick={() => toggle.mutate("pause")}
            />
          ) : (
            <Row
              leading={<IconTile color="var(--ios-green)"><PlayIcon fill="currentColor" /></IconTile>}
              leadingKind="tile"
              title="Resume Monitor"
              loading={toggle.isPending}
              onClick={() => toggle.mutate("resume")}
            />
          )}
          <Row
            leading={<IconTile color="var(--ios-indigo)"><CalendarClockIcon /></IconTile>}
            leadingKind="tile"
            title="Start Maintenance…"
            onClick={() => setSheet("maintenance")}
          />
          <Row
            leading={<IconTile color="var(--ios-red)"><TriangleAlertIcon /></IconTile>}
            leadingKind="tile"
            title="Report Incident…"
            onClick={() => setSheet("incident")}
          />
        </ListGroup>
      )}

      <ListGroup header="Recent Changes">
        {events.data?.length ? (
          events.data.map((e) => (
            <Row
              key={e.id}
              leading={<StatusDot status={e.to_status} size="lg" />}
              leadingKind="dot"
              title={statusLabel[e.to_status]}
              subtitle={e.message || undefined}
              detail={<span className="text-[15px]">{timeAgo(e.created_at)}</span>}
            />
          ))
        ) : (
          <Row title={<span className="text-[var(--ios-label2)]">No changes yet</span>} />
        )}
      </ListGroup>

      <MaintenanceSheet open={sheet === "maintenance"} onOpenChange={(o) => !o && setSheet(null)} monitorIds={[m.id]} defaultTitle={`Maintenance on ${m.name}`} />
      <NewIncidentSheet open={sheet === "incident"} onOpenChange={(o) => !o && setSheet(null)} monitorIds={[m.id]} />
    </NavScreen>
  );
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-[10px] bg-[var(--ios-cell)] px-4 py-3">
      <p className="text-[13px] text-[var(--ios-label2)]">{label}</p>
      <p className={cn("mt-0.5 text-[22px] leading-7 font-semibold tabular-nums", value === "–" && "text-[var(--ios-label3)]")}>{value}</p>
    </div>
  );
}
