import { useState } from "react";
import { useParams } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { CheckIcon, MessageSquareTextIcon, SquareIcon } from "lucide-react";
import { formatDateTime, timeAgo } from "@drix/ui/lib/utils";
import { StatusDot } from "@drix/ui/drix/status";
import { api, type Incident, type IncidentStatus } from "../api";
import { useMonitors } from "../lib/queries";
import { useCan } from "../lib/roles";
import { incidentColor, incidentStatusLabel } from "./IncidentsScreen";
import { monitorStatus } from "./StatusScreen";
import { UpdateSheet } from "./sheets";
import { IconTile, ListGroup, NavScreen, Row, SkeletonGroup } from "./ui";

export default function IncidentScreen() {
  const id = Number(useParams().id);
  const can = useCan();
  const { data: monitors } = useMonitors();
  const [sheet, setSheet] = useState<{ status?: IncidentStatus } | null>(null);
  const { data: inc, isLoading, refetch } = useQuery({ queryKey: ["incident", id], queryFn: () => api.get<Incident>(`/incidents/${id}`), refetchInterval: 15_000 });
  const back = { to: "/m/incidents", label: "Incidents" };

  if (isLoading || !inc) {
    return (
      <NavScreen title="" back={back}>
        <SkeletonGroup rows={3} />
      </NavScreen>
    );
  }

  const isMaint = inc.kind === "maintenance";
  const open = !inc.resolved_at;
  const affected = inc.monitors.map((a) => monitors?.find((m) => m.id === a.monitor_id)).filter((m) => !!m);
  const window =
    inc.scheduled_start && inc.scheduled_end
      ? `${formatDateTime(inc.scheduled_start)} – ${new Date(inc.scheduled_end).toLocaleTimeString("en", { hour: "2-digit", minute: "2-digit" })}`
      : null;

  return (
    <NavScreen
      title={inc.title}
      back={back}
      onRefresh={refetch}
      caption={isMaint ? "Maintenance" : `Incident · ${inc.impact[0].toUpperCase() + inc.impact.slice(1)} Impact`}
    >
      <div className="mb-5 flex flex-wrap items-center gap-2 px-1 text-[15px]">
        <span className="rounded-full px-2.5 py-[3px] text-[13px] font-semibold text-white" style={{ background: incidentColor(inc) }}>
          {incidentStatusLabel[inc.status]}
        </span>
        <span className="text-[var(--ios-label2)]">{window ?? `Updated ${timeAgo(inc.updated_at)}`}</span>
      </div>

      {can("editor") && open && (
        <ListGroup>
          <Row
            leading={<IconTile color="var(--ios-gray)"><MessageSquareTextIcon /></IconTile>}
            leadingKind="tile"
            title="Post Update…"
            onClick={() => setSheet({})}
          />
          {isMaint ? (
            inc.status === "in_progress" && (
              <Row
                leading={<IconTile color="var(--ios-indigo)"><SquareIcon fill="currentColor" /></IconTile>}
                leadingKind="tile"
                title="End Maintenance Now"
                onClick={() => setSheet({ status: "completed" })}
              />
            )
          ) : (
            <Row
              leading={<IconTile color="var(--ios-green)"><CheckIcon strokeWidth={3} /></IconTile>}
              leadingKind="tile"
              title="Resolve"
              onClick={() => setSheet({ status: "resolved" })}
            />
          )}
        </ListGroup>
      )}

      {affected.length > 0 && (
        <ListGroup header="Affected">
          {affected.map((m) => (
            <Row key={m!.id} to={`/m/monitors/${m!.id}`} leading={<StatusDot status={monitorStatus(m!)} size="lg" />} leadingKind="dot" title={m!.name} />
          ))}
        </ListGroup>
      )}

      <ListGroup header="Updates">
        {inc.updates.length ? (
          <ol className="px-4 py-1">
            {inc.updates.map((u, i) => (
              <li key={u.id} className="relative pb-4 pl-6 last:pb-3">
                {i < inc.updates.length - 1 && <span className="absolute top-[18px] bottom-0 left-[4.5px] w-px bg-[var(--ios-sep)]" />}
                <span className="absolute top-[13px] left-0 size-2.5 rounded-full" style={{ background: i === 0 ? incidentColor(inc) : "var(--ios-label3)" }} />
                <div className="flex items-baseline justify-between gap-3 pt-2.5">
                  <span className="text-[15px] font-semibold">{incidentStatusLabel[u.status]}</span>
                  <span className="shrink-0 text-[13px] text-[var(--ios-label2)]">{timeAgo(u.created_at)}</span>
                </div>
                <p className="mt-0.5 text-[17px] leading-[22px] whitespace-pre-wrap">{u.body}</p>
                {u.author && <p className="mt-0.5 text-[13px] text-[var(--ios-label2)]">{u.author}</p>}
              </li>
            ))}
          </ol>
        ) : (
          <Row title={<span className="text-[var(--ios-label2)]">No updates yet</span>} />
        )}
      </ListGroup>

      {sheet && <UpdateSheet open onOpenChange={(o) => !o && setSheet(null)} incident={inc} initialStatus={sheet.status} />}
    </NavScreen>
  );
}
