import { useState } from "react";
import { Link } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { ActivityIcon } from "lucide-react";
import { formatDateTime, statusLabel, timeAgo } from "@drix/ui/lib/utils";
import { Card, EmptyState, Skeleton, Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@drix/ui/components/display";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@drix/ui/components/menu";
import { StatusPill } from "@drix/ui/drix/status";
import { api, type Event } from "../api";
import { keys, useMonitors } from "../lib/queries";
import { PageHeader } from "../components/Page";

export default function EventsPage() {
  const [monitor, setMonitor] = useState("all");
  const { data: monitors } = useMonitors();
  const id = monitor === "all" ? undefined : Number(monitor);
  const { data, isLoading } = useQuery({
    queryKey: keys.events(id),
    queryFn: () => api.get<Event[]>(`/events?limit=200${id ? `&monitor_id=${id}` : ""}`),
    refetchInterval: 30_000,
  });

  return (
    <>
      <PageHeader
        title="Events"
        description="Every status change, newest first."
        actions={
          <Select value={monitor} onValueChange={setMonitor}>
            <SelectTrigger className="w-56">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">All monitors</SelectItem>
              {monitors?.map((m) => (
                <SelectItem key={m.id} value={String(m.id)}>
                  {m.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        }
      />
      <Card className="overflow-hidden">
        {isLoading ? (
          <div className="grid gap-3 p-4">
            {[0, 1, 2, 3].map((i) => (
              <Skeleton key={i} className="h-9" />
            ))}
          </div>
        ) : !data?.length ? (
          <EmptyState icon={<ActivityIcon />} title="No events yet" description="Status changes such as a monitor going down or recovering show up here." />
        ) : (
          <Table>
            <TableHeader>
              <TableRow className="hover:bg-transparent">
                <TableHead className="pl-4">When</TableHead>
                <TableHead>Monitor</TableHead>
                <TableHead>Change</TableHead>
                <TableHead className="hidden md:table-cell">Details</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {data.map((e) => (
                <TableRow key={e.id}>
                  <TableCell className="pl-4">
                    <p className="tabular text-xs">{formatDateTime(e.created_at)}</p>
                    <p className="text-[11px] text-muted-foreground">{timeAgo(e.created_at)}</p>
                  </TableCell>
                  <TableCell>
                    <Link to={`/monitors/${e.monitor_id}`} className="font-medium hover:underline">
                      {e.monitor_name}
                    </Link>
                  </TableCell>
                  <TableCell>
                    <div className="flex items-center gap-1.5">
                      <StatusPill status={e.from_status} label={statusLabel[e.from_status] ?? e.from_status} />
                      <span className="text-muted-foreground">→</span>
                      <StatusPill status={e.to_status} />
                    </div>
                  </TableCell>
                  <TableCell className="hidden max-w-md truncate font-mono text-[11.5px] text-muted-foreground md:table-cell">{e.message}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </Card>
    </>
  );
}
