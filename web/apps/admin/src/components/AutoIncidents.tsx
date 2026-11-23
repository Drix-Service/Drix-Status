import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { Loader2Icon } from "lucide-react";
import { Button } from "@drix/ui/components/button";
import { Field, Input, Switch } from "@drix/ui/components/form";
import { api, ApiError } from "../api";

type Settings = { enabled: boolean; after_min: number; notify_subscribers: boolean };
type SectionComponent = React.ComponentType<{ title: string; description: string; children: React.ReactNode }>;

/** Settings → Automatic incidents. */
export function AutoIncidentsSection({ Section, canEdit }: { Section: SectionComponent; canEdit: boolean }) {
  const qc = useQueryClient();
  const { data } = useQuery({ queryKey: ["auto-incidents"], queryFn: () => api.get<Settings>("/auto-incidents") });
  const [f, setF] = useState<Settings>({ enabled: false, after_min: 5, notify_subscribers: false });
  const [error, setError] = useState<string | undefined>();
  useEffect(() => {
    if (data) setF(data);
  }, [data]);
  const save = useMutation({
    mutationFn: () => api.put<Settings>("/auto-incidents", f),
    onSuccess: (d) => {
      qc.setQueryData(["auto-incidents"], d);
      setError(undefined);
      toast.success(d.enabled ? "Automatic incidents are on" : "Automatic incidents are off");
    },
    onError: (e) => {
      setError(e instanceof ApiError ? e.fields?.after_min : undefined);
      toast.error(e.message);
    },
  });
  const dirty = !!data && JSON.stringify(f) !== JSON.stringify(data);
  return (
    <Section
      title="Automatic incidents"
      description="When a monitor on the status page has been down for a while, post an incident there by itself and resolve it when it's back, even if nobody is awake to write one."
    >
      <fieldset disabled={!canEdit} className="grid gap-4">
        <label className="flex items-center gap-2.5 text-[13px]">
          <Switch checked={f.enabled} onCheckedChange={(v) => setF((p) => ({ ...p, enabled: v }))} />
          Post incidents automatically
        </label>
        {f.enabled && (
          <>
            <Field label="After a monitor has been down for (minutes)" error={error} hint="Shorter blips don't get an incident.">
              <Input type="number" min={1} max={60} className="w-28" value={f.after_min} onChange={(e) => setF((p) => ({ ...p, after_min: Number(e.target.value) }))} />
            </Field>
            <label className="flex items-center gap-2.5 text-[13px]">
              <Switch checked={f.notify_subscribers} onCheckedChange={(v) => setF((p) => ({ ...p, notify_subscribers: v }))} />
              Email subscribers about automatic incidents
            </label>
            <div className="rounded-lg border bg-muted/30 px-3 py-2.5 text-xs text-muted-foreground">
              <p>
                The incident says "Problems with <em>name</em>" and "We're investigating…" in the status page's default language. Several outages at once share one incident.
              </p>
              <ul className="mt-1.5 list-disc pl-4">
                <li>Monitors already in an incident someone wrote, in maintenance, or with "Post automatic incidents" off are skipped.</li>
                <li>It's resolved when everything has been back for 2 minutes. If you post an update or change its status, you're in charge and it's left to you.</li>
                <li>Your alert channels aren't sent these: they already got the monitor's down alert.</li>
              </ul>
            </div>
          </>
        )}
        {canEdit && (
          <div>
            <Button type="button" disabled={!dirty || save.isPending} onClick={() => save.mutate()}>
              {save.isPending && <Loader2Icon className="animate-spin" />}
              Save
            </Button>
          </div>
        )}
      </fieldset>
    </Section>
  );
}
