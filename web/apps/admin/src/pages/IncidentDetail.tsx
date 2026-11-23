import { useState } from "react";
import { Link, useNavigate, useParams } from "react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { ChevronLeftIcon, Loader2Icon, PencilIcon, Trash2Icon } from "lucide-react";
import { cn, formatDateTime, timeAgo } from "@drix/ui/lib/utils";
import { Button } from "@drix/ui/components/button";
import { Badge, Card, CardContent, CardHeader, CardTitle } from "@drix/ui/components/display";
import { Field, Textarea } from "@drix/ui/components/form";
import { ConfirmDialog } from "@drix/ui/components/overlay";
import { StatusDot } from "@drix/ui/drix/status";
import { api, ApiError, type Incident, type IncidentStatus } from "../api";
import { useMonitors } from "../lib/queries";
import { statusPageUrl } from "../components/Shell";
import { useCan } from "../lib/roles";
import { impactDot, impacts, statusesFor, statusHint, statusLabel, windowLabel } from "../lib/incidents";
import { PageSkeleton } from "../components/Page";
import { IncidentSheet } from "../components/IncidentForm";

export default function IncidentDetailPage() {
  const id = Number(useParams().id);
  const qc = useQueryClient();
  const navigate = useNavigate();
  const can = useCan();
  const { data: monitors } = useMonitors();
  const [editing, setEditing] = useState(false);
  const [confirm, setConfirm] = useState(false);
  const { data: inc, isLoading } = useQuery({ queryKey: ["incident", id], queryFn: () => api.get<Incident>(`/incidents/${id}`), refetchInterval: 15_000 });

  const remove = useMutation({
    mutationFn: () => api.del(`/incidents/${id}`),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["incidents"] });
      toast.success("Deleted");
      navigate(inc?.kind === "maintenance" ? "/maintenance" : "/incidents");
    },
  });

  if (isLoading) return <PageSkeleton />;
  if (!inc) {
    return (
      <div className="py-20 text-center text-sm text-muted-foreground">
        Not found. <Link to="/incidents" className="font-medium text-foreground underline">Back to incidents</Link>
      </div>
    );
  }
  const isMaint = inc.kind === "maintenance";
  const closed = !!inc.resolved_at;
  const back = isMaint ? "/maintenance" : "/incidents";

  return (
    <>
      <Link to={back} className="mb-4 inline-flex items-center gap-1 text-[13px] text-muted-foreground hover:text-foreground">
        <ChevronLeftIcon className="size-4" /> {isMaint ? "Maintenance" : "Incidents"}
      </Link>
      <div className="mb-6 flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
            <span className="flex items-center gap-1.5">
              <span className={cn("size-2 rounded-full", impactDot(inc.kind, inc.impact))} />
              {isMaint ? "Maintenance" : `${impacts.find((i) => i.value === inc.impact)?.label} impact`}
            </span>
            <span>·</span>
            <span>{isMaint ? windowLabel(inc.scheduled_start, inc.scheduled_end) : `Started ${formatDateTime(inc.created_at)}`}</span>
          </div>
          <div className="mt-1 flex flex-wrap items-center gap-2">
            <h1 className="text-lg font-semibold tracking-tight">{inc.title}</h1>
            <Badge variant={closed ? "outline" : "default"}>{statusLabel[inc.status]}</Badge>
            {inc.auto && <Badge variant="secondary">Detected automatically</Badge>}
          </div>
          {inc.auto && !closed && (
            <p className="mt-1.5 text-xs text-muted-foreground">
              Drix posted this because a public monitor was down, and resolves it when everything has been back for 2 minutes. Post an update or change the status to take over.
            </p>
          )}
        </div>
        <div className="flex gap-2">
          {can("editor") && (
            <Button variant="outline" onClick={() => setEditing(true)}>
              <PencilIcon /> Edit
            </Button>
          )}
          {can("admin") && (
            <Button variant="ghost" size="icon" onClick={() => setConfirm(true)} aria-label="Delete" className="text-muted-foreground hover:text-destructive">
              <Trash2Icon />
            </Button>
          )}
        </div>
      </div>

      <div className="grid gap-6 lg:grid-cols-[1fr_18rem]">
        <div className="grid min-w-0 content-start gap-6">
          {can("editor") && <PostUpdate incident={inc} />}
          <Card>
            <CardHeader>
              <CardTitle>Timeline</CardTitle>
            </CardHeader>
            <CardContent>
              <ol className="relative grid gap-5 border-l pl-5">
                {inc.updates.map((u, i) => (
                  <li key={u.id} className="relative">
                    <span className={cn("absolute top-1.5 -left-[25px] size-2.5 rounded-full ring-4 ring-card", i === 0 ? impactDot(inc.kind, inc.impact) : "bg-border")} />
                    <p className="text-[13px]">
                      <span className="font-semibold">{statusLabel[u.status]}</span>
                      <span className="text-muted-foreground">
                        {" "}
                        · {formatDateTime(u.created_at)} · {u.author || "Automatic"}
                      </span>
                    </p>
                    <p className="mt-1 text-[13.5px] leading-relaxed whitespace-pre-line">{u.body}</p>
                  </li>
                ))}
              </ol>
            </CardContent>
          </Card>
        </div>
        <div className="grid content-start gap-6">
          <Card>
            <CardHeader>
              <CardTitle>Affected monitors</CardTitle>
            </CardHeader>
            <CardContent className="grid gap-2">
              {inc.monitors.length === 0 && <p className="text-[13px] text-muted-foreground">None selected.</p>}
              {inc.monitors.map((m) => {
                const mon = monitors?.find((x) => x.id === m.monitor_id);
                return (
                  <Link key={m.monitor_id} to={`/monitors/${m.monitor_id}`} className="flex items-center gap-2 text-[13px] hover:underline">
                    <StatusDot status={mon?.live.status ?? "pending"} size="sm" />
                    <span className="truncate">{mon?.name ?? `#${m.monitor_id}`}</span>
                    {!isMaint && <span className="ml-auto text-[11px] text-muted-foreground">shown {m.impact}</span>}
                  </Link>
                );
              })}
            </CardContent>
          </Card>
          <Card>
            <CardHeader>
              <CardTitle>Status page</CardTitle>
            </CardHeader>
            <CardContent className="grid gap-1.5 text-[13px]">
              <a href={`${statusPageUrl.replace(/\/$/, "")}/incidents/${inc.id}`} target="_blank" rel="noreferrer" className="truncate hover:underline">
                View on status page ↗
              </a>
              <p className="mt-1 text-xs text-muted-foreground">Subscribers {inc.notify_subscribers ? "are" : "aren't"} notified.</p>
            </CardContent>
          </Card>
        </div>
      </div>

      <IncidentSheet open={editing} onOpenChange={setEditing} kind={inc.kind} incident={inc} />
      <ConfirmDialog
        open={confirm}
        onOpenChange={setConfirm}
        title="Delete this permanently?"
        description="It disappears from every status page, including the history. To close it normally, post a resolved update instead."
        confirmLabel="Delete"
        destructive
        onConfirm={() => remove.mutate()}
      />
    </>
  );
}

