import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { PlusIcon } from "lucide-react";
import { timeAgo } from "@drix/ui/lib/utils";
import { api, type Incident } from "../api";
import { useCan } from "../lib/roles";
import { openIncidentsQuery } from "./queries";
import { NewIncidentSheet } from "./sheets";
import { BarButton, ListGroup, NavScreen, Row, SkeletonGroup } from "./ui";

export const incidentStatusLabel: Record<string, string> = {
  investigating: "Investigating",
  identified: "Identified",
  monitoring: "Monitoring",
  resolved: "Resolved",
  scheduled: "Scheduled",
  in_progress: "In Progress",
  completed: "Completed",
  cancelled: "Cancelled",
};

/** The colour that stands for an incident: impact, or indigo for maintenance. */
export function incidentColor(inc: Incident) {
  if (inc.resolved_at) return "var(--ios-gray)";
  if (inc.kind === "maintenance") return "var(--ios-indigo)";
  return inc.impact === "minor" ? "var(--ios-orange)" : "var(--ios-red)";
}

/** Open incidents, active and upcoming maintenance, and recent history. */
export default function IncidentsScreen() {
  const can = useCan();
  const qc = useQueryClient();
  const [creating, setCreating] = useState(false);
  const open = useQuery(openIncidentsQuery);
  const closed = useQuery({ queryKey: ["m-incidents", "closed"], queryFn: () => api.get<Incident[]>("/incidents?state=closed&limit=10") });

  const incidents = (open.data ?? []).filter((i) => i.kind === "incident");
  const maintenance = (open.data ?? []).filter((i) => i.kind === "maintenance");

  return (
    <NavScreen
      title="Incidents"
      onRefresh={() => qc.refetchQueries({ queryKey: ["m-incidents"] })}
      right={
        can("editor") && (
          <BarButton label="New incident" onClick={() => setCreating(true)}>
            <PlusIcon strokeWidth={2.2} />
          </BarButton>
        )
      }
    >
      {open.isLoading ? (
        <SkeletonGroup rows={2} />
      ) : (
        <>
          <ListGroup header="Open">
            {incidents.length ? incidents.map((i) => <IncidentRow key={i.id} inc={i} />) : <Row title={<span className="text-[var(--ios-label2)]">No open incidents</span>} />}
          </ListGroup>

          <ListGroup header="Maintenance">
            {maintenance.length ? maintenance.map((i) => <IncidentRow key={i.id} inc={i} />) : <Row title={<span className="text-[var(--ios-label2)]">Nothing scheduled</span>} />}
          </ListGroup>

          {(closed.data?.length ?? 0) > 0 && (
            <ListGroup header="Recently Closed">
              {closed.data!.map((i) => (
                <IncidentRow key={i.id} inc={i} />
              ))}
            </ListGroup>
          )}
        </>
      )}

      <NewIncidentSheet open={creating} onOpenChange={setCreating} />
    </NavScreen>
  );
}

function IncidentRow({ inc }: { inc: Incident }) {
  const when =
    inc.kind === "maintenance" && inc.status === "scheduled" && inc.scheduled_start
      ? `starts ${new Date(inc.scheduled_start).toLocaleString("en", { weekday: "short", hour: "2-digit", minute: "2-digit" })}`
      : timeAgo(inc.resolved_at ?? inc.updated_at);
  return (
    <Row
      to={`/m/incidents/${inc.id}`}
      leading={<span className="size-2.5 rounded-full" style={{ background: incidentColor(inc) }} />}
      leadingKind="dot"
      title={inc.title}
      subtitle={`${incidentStatusLabel[inc.status]} · ${when}`}
    />
  );
}
