import { useMutation, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { CheckCircle2Icon, HandIcon, Loader2Icon } from "lucide-react";
import { timeAgo } from "@drix/ui/lib/utils";
import { Button } from "@drix/ui/components/button";
import { ackWho, api, type Monitor, type MonitorAck } from "../api";
import { keys } from "../lib/queries";
import { useCan } from "../lib/roles";

/** Acknowledge a monitor's current outage ("I'm on it"). */
export function useAcknowledge() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => api.post<MonitorAck>(`/monitors/${id}/ack`),
    onSuccess: (ack, id) => {
      qc.setQueryData<Monitor>(keys.monitor(id), (m) => (m ? { ...m, ack } : m));
      qc.setQueryData<Monitor[]>(keys.monitors, (list) => list?.map((m) => (m.id === id ? { ...m, ack } : m)));
      qc.invalidateQueries({ queryKey: ["m-monitor", id] });
      toast.success("Acknowledged. Reminders and escalations stop until it recovers.");
    },
    onError: (e) => toast.error(e.message),
  });
}

/** Under a down monitor: who's on it, or a button to say you are. */
export function AckBar({ monitor }: { monitor: Monitor }) {
  const canEdit = useCan()("editor");
  const ack = useAcknowledge();
  if (monitor.live.status !== "down") return null;
  if (monitor.ack) {
    return (
      <div className="mt-3 flex items-center gap-2.5 rounded-lg border bg-muted/30 px-3 py-2 text-[13px]">
        <CheckCircle2Icon className="size-4 shrink-0 text-up" />
        <p className="min-w-0 flex-1">
          <span className="font-medium">Acknowledged by {ackWho(monitor.ack)}</span>
          <span className="text-muted-foreground"> · {timeAgo(monitor.ack.at)}. Reminders and escalations are paused until it recovers.</span>
        </p>
      </div>
    );
  }
  return (
    <div className="mt-3 flex items-center gap-2.5 rounded-lg border border-down/25 px-3 py-2 text-[13px]">
      <HandIcon className="size-4 shrink-0 text-muted-foreground" />
      <p className="min-w-0 flex-1 text-muted-foreground">Nobody has acknowledged this outage yet.</p>
      {canEdit && (
        <Button size="sm" disabled={ack.isPending} onClick={() => ack.mutate(monitor.id)}>
          {ack.isPending && <Loader2Icon className="animate-spin" />}
          Acknowledge
        </Button>
      )}
    </div>
  );
}
