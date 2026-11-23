import { useMemo } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { CheckIcon, ExclamationTriangleIcon, XIcon } from "./icons";
import { cn, formatLatency, timeAgo, type Status } from "@drix/ui/lib/utils";
import { StatusDot } from "@drix/ui/drix/status";
import { Sparkline } from "@drix/ui/drix/sparkline";
import { ackWho, type Monitor } from "../api";
import { keys, useGroups, useMonitors } from "../lib/queries";
import { bootTime } from "../lib/persist";
import { ListGroup, NavScreen, Row, SkeletonGroup } from "./ui";

export const monitorStatus = (m: Monitor): Status => (m.enabled ? (m.live?.status ?? "pending") : "paused");

/** Home screen: overall state, counts, what needs attention, every group. */
export default function StatusScreen() {
  const qc = useQueryClient();
  const { data: monitors, isLoading, isError, fetchStatus, dataUpdatedAt } = useMonitors();
  const { data: groupsData } = useGroups();

  const { attention, groups, counts } = useMemo(() => {
    const list = monitors ?? [];
    const byId = new Map(list.map((m) => [m.id, m]));
    const grouped = new Set<number>();
    const groups = (groupsData?.groups ?? [])
      .map((g) => ({ name: g.name, monitors: g.monitors.map((pm) => byId.get(pm.monitor_id)).filter((m): m is Monitor => !!m) }))
      .filter((g) => g.monitors.length > 0);
    groups.forEach((g) => g.monitors.forEach((m) => grouped.add(m.id)));
    const priv = list.filter((m) => !grouped.has(m.id));
    if (priv.length) groups.push({ name: "Private", monitors: priv });

    const rank: Record<string, number> = { down: 0, degraded: 1 };
    const attention = list.filter((m) => ["down", "degraded"].includes(monitorStatus(m))).sort((a, b) => rank[monitorStatus(a)] - rank[monitorStatus(b)]);
    const counts = { up: 0, down: 0, degraded: 0, paused: 0, total: list.length };
    list.forEach((m) => {
      const s = monitorStatus(m);
      if (s === "up") counts.up++;
      else if (s === "down") counts.down++;
      else if (s === "degraded") counts.degraded++;
      else if (s === "paused") counts.paused++;
    });
    return { attention, groups, counts };
  }, [monitors, groupsData]);

  const refresh = () => Promise.all([qc.refetchQueries({ queryKey: keys.monitors }), qc.refetchQueries({ queryKey: keys.groups })]);

  const state = counts.down > 0 ? "down" : counts.degraded > 0 ? "degraded" : "up";
  const color = state === "up" ? "var(--ios-green)" : state === "down" ? "var(--ios-red)" : "var(--ios-orange)";
  const headline = state === "up" ? "All Systems Operational" : state === "down" ? (counts.down === 1 ? "1 Monitor Down" : `${counts.down} Monitors Down`) : `${counts.degraded} Degraded`;
  const sub = state === "up" ? `${counts.up} of ${counts.total} monitors up` : attention.map((m) => m.name).slice(0, 3).join(", ") + (attention.length > 3 ? "…" : "");
  const Symbol = state === "up" ? CheckIcon : state === "down" ? XIcon : ExclamationTriangleIcon;

  // Opened on saved data: say so until the first fresh answer arrives.
  const restored = !!monitors && dataUpdatedAt < bootTime;
  const staleNote = !restored ? null : isError || fetchStatus === "paused" ? `Offline · updated ${timeAgo(dataUpdatedAt)}` : "Updating…";

  return (
    <NavScreen title="Status" onRefresh={refresh}>
      {isLoading ? (
        <>
          <div className="mb-[35px] h-[132px] animate-pulse rounded-[10px] bg-[var(--ios-cell)]" />
          <SkeletonGroup />
        </>
      ) : (
        <>
          <div className="mb-[35px] rounded-[10px] bg-[var(--ios-cell)] p-4">
            <div className="flex items-center gap-3.5">
              <span className="flex size-11 shrink-0 items-center justify-center rounded-full text-white" style={{ background: color }}>
                <Symbol className="size-6" />
              </span>
              <div className="min-w-0">
                <p className="text-[20px] leading-[25px] font-semibold tracking-[0.01em]">{headline}</p>
                <p className="truncate text-[15px] text-[var(--ios-label2)]">{staleNote ?? sub}</p>
              </div>
            </div>
            <div className="mt-4 grid grid-cols-4 gap-2 border-t-[0.5px] border-[var(--ios-sep)] pt-3.5">
              <Count n={counts.up} label="Up" color="var(--ios-green)" />
              <Count n={counts.down} label="Down" color="var(--ios-red)" />
              <Count n={counts.degraded} label="Degraded" color="var(--ios-orange)" />
              <Count n={counts.paused} label="Paused" color="var(--ios-gray)" />
            </div>
          </div>

          {attention.length > 0 && (
            <ListGroup header="Needs Attention">
              {attention.map((m) => (
                <MonitorRow key={m.id} m={m} showMessage />
              ))}
            </ListGroup>
          )}

          {groups.map((g) => (
            <ListGroup key={g.name} header={g.name}>
              {g.monitors.map((m) => (
                <MonitorRow key={m.id} m={m} />
              ))}
            </ListGroup>
          ))}

          {counts.total === 0 && <p className="mt-16 text-center text-[17px] text-[var(--ios-label2)]">No Monitors Yet</p>}
        </>
      )}
    </NavScreen>
  );
}

function Count({ n, label, color }: { n: number; label: string; color: string }) {
  return (
    <div className="min-w-0">
      <p className={cn("text-[22px] leading-7 font-semibold tabular-nums", n === 0 && "text-[var(--ios-label3)]")} style={n ? { color } : undefined}>
        {n}
      </p>
      <p className="truncate text-[12px] text-[var(--ios-label2)]">{label}</p>
    </div>
  );
}

function MonitorRow({ m, showMessage }: { m: Monitor; showMessage?: boolean }) {
  const status = monitorStatus(m);
  const detail =
    status === "paused" ? "Paused" : status === "down" ? "Down" : status === "pending" || !m.live?.latency_ms ? "–" : formatLatency(m.live.latency_ms);
  return (
    <Row
      to={`/m/monitors/${m.id}`}
      leading={<StatusDot status={status} size="lg" />}
      leadingKind="dot"
      title={m.name}
      subtitle={showMessage ? (m.ack ? `Acknowledged · ${ackWho(m.ack)}` : m.live?.message || undefined) : undefined}
      detail={
        <span className="flex items-center gap-2.5">
          {m.spark && m.spark.length > 1 && status !== "paused" && !showMessage && <Sparkline values={m.spark} width={40} height={16} className="hidden opacity-70 min-[380px]:block" />}
          <span className={cn("tabular-nums", status === "down" && "text-[var(--ios-red)]")}>{detail}</span>
        </span>
      }
    />
  );
}
