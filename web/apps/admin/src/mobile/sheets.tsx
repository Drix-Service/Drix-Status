import { useEffect, useState } from "react";
import { useNavigate } from "react-router";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { api, ApiError, type Impact, type Incident, type IncidentStatus } from "../api";
import { keys, useMonitors } from "../lib/queries";
import { CheckList, FieldCell, IosSwitch, ListGroup, Row, Sheet, TextAreaCell, setNav } from "./ui";

const errMsg = (e: unknown) => (e instanceof ApiError ? Object.values(e.fields ?? {})[0] ?? e.message : "Something went wrong");

function useRefreshIncidents() {
  const qc = useQueryClient();
  return () => {
    qc.invalidateQueries({ queryKey: ["m-incidents"] });
    qc.invalidateQueries({ queryKey: ["incident"] });
    qc.invalidateQueries({ queryKey: keys.monitors });
  };
}

function NotifyGroup({ checked, onChange }: { checked: boolean; onChange: (v: boolean) => void }) {
  return (
    <ListGroup footer="Sends an email to everyone subscribed to your status page.">
      <Row title="Email Subscribers" accessory={<IosSwitch checked={checked} onCheckedChange={onChange} />} />
    </ListGroup>
  );
}

/** New incident, optionally pre-filled with affected monitors. */
export function NewIncidentSheet({ open, onOpenChange, monitorIds = [] }: { open: boolean; onOpenChange: (o: boolean) => void; monitorIds?: number[] }) {
  const navigate = useNavigate();
  const refresh = useRefreshIncidents();
  const { data: monitors } = useMonitors();
  const [title, setTitle] = useState("");
  const [impact, setImpact] = useState<Impact>("minor");
  const [message, setMessage] = useState("");
  const [affected, setAffected] = useState<number[]>(monitorIds);
  const [notify, setNotify] = useState(true);

  useEffect(() => {
    if (open) {
      setTitle("");
      setMessage("");
      setImpact("minor");
      setAffected(monitorIds);
      setNotify(true);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  const create = useMutation({
    mutationFn: () =>
      api.post<Incident>("/incidents", {
        kind: "incident",
        title,
        status: "investigating",
        impact,
        message,
        notify_subscribers: notify,
        monitors: affected.map((id) => ({ id, impact: impact === "minor" ? "degraded" : "down" })),
      }),
    onSuccess: (inc) => {
      refresh();
      onOpenChange(false);
      toast.success("Incident posted");
      setNav("push");
      navigate(`/m/incidents/${inc.id}`, { viewTransition: true });
    },
    onError: (e) => toast.error(errMsg(e)),
  });

  const toggle = (id: number) => setAffected((a) => (a.includes(id) ? a.filter((x) => x !== id) : [...a, id]));

  return (
    <Sheet
      open={open}
      onOpenChange={onOpenChange}
      title="New Incident"
      action={{ label: "Post", onClick: () => create.mutate(), disabled: !title.trim(), loading: create.isPending }}
    >
      <ListGroup>
        <FieldCell value={title} onChange={(e) => setTitle(e.target.value)} placeholder="Title" maxLength={200} autoFocus />
        <TextAreaCell value={message} onChange={(e) => setMessage(e.target.value)} placeholder="What's happening?" />
      </ListGroup>
      <ListGroup header="Impact">
        <CheckList<Impact>
          value={impact}
          onChange={setImpact}
          options={[
            { value: "minor", label: "Minor", subtitle: "Slower or partly unavailable" },
            { value: "major", label: "Major", subtitle: "Unavailable for many users" },
            { value: "critical", label: "Critical", subtitle: "Completely down" },
          ]}
        />
      </ListGroup>
      <ListGroup header="Affected Monitors">
        {(monitors ?? []).map((m) => (
          <Row key={m.id} title={m.name} onClick={() => toggle(m.id)} accessory={affected.includes(m.id) ? "check" : "none"} />
        ))}
      </ListGroup>
      <NotifyGroup checked={notify} onChange={setNotify} />
    </Sheet>
  );
}

const durations = [
  { value: "15", label: "15 Minutes" },
  { value: "60", label: "1 Hour" },
  { value: "120", label: "2 Hours" },
  { value: "240", label: "4 Hours" },
];

/** Maintenance starting now: alerts pause for these monitors until it ends. */
export function MaintenanceSheet({ open, onOpenChange, monitorIds, defaultTitle }: { open: boolean; onOpenChange: (o: boolean) => void; monitorIds: number[]; defaultTitle: string }) {
  const navigate = useNavigate();
  const refresh = useRefreshIncidents();
  const [minutes, setMinutes] = useState("60");
  const [title, setTitle] = useState(defaultTitle);
  const [notify, setNotify] = useState(false);

  useEffect(() => {
    if (open) {
      setMinutes("60");
      setTitle(defaultTitle);
      setNotify(false);
    }
  }, [open, defaultTitle]);

  const start = useMutation({
    mutationFn: () => {
      const now = new Date();
      return api.post<Incident>("/incidents", {
        kind: "maintenance",
        title,
        impact: "none",
        scheduled_start: now.toISOString(),
        scheduled_end: new Date(now.getTime() + Number(minutes) * 60_000).toISOString(),
        notify_subscribers: notify,
        monitors: monitorIds.map((id) => ({ id, impact: "maintenance" })),
      });
    },
    onSuccess: (inc) => {
      refresh();
      onOpenChange(false);
      toast.success("Maintenance started", { description: "Alerts are paused until it ends." });
      setNav("push");
      navigate(`/m/incidents/${inc.id}`, { viewTransition: true });
    },
    onError: (e) => toast.error(errMsg(e)),
  });

  return (
    <Sheet open={open} onOpenChange={onOpenChange} title="Maintenance" action={{ label: "Start", onClick: () => start.mutate(), disabled: !title.trim(), loading: start.isPending }}>
      <ListGroup>
        <FieldCell value={title} onChange={(e) => setTitle(e.target.value)} placeholder="Title" maxLength={200} />
      </ListGroup>
      <ListGroup header="Duration" footer="Starts now. Alerts for this monitor pause until it ends, and you can end it early.">
        <CheckList value={minutes} onChange={setMinutes} options={durations} />
      </ListGroup>
      <NotifyGroup checked={notify} onChange={setNotify} />
    </Sheet>
  );
}

const incidentStatuses: { value: IncidentStatus; label: string }[] = [
  { value: "investigating", label: "Investigating" },
  { value: "identified", label: "Identified" },
  { value: "monitoring", label: "Monitoring" },
  { value: "resolved", label: "Resolved" },
];
const maintenanceStatuses: { value: IncidentStatus; label: string }[] = [
  { value: "in_progress", label: "In Progress" },
  { value: "completed", label: "Completed" },
];

/** Post an update to an incident or maintenance window. */
export function UpdateSheet({ open, onOpenChange, incident, initialStatus }: { open: boolean; onOpenChange: (o: boolean) => void; incident: Incident; initialStatus?: IncidentStatus }) {
  const refresh = useRefreshIncidents();
  const options = incident.kind === "maintenance" ? maintenanceStatuses : incidentStatuses;
  const [status, setStatus] = useState<IncidentStatus>(initialStatus ?? incident.status);
  const [message, setMessage] = useState("");

  useEffect(() => {
    if (open) {
      setStatus(initialStatus ?? (options.some((o) => o.value === incident.status) ? incident.status : options[0].value));
      setMessage(initialStatus === "resolved" ? "This incident has been resolved." : initialStatus === "completed" ? "Maintenance is complete." : "");
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  const post = useMutation({
    mutationFn: () => api.post<Incident>(`/incidents/${incident.id}/updates`, { status, message }),
    onSuccess: () => {
      refresh();
      onOpenChange(false);
      toast.success("Update posted");
    },
    onError: (e) => toast.error(errMsg(e)),
  });

  return (
    <Sheet
      open={open}
      onOpenChange={onOpenChange}
      title={initialStatus === "resolved" ? "Resolve" : initialStatus === "completed" ? "End Maintenance" : "Update"}
      action={{ label: "Post", onClick: () => post.mutate(), disabled: !message.trim(), loading: post.isPending }}
    >
      <ListGroup>
        <TextAreaCell value={message} onChange={(e) => setMessage(e.target.value)} placeholder="What changed?" autoFocus={!initialStatus} />
      </ListGroup>
      <ListGroup header="Status">
        <CheckList value={status} onChange={setStatus} options={options} />
      </ListGroup>
    </Sheet>
  );
}
