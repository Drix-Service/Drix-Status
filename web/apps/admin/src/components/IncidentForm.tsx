import { useEffect, useState } from "react";
import { useNavigate } from "react-router";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { Loader2Icon } from "lucide-react";
import { cn } from "@drix/ui/lib/utils";
import { Button } from "@drix/ui/components/button";
import { Field, Input, Label, Switch, Textarea } from "@drix/ui/components/form";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@drix/ui/components/menu";
import { Sheet, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "@drix/ui/components/overlay";
import { api, ApiError, type Impact, type Incident, type IncidentKind, type IncidentStatus } from "../api";
import { useMonitors } from "../lib/queries";
import { fromLocalInput, impacts, incidentStatuses, statusHint, statusLabel, toLocalInput } from "../lib/incidents";
import { MonitorPicker } from "./MonitorPicker";

type MonitorImpact = "degraded" | "down";

function defaultWindow() {
  const s = new Date(Date.now() + 24 * 3600_000);
  s.setMinutes(0, 0, 0);
  const e = new Date(s.getTime() + 3600_000);
  return { start: toLocalInput(s.toISOString()), end: toLocalInput(e.toISOString()) };
}

export function IncidentSheet({
  open,
  onOpenChange,
  kind,
  incident,
}: {
  open: boolean;
  onOpenChange: (o: boolean) => void;
  kind: IncidentKind;
  incident?: Incident | null;
}) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const { data: monitors } = useMonitors();
  const editing = !!incident;
  const isMaint = (incident?.kind ?? kind) === "maintenance";

  const [title, setTitle] = useState("");
  const [impact, setImpact] = useState<Impact>("minor");
  const [status, setStatus] = useState<IncidentStatus>("investigating");
  const [message, setMessage] = useState("");
  const [monitorIds, setMonitorIds] = useState<number[]>([]);
  const [monitorImpact, setMonitorImpact] = useState<Record<number, MonitorImpact>>({});
  const [start, setStart] = useState("");
  const [end, setEnd] = useState("");
  const [auto, setAuto] = useState(true);
  const [notify, setNotify] = useState(true);
  const [errors, setErrors] = useState<Record<string, string>>({});

  useEffect(() => {
    if (!open) return;
    const w = defaultWindow();
    setTitle(incident?.title ?? "");
    setImpact(incident?.impact ?? (isMaint ? "none" : "minor"));
    setStatus(incident?.status ?? (isMaint ? "scheduled" : "investigating"));
    setMessage("");
    setMonitorIds(incident?.monitors.map((m) => m.monitor_id) ?? []);
    setMonitorImpact(Object.fromEntries((incident?.monitors ?? []).map((m) => [m.monitor_id, m.impact === "down" ? "down" : "degraded"])));
    setStart(incident ? toLocalInput(incident.scheduled_start) : w.start);
    setEnd(incident ? toLocalInput(incident.scheduled_end) : w.end);
    setAuto(incident?.auto_transition ?? true);
    setNotify(incident?.notify_subscribers ?? true);
    setErrors({});
  }, [open, incident, isMaint]);



  const save = useMutation({
    mutationFn: () => {
      const body = {
        kind: incident?.kind ?? kind,
        title,
        impact,
        status: editing ? undefined : status,
        message: editing ? undefined : message,
        monitors: monitorIds.map((id) => ({ id, impact: isMaint ? "maintenance" : (monitorImpact[id] ?? "degraded") })),
        scheduled_start: isMaint ? fromLocalInput(start) : null,
        scheduled_end: isMaint ? fromLocalInput(end) : null,
        auto_transition: auto,
        notify_subscribers: notify,
      };
      return editing ? api.put<Incident>(`/incidents/${incident!.id}`, body) : api.post<Incident>("/incidents", body);
    },
    onSuccess: (inc) => {
      qc.invalidateQueries({ queryKey: ["incidents"] });
      qc.invalidateQueries({ queryKey: ["incident", inc.id] });
      qc.invalidateQueries({ queryKey: ["overview"] });
      toast.success(editing ? "Saved" : isMaint ? "Maintenance scheduled" : "Incident declared", { description: inc.title });
      onOpenChange(false);
      if (!editing) navigate(`/incidents/${inc.id}`);
    },
    onError: (e) => {
      if (e instanceof ApiError && e.fields) setErrors(e.fields);
      else toast.error(e.message);
    },
  });

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent className="sm:max-w-[34rem]">
        <form
          className="flex h-full flex-col"
          onSubmit={(e) => {
            e.preventDefault();
            setErrors({});
            save.mutate();
          }}
        >
          <SheetHeader>
            <SheetTitle>{editing ? `Edit ${isMaint ? "maintenance" : "incident"}` : isMaint ? "Schedule maintenance" : "Declare an incident"}</SheetTitle>
            <SheetDescription>
              {isMaint
                ? "Affected monitors show “Maintenance” during the window, and downtime in it doesn't count against uptime."
                : "Shown on the status page. Post updates as you learn more."}
            </SheetDescription>
          </SheetHeader>

          <div className="flex-1 overflow-y-auto px-5 py-5">
            <div className="grid gap-5">
              <Field label="Title" htmlFor="inc-title" error={errors.title}>
                <Input id="inc-title" autoFocus value={title} onChange={(e) => setTitle(e.target.value)} placeholder={isMaint ? "Database upgrade" : "Elevated API error rates"} />
              </Field>

              {isMaint ? (
                <div className="grid gap-3 sm:grid-cols-2">
                  <Field label="Starts" htmlFor="inc-start" error={errors.scheduled_start}>
                    <Input id="inc-start" type="datetime-local" value={start} onChange={(e) => setStart(e.target.value)} />
                  </Field>
                  <Field label="Ends" htmlFor="inc-end" error={errors.scheduled_end}>
                    <Input id="inc-end" type="datetime-local" value={end} onChange={(e) => setEnd(e.target.value)} />
                  </Field>
                  <p className="-mt-1 text-xs text-muted-foreground sm:col-span-2">
                    Times are in your time zone ({Intl.DateTimeFormat().resolvedOptions().timeZone}). Visitors see them in theirs.
                  </p>
                </div>
              ) : (
                <div className="grid gap-1.5">
                  <Label>Impact</Label>
                  <div className="grid grid-cols-2 gap-1.5 sm:grid-cols-4">
                    {impacts.map((i) => (
                      <button
                        key={i.value}
                        type="button"
                        onClick={() => setImpact(i.value)}
                        className={cn(
                          "flex flex-col items-start gap-0.5 rounded-lg border px-2.5 py-2 text-left transition-colors hover:bg-muted/50",
                          impact === i.value && "border-foreground/60 bg-muted/60",
                        )}
                        aria-pressed={impact === i.value}
                      >
                        <span className="flex items-center gap-1.5 text-[13px] font-medium">
                          <span className={cn("size-2 rounded-full", i.dot)} /> {i.label}
                        </span>
                        <span className="text-[11px] leading-tight text-muted-foreground">{i.hint}</span>
                      </button>
                    ))}
                  </div>
                </div>
              )}

              {!editing && !isMaint && (
                <div className="grid gap-1.5">
                  <Label>Status</Label>
                  <div className="flex flex-wrap gap-1.5">
                    {incidentStatuses.map((s) => (
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
                  <p className="text-xs text-muted-foreground">{statusHint[status]}</p>
                </div>
              )}

              {!editing && (
                <Field label={isMaint ? "Details" : "Message"} htmlFor="inc-msg" error={errors.message} hint={isMaint ? "What will happen and what to expect. Optional." : "Visible to everyone on the status page."}>
                  <Textarea
                    id="inc-msg"
                    rows={4}
                    value={message}
                    onChange={(e) => setMessage(e.target.value)}
                    placeholder={isMaint ? "We're upgrading the primary database. Expect up to 5 minutes of read-only mode." : "We're investigating reports of failed requests to the API."}
                  />
                </Field>
              )}

              <Field label="Affected monitors" error={errors.monitors}>
                <MonitorPicker value={monitorIds} onChange={setMonitorIds} />
              </Field>
              {!isMaint && monitorIds.length > 0 && (
                <div className="grid gap-1.5 rounded-lg border p-2">
                  {monitorIds.map((id) => {
                    const m = monitors?.find((x) => x.id === id);
                    return (
                      <div key={id} className="flex items-center gap-2 text-[13px]">
                        <span className="min-w-0 flex-1 truncate">{m?.name ?? `#${id}`}</span>
                        <Select value={monitorImpact[id] ?? "degraded"} onValueChange={(v) => setMonitorImpact((p) => ({ ...p, [id]: v as MonitorImpact }))}>
                          <SelectTrigger className="h-7 w-32 text-xs">
                            <SelectValue />
                          </SelectTrigger>
                          <SelectContent>
                            <SelectItem value="degraded">Show degraded</SelectItem>
                            <SelectItem value="down">Show down</SelectItem>
                          </SelectContent>
                        </Select>
                      </div>
                    );
                  })}
                </div>
              )}



              <div className="grid gap-2">
                {isMaint && (
                  <ToggleRow label="Start and finish automatically" hint="Switches to “In progress” at the start time and “Completed” at the end." checked={auto} onChange={setAuto} />
                )}
                <ToggleRow label="Notify subscribers" hint="Email people who subscribed to the status page." checked={notify} onChange={setNotify} />
              </div>
            </div>
          </div>

          <SheetFooter>
            <Button type="button" variant="ghost" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button type="submit" disabled={save.isPending || !title.trim()}>
              {save.isPending && <Loader2Icon className="animate-spin" />}
              {editing ? "Save changes" : isMaint ? "Schedule" : "Declare incident"}
            </Button>
          </SheetFooter>
        </form>
      </SheetContent>
    </Sheet>
  );
}

function ToggleRow({ label, hint, checked, onChange }: { label: string; hint: string; checked: boolean; onChange: (v: boolean) => void }) {
  return (
    <label className="flex cursor-pointer items-center justify-between gap-4 rounded-lg border px-3 py-2.5">
      <span>
        <span className="block text-[13px] font-medium">{label}</span>
        <span className="block text-xs text-muted-foreground">{hint}</span>
      </span>
      <Switch checked={checked} onCheckedChange={onChange} />
    </label>
  );
}
