import { useMemo, useState } from "react";
import { Link, useNavigate, useSearchParams } from "react-router";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { DownloadIcon, EyeOffIcon, FileUpIcon, MoreHorizontalIcon, PauseIcon, PencilIcon, PlayIcon, PlusIcon, RadarIcon, SearchIcon, Trash2Icon, XIcon } from "lucide-react";
import { cn, formatLatency, formatUptime, statusLabel, timeAgo, type Status } from "@drix/ui/lib/utils";
import { Button } from "@drix/ui/components/button";
import { Card, EmptyState, Skeleton, Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@drix/ui/components/display";
import { Checkbox, Input } from "@drix/ui/components/form";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@drix/ui/components/menu";
import { ConfirmDialog, Tooltip, TooltipContent, TooltipTrigger } from "@drix/ui/components/overlay";
import { MonitorTypeIcon, StatusPill, monitorTypeLabel } from "@drix/ui/drix/status";
import { Sparkline } from "@drix/ui/drix/sparkline";
import { api, type Monitor } from "../api";
import { keys, useGroups, useMonitors } from "../lib/queries";
import { PageHeader } from "../components/Page";
import { useCan } from "../lib/roles";
import { MonitorSheet } from "../components/MonitorForm";
import { ImportMonitorsDialog } from "../components/ImportMonitors";

const statusOrder: Record<Status, number> = { down: 0, degraded: 1, maintenance: 2, pending: 3, up: 4, paused: 5 };

export default function MonitorsPage() {
  const { data: monitors, isLoading } = useMonitors();
  const { data: groupsData } = useGroups();
  const groups = groupsData?.groups;
  const [params, setParams] = useSearchParams();
  const navigate = useNavigate();
  const qc = useQueryClient();
  const canEdit = useCan()("editor");

  const [q, setQ] = useState("");
  const [status, setStatus] = useState<string>("all");
  const [page, setPage] = useState<string>("all");
  const [selected, setSelected] = useState<Set<number>>(new Set());
  const [importing, setImporting] = useState(false);
  const [editing, setEditing] = useState<Monitor | null>(null);
  const [confirmDelete, setConfirmDelete] = useState<number[] | null>(null);

  const sheetOpen = params.get("new") === "1" || editing !== null;
  const closeSheet = (o: boolean) => {
    if (o) return;
    setEditing(null);
    if (params.has("new")) {
      params.delete("new");
      setParams(params, { replace: true });
    }
  };


  const rows = useMemo(() => {
    const term = q.trim().toLowerCase();
    return (monitors ?? [])
      .filter((m) => (status === "all" ? true : m.live.status === status))
      .filter((m) => (page === "all" ? true : page === "none" ? m.groups.length === 0 : m.groups.some((g) => String(g.id) === page)))
      .filter((m) => !term || m.name.toLowerCase().includes(term) || m.target.toLowerCase().includes(term))
      .sort((a, b) => statusOrder[a.live.status] - statusOrder[b.live.status] || a.name.localeCompare(b.name));
  }, [monitors, q, status, page]);

  const counts = useMemo(() => {
    const c: Record<string, number> = { all: monitors?.length ?? 0, up: 0, degraded: 0, down: 0, pending: 0, paused: 0 };
    monitors?.forEach((m) => c[m.live.status]++);
    return c;
  }, [monitors]);

  const bulk = useMutation({
    mutationFn: ({ action, ids }: { action: "pause" | "resume" | "delete"; ids: number[] }) => api.post("/monitors/bulk", { action, ids }),
    onSuccess: (_, v) => {
      toast.success(`${v.ids.length} monitor${v.ids.length === 1 ? "" : "s"} ${v.action === "delete" ? "deleted" : v.action === "pause" ? "paused" : "resumed"}`);
      setSelected(new Set());
      qc.invalidateQueries({ queryKey: keys.monitors });
      qc.invalidateQueries({ queryKey: keys.overview });
    },
    onError: (e) => toast.error(e.message),
  });

  const allChecked = rows.length > 0 && rows.every((r) => selected.has(r.id));
  const someChecked = rows.some((r) => selected.has(r.id));
  const toggleAll = () => setSelected(allChecked ? new Set() : new Set(rows.map((r) => r.id)));
  const toggle = (id: number) =>
    setSelected((s) => {
      const n = new Set(s);
      if (n.has(id)) n.delete(id);
      else n.add(id);
      return n;
    });

  return (
    <>
      <PageHeader
        title="Monitors"
        description="Everything the worker cluster checks, refreshed live."
        actions={
          <>
            <Button asChild variant="outline">
              <a href="/api/admin/monitors.csv" download>
                <DownloadIcon /> Export CSV
              </a>
            </Button>
            {canEdit && (
              <>
                <Button variant="outline" onClick={() => setImporting(true)}>
                  <FileUpIcon /> Import CSV
                </Button>
                <Button onClick={() => setParams({ new: "1" })}>
                  <PlusIcon /> New monitor
                </Button>
              </>
            )}
          </>
        }
      />
      <ImportMonitorsDialog open={importing} onOpenChange={setImporting} />

      <div className="mb-3 flex flex-wrap items-center gap-2">
        <div className="relative w-full sm:w-64">
          <SearchIcon className="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input value={q} onChange={(e) => setQ(e.target.value)} placeholder="Search name or target" className="pl-8" />
        </div>
        <div className="flex items-center gap-1 rounded-lg bg-muted p-0.5">
          {(["all", "down", "degraded", "up", "paused"] as const).map((s) => (
            <button
              key={s}
              onClick={() => setStatus(s)}
              className={cn(
                "tabular flex h-7 items-center gap-1.5 rounded-md px-2.5 text-xs font-medium text-muted-foreground transition-colors",
                status === s && "bg-background text-foreground shadow-xs dark:bg-input/40",
              )}
            >
              {s === "all" ? "All" : statusLabel[s]}
              <span className="text-muted-foreground">{counts[s]}</span>
            </button>
          ))}
        </div>
        <Select value={page} onValueChange={setPage}>
          <SelectTrigger className="w-44">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">All groups</SelectItem>
            <SelectItem value="none">No group (private)</SelectItem>
            {groups?.map((p) => (
              <SelectItem key={p.id} value={String(p.id)}>
                {p.name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>

      {selected.size > 0 && (
        <div className="mb-3 flex items-center gap-2 rounded-lg border bg-muted/40 px-3 py-1.5 text-[13px]">
          <span className="tabular font-medium">{selected.size} selected</span>
          <div className="ml-auto flex gap-1">
            <Button size="sm" variant="ghost" onClick={() => bulk.mutate({ action: "pause", ids: [...selected] })}>
              <PauseIcon /> Pause
            </Button>
            <Button size="sm" variant="ghost" onClick={() => bulk.mutate({ action: "resume", ids: [...selected] })}>
              <PlayIcon /> Resume
            </Button>
            <Button size="sm" variant="ghost" className="text-destructive hover:text-destructive" onClick={() => setConfirmDelete([...selected])}>
              <Trash2Icon /> Delete
            </Button>
            <Button size="icon-sm" variant="ghost" onClick={() => setSelected(new Set())} aria-label="Clear selection">
              <XIcon />
            </Button>
          </div>
        </div>
      )}

      <Card className="overflow-hidden">
        {isLoading ? (
          <div className="grid gap-3 p-4">
            {Array.from({ length: 6 }, (_, i) => (
              <Skeleton key={i} className="h-9" />
            ))}
          </div>
        ) : !monitors?.length ? (
          <EmptyState
            icon={<RadarIcon />}
            title="No monitors yet"
            description="Add an HTTP endpoint, a TCP port, a host to ping or a DNS record. Workers start checking it within seconds."
            action={
              canEdit && (
                <Button onClick={() => setParams({ new: "1" })}>
                  <PlusIcon /> New monitor
                </Button>
              )
            }
          />
        ) : rows.length === 0 ? (
          <EmptyState icon={<SearchIcon />} title="No matches" description="Try a different search or filter." />
        ) : (
          <Table>
            <TableHeader>
              <TableRow className="hover:bg-transparent">
                <TableHead className="w-10 pl-4">
                  {canEdit && (
                    <Checkbox checked={allChecked ? true : someChecked ? "indeterminate" : false} onCheckedChange={toggleAll} aria-label="Select all" />
                  )}
                </TableHead>
                <TableHead>Monitor</TableHead>
                <TableHead>Status</TableHead>
                <TableHead className="hidden md:table-cell">Response</TableHead>
                <TableHead className="hidden text-right lg:table-cell">Uptime 30d</TableHead>
                <TableHead className="hidden xl:table-cell">Groups</TableHead>
                <TableHead className="hidden lg:table-cell">Worker</TableHead>
                <TableHead className="w-10" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((m) => (
                <TableRow
                  key={m.id}
                  data-state={selected.has(m.id) ? "selected" : undefined}
                  className="cursor-pointer"
                  onClick={(e) => {
                    if ((e.target as HTMLElement).closest("button,[role=checkbox],[role=menuitem],a")) return;
                    navigate(`/monitors/${m.id}`);
                  }}
                >
                  <TableCell className="pl-4">
                    {canEdit && <Checkbox checked={selected.has(m.id)} onCheckedChange={() => toggle(m.id)} aria-label={`Select ${m.name}`} />}
                  </TableCell>
                  <TableCell className="max-w-[18rem]">
                    <div className="flex items-center gap-2.5">
                      <Tooltip>
                        <TooltipTrigger asChild>
                          <span className="flex size-7 shrink-0 items-center justify-center rounded-md border bg-muted/40">
                            <MonitorTypeIcon type={m.type} className="size-3.5" />
                          </span>
                        </TooltipTrigger>
                        <TooltipContent>
                          {monitorTypeLabel(m.type)} · every {m.interval_s}s
                        </TooltipContent>
                      </Tooltip>
                      <div className="min-w-0">
                        <Link to={`/monitors/${m.id}`} className="flex items-center gap-1.5 truncate font-medium hover:underline">
                          {m.name}
                          {m.groups.length === 0 && <EyeOffIcon className="size-3 shrink-0 text-muted-foreground" aria-label="Not on the status page" />}
                        </Link>
                        <p className="truncate font-mono text-[11.5px] text-muted-foreground">{m.target}</p>
                      </div>
                    </div>
                  </TableCell>
                  <TableCell>
                    <Tooltip>
                      <TooltipTrigger asChild>
                        <span>
                          <StatusPill status={m.live.status} />
                        </span>
                      </TooltipTrigger>
                      <TooltipContent className="max-w-sm font-mono text-[11px]">
                        {m.live.message || "No message"}
                        {m.live.last_checked && <span className="block opacity-70">checked {timeAgo(m.live.last_checked)}</span>}
                      </TooltipContent>
                    </Tooltip>
                  </TableCell>
                  <TableCell className="hidden md:table-cell">
                    <div className="flex items-center gap-3">
                      <Sparkline values={m.spark ?? []} width={80} height={22} />
                      <span className="tabular w-14 text-xs text-muted-foreground">{m.live.status === "down" ? "—" : formatLatency(m.live.latency_ms)}</span>
                    </div>
                  </TableCell>
                  <TableCell className="tabular hidden text-right text-xs lg:table-cell">{formatUptime(m.uptime_30)}</TableCell>
                  <TableCell className="hidden max-w-40 truncate text-xs text-muted-foreground xl:table-cell">
                    {m.groups.length ? m.groups.map((g) => g.name).join(", ") : "Private"}
                  </TableCell>
                  <TableCell className="hidden text-xs text-muted-foreground lg:table-cell">{m.owner || "—"}</TableCell>
                  <TableCell>
                    {canEdit && <RowMenu
                      monitor={m}
                      onEdit={() => setEditing(m)}
                      onToggle={() => bulk.mutate({ action: m.enabled ? "pause" : "resume", ids: [m.id] })}
                      onDelete={() => setConfirmDelete([m.id])}
                    />}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </Card>

      <MonitorSheet
        open={sheetOpen}
        onOpenChange={closeSheet}
        monitor={editing}
        defaultSectionId={page !== "all" && page !== "none" ? Number(page) : null}
      />
      <ConfirmDialog
        open={!!confirmDelete}
        onOpenChange={(o) => !o && setConfirmDelete(null)}
        title={`Delete ${confirmDelete?.length === 1 ? "this monitor" : `${confirmDelete?.length} monitors`}?`}
        description="Check history and uptime data are removed permanently."
        confirmLabel="Delete"
        destructive
        onConfirm={() => confirmDelete && bulk.mutate({ action: "delete", ids: confirmDelete })}
      />
    </>
  );
}

function RowMenu({ monitor, onEdit, onToggle, onDelete }: { monitor: Monitor; onEdit: () => void; onToggle: () => void; onDelete: () => void }) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" size="icon-sm" aria-label="Actions">
          <MoreHorizontalIcon />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <DropdownMenuItem onSelect={onEdit}>
          <PencilIcon /> Edit
        </DropdownMenuItem>
        <DropdownMenuItem onSelect={onToggle}>
          {monitor.enabled ? <PauseIcon /> : <PlayIcon />} {monitor.enabled ? "Pause" : "Resume"}
        </DropdownMenuItem>
        <DropdownMenuSeparator />
        <DropdownMenuItem variant="destructive" onSelect={onDelete}>
          <Trash2Icon /> Delete
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