function PostUpdate({ incident: inc }: { incident: Incident }) {
  const qc = useQueryClient();
  const options = statusesFor(inc.kind);
  const [status, setStatus] = useState<IncidentStatus>(inc.resolved_at ? inc.status : nextStatus(inc));
  const [message, setMessage] = useState("");
  const [error, setError] = useState<string | null>(null);
  const post = useMutation({
    mutationFn: () => api.post<Incident>(`/incidents/${inc.id}/updates`, { status, message }),
    onSuccess: (updated) => {
      qc.setQueryData(["incident", inc.id], updated);
      qc.invalidateQueries({ queryKey: ["incidents"] });
      qc.invalidateQueries({ queryKey: ["overview"] });
      setMessage("");
      setError(null);
      setStatus(nextStatus(updated));
      toast.success("Update posted");
    },
    onError: (e) => setError(e instanceof ApiError ? e.message : "Could not post"),
  });
  return (
    <Card>
      <CardHeader>
        <CardTitle>Post an update</CardTitle>
      </CardHeader>
      <CardContent>
        <form
          className="grid gap-3"
          onSubmit={(e) => {
            e.preventDefault();
            post.mutate();
          }}
        >
          <div className="flex flex-wrap gap-1.5">
            {options.map((s) => (
              <button
                key={s}
                type="button"
                onClick={() => setStatus(s)}
                className={cn("h-7 rounded-md border px-2.5 text-xs font-medium transition-colors hover:bg-muted", status === s && "border-foreground/60 bg-muted")}
              >
                {statusLabel[s]}
              </button>
            ))}
          </div>
          {statusHint[status] && <p className="-mt-1 text-xs text-muted-foreground">{statusHint[status]}</p>}
          <Field error={error ?? undefined}>
            <Textarea rows={3} value={message} onChange={(e) => setMessage(e.target.value)} placeholder="What's new? Visible to everyone." />
          </Field>
          <div className="flex items-center justify-between gap-2">
            <span className="text-xs text-muted-foreground">Last update {timeAgo(inc.updated_at)}</span>
            <Button type="submit" disabled={post.isPending || !message.trim()}>
              {post.isPending && <Loader2Icon className="animate-spin" />}
              Post update
            </Button>
          </div>
        </form>
      </CardContent>
    </Card>
  );
}

function nextStatus(inc: Incident): IncidentStatus {
  const order = statusesFor(inc.kind);
  const i = order.indexOf(inc.status);
  if (inc.kind === "maintenance") return inc.status === "scheduled" ? "in_progress" : "completed";
  return order[Math.min(i + 1, order.length - 1)];
}
