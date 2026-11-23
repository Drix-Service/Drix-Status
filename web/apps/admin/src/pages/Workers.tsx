import { useEffect, useState } from "react";
import { useSearchParams } from "react-router";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { CpuIcon, KeyRoundIcon, MoreHorizontalIcon, PauseIcon, PencilIcon, PlayIcon, PlusIcon, Trash2Icon, TriangleAlertIcon } from "lucide-react";
import { cn, formatDuration, timeAgo } from "@drix/ui/lib/utils";
import { Button } from "@drix/ui/components/button";
import { Card, EmptyState, Skeleton } from "@drix/ui/components/display";
import { Field, Input } from "@drix/ui/components/form";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger, Tabs, TabsContent, TabsList, TabsTrigger } from "@drix/ui/components/menu";
import { ConfirmDialog, Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, Tooltip, TooltipContent, TooltipTrigger } from "@drix/ui/components/overlay";
import { StatusPill } from "@drix/ui/drix/status";
import { api, ApiError, type Agent } from "../api";
import { keys, useAgents } from "../lib/queries";
import { CodeBlock, CopyButton, PageHeader } from "../components/Page";
import { useCan } from "../lib/roles";

// Stable, muted palette for distribution segments.
const palette = ["bg-chart-1", "bg-chart-2", "bg-chart-3", "bg-chart-4", "bg-chart-5"];
const swatch = (i: number) => palette[i % palette.length];

export default function WorkersPage() {
  const { data, isLoading } = useAgents();
  const qc = useQueryClient();
  const [params, setParams] = useSearchParams();
  const [editing, setEditing] = useState<Agent | null>(null);
  const [deleting, setDeleting] = useState<Agent | null>(null);
  const [token, setToken] = useState<{ agent: Agent; token: string; rotated?: boolean } | null>(null);

  const addOpen = params.get("new") === "1" || editing !== null;
  const closeAdd = () => {
    setEditing(null);
    if (params.has("new")) {
      params.delete("new");
      setParams(params, { replace: true });
    }
  };

  const update = useMutation({
    mutationFn: (a: Agent & { disabled: boolean }) => api.put(`/agents/${a.id}`, { name: a.name, region: a.region, disabled: a.disabled }),
    onSuccess: (_, a) => {
      toast.success(a.disabled ? `${a.name} disabled` : `${a.name} enabled`);
      qc.invalidateQueries({ queryKey: keys.agents });
    },
  });
  const rotate = useMutation({
    mutationFn: (a: Agent) => api.post<{ token: string }>(`/agents/${a.id}/rotate`),
    onSuccess: (r, a) => {
      setToken({ agent: a, token: r.token, rotated: true });
      qc.invalidateQueries({ queryKey: keys.agents });
    },
  });
  const remove = useMutation({
    mutationFn: (a: Agent) => api.del(`/agents/${a.id}`),
    onSuccess: (_, a) => {
      toast.success(`${a.name} removed`);
      qc.invalidateQueries({ queryKey: keys.agents });
    },
  });

  const canAdmin = useCan()("admin");
  const agents = data?.agents ?? [];
  const online = agents.filter((a) => a.online);
  const total = data?.total_monitors ?? 0;

  return (
    <>
      <PageHeader
        title="Workers"
        description="Workers are small agents that run checks and report back. Monitors are split between online workers automatically, and move to the others if one goes offline."
        actions={
          canAdmin && (
            <Button onClick={() => setParams({ new: "1" })}>
              <PlusIcon /> Add worker
            </Button>
          )
        }
      />

      {isLoading ? (
        <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
          {[0, 1, 2].map((i) => (
            <Skeleton key={i} className="h-44 rounded-xl" />
          ))}
        </div>
      ) : agents.length === 0 ? (
        <Card>
          <EmptyState
            icon={<CpuIcon />}
            title="No workers yet"
            description="Add a worker and run it on any server with network access to your services. Add more workers to spread the load and stay up if one fails."
            action={
              <Button onClick={() => setParams({ new: "1" })}>
                <PlusIcon /> Add worker
              </Button>
            }
          />
        </Card>
      ) : (
        <>
          <Card className="mb-6 p-4">
            <div className="mb-3 flex flex-wrap items-baseline justify-between gap-2">
              <p className="text-[13px] font-medium">Load distribution</p>
              <p className="tabular text-xs text-muted-foreground">
                {total} active monitors across {online.length} online {online.length === 1 ? "worker" : "workers"}
              </p>
            </div>
            {online.length === 0 ? (
              <div className="flex items-center gap-2 rounded-md bg-down/[0.07] px-3 py-2 text-[13px] text-down">
                <TriangleAlertIcon className="size-4" /> No workers are online, so no checks are running.
              </div>
            ) : (
              <>
                <div className="flex h-2.5 gap-0.5 overflow-hidden rounded-full">
                  {online.map((a) => (
                    <Tooltip key={a.id}>
                      <TooltipTrigger asChild>
                        <div className={cn("h-full transition-[width] duration-500", swatch(agents.indexOf(a)))} style={{ width: `${total ? (a.owned / total) * 100 : 100 / online.length}%` }} />
                      </TooltipTrigger>
                      <TooltipContent>
                        {a.name}: {a.owned} monitors
                      </TooltipContent>
                    </Tooltip>
                  ))}
                </div>
                <div className="mt-3 flex flex-wrap gap-x-5 gap-y-1.5">
                  {online.map((a) => (
                    <span key={a.id} className="flex items-center gap-1.5 text-xs text-muted-foreground">
                      <span className={cn("size-2 rounded-[2px]", swatch(agents.indexOf(a)))} />
                      {a.name}
                      <span className="tabular text-foreground">{total ? Math.round((a.owned / total) * 100) : 0}%</span>
                    </span>
                  ))}
                </div>
              </>
            )}
          </Card>

          <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
            {agents.map((a) => (
              <WorkerCard
                key={a.id}
                agent={a}
                color={swatch(agents.indexOf(a))}
                total={total}
                canManage={canAdmin}
                onEdit={() => setEditing(a)}
                onToggle={() => update.mutate({ ...a, disabled: !a.disabled })}
                onRotate={() => rotate.mutate(a)}
                onDelete={() => setDeleting(a)}
              />
            ))}
          </div>
        </>
      )}

      <WorkerDialog
        open={addOpen}
        agent={editing}
        onClose={closeAdd}
        onCreated={(agent, tok) => {
          closeAdd();
          setToken({ agent, token: tok });
        }}
      />
      <TokenDialog data={token} publicUrl={data?.public_url ?? location.origin} onClose={() => setToken(null)} />
      <ConfirmDialog
        open={!!deleting}
        onOpenChange={(o) => !o && setDeleting(null)}
        title={`Remove ${deleting?.name}?`}
        description="The worker is disconnected right away and its token stops working. Its monitors move to the other workers."
        confirmLabel="Remove worker"
        destructive
        onConfirm={() => deleting && remove.mutate(deleting)}
      />
    </>
  );
}

