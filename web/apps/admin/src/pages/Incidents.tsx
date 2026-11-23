import { useState } from "react";
import { Link, useSearchParams } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { CalendarClockIcon, PlusIcon, SirenIcon } from "lucide-react";
import { cn, formatDateTime, timeAgo } from "@drix/ui/lib/utils";
import { Button } from "@drix/ui/components/button";
import { Badge, Card, EmptyState, Skeleton } from "@drix/ui/components/display";
import { Tabs, TabsList, TabsTrigger } from "@drix/ui/components/menu";
import { api, type Incident, type IncidentKind } from "../api";
import { useMonitors } from "../lib/queries";
import { useCan } from "../lib/roles";
import { impactDot, impacts, statusLabel, windowLabel } from "../lib/incidents";
import { PageHeader } from "../components/Page";
import { IncidentSheet } from "../components/IncidentForm";

export function IncidentsList({ kind }: { kind: IncidentKind }) {
  const isMaint = kind === "maintenance";
  const [params, setParams] = useSearchParams();
  const [tab, setTab] = useState<"open" | "closed">("open");
  const canEdit = useCan()("editor");
  const { data: monitors } = useMonitors();
  const { data, isLoading } = useQuery({
    queryKey: ["incidents", kind, tab],
    queryFn: () => api.get<Incident[]>(`/incidents?kind=${kind}&state=${tab}&limit=100`),
    refetchInterval: 15_000,
  });
  const open = params.get("new") === "1";
  const setOpen = (o: boolean) => {
    if (o) setParams({ new: "1" });
    else {
      params.delete("new");
      setParams(params, { replace: true });
    }
  };
  const name = (id: number) => monitors?.find((m) => m.id === id)?.name ?? `#${id}`;
  const list = [...(data ?? [])];
  if (isMaint && tab === "open") list.sort((a, b) => (a.scheduled_start ?? "").localeCompare(b.scheduled_start ?? ""));

  return (
    <>
      <PageHeader
        title={isMaint ? "Maintenance" : "Incidents"}
        description={
          isMaint
            ? "Planned work. Affected monitors show “Maintenance”, alerts pause and downtime doesn't count against uptime."
            : "Tell visitors what's going on. Incidents appear on the status pages you choose."
        }
        actions={
          canEdit && (
            <Button onClick={() => setOpen(true)}>
              <PlusIcon /> {isMaint ? "Schedule maintenance" : "Declare incident"}
            </Button>
          )
        }
      />
      <Tabs value={tab} onValueChange={(v) => setTab(v as "open" | "closed")} className="mb-4">
        <TabsList>
          <TabsTrigger value="open">{isMaint ? "Upcoming & in progress" : "Active"}</TabsTrigger>
          <TabsTrigger value="closed">{isMaint ? "Past" : "Resolved"}</TabsTrigger>
        </TabsList>
      </Tabs>
      {isLoading ? (
        <Skeleton className="h-48 rounded-xl" />
      ) : !list.length ? (
        <Card>
          <EmptyState
            icon={isMaint ? <CalendarClockIcon /> : <SirenIcon />}
            title={tab === "open" ? (isMaint ? "Nothing scheduled" : "No active incidents") : "Nothing here yet"}
            description={tab === "open" ? (isMaint ? "Schedule maintenance to let visitors know in advance." : "When something breaks, declare an incident to keep people informed.") : undefined}
          />
        </Card>
      ) : (
        <Card className="divide-y overflow-hidden">
          {list.map((inc) => {
            const latest = inc.updates[0];
            return (
              <Link key={inc.id} to={`/incidents/${inc.id}`} className="flex items-start gap-3 px-4 py-3.5 transition-colors hover:bg-muted/40">
                <span className={cn("mt-1.5 size-2 shrink-0 rounded-full", impactDot(inc.kind, inc.impact))} />
                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-center gap-2">
                    <p className="truncate text-[13.5px] font-medium">{inc.title}</p>
                    <Badge variant={inc.status === "in_progress" ? "default" : "outline"}>{statusLabel[inc.status]}</Badge>
                    {inc.auto && <Badge variant="secondary">Automatic</Badge>}
                    {!isMaint && <span className="text-[11px] text-muted-foreground">{impacts.find((i) => i.value === inc.impact)?.label} impact</span>}
                  </div>
                  {isMaint && <p className="mt-0.5 text-xs text-muted-foreground">{windowLabel(inc.scheduled_start, inc.scheduled_end)}</p>}
                  {latest && <p className="mt-0.5 line-clamp-1 text-[13px] text-muted-foreground">{latest.body}</p>}
                  {inc.monitors.length > 0 && (
                    <p className="mt-1 truncate text-[11.5px] text-muted-foreground">Affects {inc.monitors.map((m) => name(m.monitor_id)).join(", ")}</p>
                  )}
                </div>
                <time className="shrink-0 text-xs text-muted-foreground" title={formatDateTime(inc.updated_at)}>
                  {timeAgo(inc.updated_at)}
                </time>
              </Link>
            );
          })}
        </Card>
      )}
      <IncidentSheet open={open} onOpenChange={setOpen} kind={kind} />
    </>
  );
}

export default function IncidentsPage() {
  return <IncidentsList kind="incident" />;
}