function WorkerCard({
  agent: a,
  color,
  total,
  canManage,
  onEdit,
  onToggle,
  onRotate,
  onDelete,
}: {
  agent: Agent;
  color: string;
  total: number;
  canManage: boolean;
  onEdit: () => void;
  onToggle: () => void;
  onRotate: () => void;
  onDelete: () => void;
}) {
  const status = a.disabled ? "paused" : a.online ? "up" : "down";
  const label = a.disabled ? "Disabled" : a.online ? "Online" : "Offline";
  return (
    <Card className={cn("flex flex-col p-4", !a.online && "bg-muted/20")}>
      <div className="flex items-start gap-3">
        <span className={cn("mt-1 h-8 w-1 shrink-0 rounded-full", a.online ? color : "bg-border")} />
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <p className="truncate text-[14px] font-semibold tracking-tight">{a.name}</p>
            <StatusPill status={status} label={label} />
          </div>
          <p className="truncate text-xs text-muted-foreground">
            {[a.region, a.live?.hostname || a.last_hostname].filter(Boolean).join(" · ") || "Not connected yet"}
          </p>
        </div>
        {canManage && <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button variant="ghost" size="icon-sm" aria-label="Worker actions">
              <MoreHorizontalIcon />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="min-w-44">
            <DropdownMenuItem onSelect={onEdit}>
              <PencilIcon /> Rename
            </DropdownMenuItem>
            <DropdownMenuItem onSelect={onToggle}>
              {a.disabled ? <PlayIcon /> : <PauseIcon />} {a.disabled ? "Enable" : "Disable"}
            </DropdownMenuItem>
            <DropdownMenuItem onSelect={onRotate}>
              <KeyRoundIcon /> New token
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem variant="destructive" onSelect={onDelete}>
              <Trash2Icon /> Remove
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>}
      </div>

      <dl className="mt-4 grid grid-cols-3 gap-3 border-t pt-3 text-xs">
        <Metric label="Monitors" value={a.online ? String(a.owned) : "—"} sub={a.online && total ? `${Math.round((a.owned / total) * 100)}% of load` : undefined} />
        <Metric label="In flight" value={a.live ? String(a.live.in_flight) : "—"} sub={a.live?.buffered ? `${a.live.buffered} buffered` : undefined} />
        <Metric
          label={a.online ? "Connected" : "Last seen"}
          value={a.online && a.live ? formatDuration(a.live.connected_at) : a.last_seen_at ? timeAgo(a.last_seen_at) : "never"}
        />
      </dl>
      <div className="mt-3 flex items-center justify-between font-mono text-[11px] text-muted-foreground">
        <span className="truncate">{a.live ? `${a.live.os}/${a.live.arch} · ${fmtVersion(a.live.version)}` : a.last_version ? fmtVersion(a.last_version) : ""}</span>
        <span className="truncate">{a.token_prefix}…</span>
      </div>
    </Card>
  );
}

function Metric({ label, value, sub }: { label: string; value: string; sub?: string }) {
  return (
    <div className="min-w-0">
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="tabular mt-0.5 truncate text-[13px] font-medium">{value}</dd>
      {sub && <dd className="truncate text-[11px] text-muted-foreground">{sub}</dd>}
    </div>
  );
}

function WorkerDialog({
  open,
  agent,
  onClose,
  onCreated,
}: {
  open: boolean;
  agent: Agent | null;
  onClose: () => void;
  onCreated: (a: Agent, token: string) => void;
}) {
  const qc = useQueryClient();
  const [name, setName] = useState("");
  const [region, setRegion] = useState("");
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (open) {
      setName(agent?.name ?? "");
      setRegion(agent?.region ?? "");
      setError(null);
    }
  }, [open, agent]);

  const save = useMutation({
    mutationFn: async () => {
      if (agent) {
        await api.put(`/agents/${agent.id}`, { name, region, disabled: agent.disabled });
        return null;
      }
      return api.post<{ agent: Agent; token: string }>("/agents", { name, region });
    },
    onSuccess: (r) => {
      qc.invalidateQueries({ queryKey: keys.agents });
      if (r) onCreated(r.agent, r.token);
      else {
        toast.success("Worker updated");
        onClose();
      }
    },
    onError: (e) => setError(e instanceof ApiError ? e.message : "Could not save"),
  });

  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <form
          className="grid gap-4"
          onSubmit={(e) => {
            e.preventDefault();
            save.mutate();
          }}
        >
          <DialogHeader>
            <DialogTitle>{agent ? "Rename worker" : "Add worker"}</DialogTitle>
            <DialogDescription>
              {agent ? "Change how this worker is labeled." : "You'll get a token and a one-line command to start the worker."}
            </DialogDescription>
          </DialogHeader>
          <Field label="Name" htmlFor="w-name" error={error ?? undefined}>
            <Input id="w-name" autoFocus value={name} onChange={(e) => setName(e.target.value)} placeholder="fra-1" />
          </Field>
          <Field label="Region" htmlFor="w-region" hint="Optional label, e.g. eu-central.">
            <Input id="w-region" value={region} onChange={(e) => setRegion(e.target.value)} placeholder="eu-central" />
          </Field>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" disabled={save.isPending || !name.trim()}>
              {agent ? "Save" : "Create worker"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

const INSTALL_SCRIPT_URL = "https://raw.githubusercontent.com/24-ghost-the-killer/Drix-Status/main/install.sh";

function TokenDialog({ data, publicUrl, onClose }: { data: { agent: Agent; token: string; rotated?: boolean } | null; publicUrl: string; onClose: () => void }) {
  if (!data) return null;
  const script = `curl -fsSL ${INSTALL_SCRIPT_URL} | sudo bash -s -- worker \\
  --server ${publicUrl} \\
  --token ${data.token}`;
  const docker = `docker run -d --name drix-agent --restart unless-stopped \\
  --cap-add NET_RAW \\
  -e DRIX_SERVER_URL=${publicUrl} \\
  -e DRIX_AGENT_TOKEN=${data.token} \\
  drix/agent:latest`;
  const binary = `DRIX_SERVER_URL=${publicUrl} \\
DRIX_AGENT_TOKEN=${data.token} \\
./drix-agent`;
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>{data.rotated ? `New token for ${data.agent.name}` : `Start ${data.agent.name}`}</DialogTitle>
          <DialogDescription>
            Copy the token now. It won't be shown again.{data.rotated && " The old token has stopped working."}
          </DialogDescription>
        </DialogHeader>
        <div className="flex items-center gap-2 rounded-lg border bg-muted/50 py-1 pr-1 pl-3">
          <code className="flex-1 truncate font-mono text-[12.5px]">{data.token}</code>
          <CopyButton value={data.token} label="Copy" />
        </div>
        <Tabs defaultValue="script" className="grid gap-3">
          <TabsList className="w-fit">
            <TabsTrigger value="script">Install script</TabsTrigger>
            <TabsTrigger value="docker">Docker</TabsTrigger>
            <TabsTrigger value="binary">Binary</TabsTrigger>
          </TabsList>
          <TabsContent value="script" className="grid gap-2">
            <CodeBlock code={script} />
            <p className="text-xs text-muted-foreground">Run on a Linux server. Installs Docker if needed, then starts the worker so it restarts on boot.</p>
          </TabsContent>
          <TabsContent value="docker">
            <CodeBlock code={docker} />
          </TabsContent>
          <TabsContent value="binary">
            <CodeBlock code={binary} />
          </TabsContent>
        </Tabs>
        <p className="text-xs text-muted-foreground">The worker connects out to the server, so it needs no open ports. It shows up here as soon as it connects.</p>
        <DialogFooter>
          <Button onClick={onClose}>Done</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

const fmtVersion = (v: string) => (/^\d/.test(v) ? `v${v}` : v);
