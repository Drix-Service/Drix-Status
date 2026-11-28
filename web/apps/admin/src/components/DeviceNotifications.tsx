import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { BellIcon, Loader2Icon, MonitorIcon, SendIcon, SmartphoneIcon } from "lucide-react";
import { cn, timeAgo } from "@drix/ui/lib/utils";
import { Button } from "@drix/ui/components/button";
import { Checkbox, Switch } from "@drix/ui/components/form";
import { api } from "../api";
import { currentEndpoint, disablePush, enablePush, pushUnavailable, type PushDevice, type PushInfo } from "../mobile/push";

type SectionComponent = React.ComponentType<{ title: string; description: string; children: React.ReactNode }>;

const eventOptions = [
  { key: "down", label: "Monitor down" },
  { key: "recovered", label: "Recovered" },
  { key: "degraded", label: "Degraded" },
  { key: "incident", label: "Incidents & maintenance" },
  { key: "cert_expiring", label: "Certificate expiring" },
];
const defaultEvents = ["down", "recovered", "incident", "cert_expiring"];

const deviceIcon = (d: PushDevice) => (/iPhone|Android|iPad/.test(d.device) ? SmartphoneIcon : MonitorIcon);

/** Settings → Notifications on this device: browser push for Macs and PCs
 *  (installed as an app or in the browser), the same alerts the phone app gets. */
export function DeviceNotificationsSection({ Section }: { Section: SectionComponent }) {
  const qc = useQueryClient();
  const unavailable = pushUnavailable();
  const info = useQuery({ queryKey: ["push"], queryFn: () => api.get<PushInfo>("/push"), enabled: unavailable === null });
  const [endpoint, setEndpoint] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (unavailable === null) currentEndpoint().then(setEndpoint);
  }, [unavailable]);

  const thisDevice = info.data?.devices.find((d) => d.endpoint === endpoint);
  const others = info.data?.devices.filter((d) => d.endpoint !== endpoint) ?? [];
  const refresh = () => qc.invalidateQueries({ queryKey: ["push"] });

  const toggle = async (on: boolean) => {
    if (!info.data?.public_key) return;
    setBusy(true);
    try {
      if (on) {
        const d = await enablePush(info.data.public_key, defaultEvents);
        setEndpoint(d.endpoint);
        toast.success("Notifications are on for this device");
      } else {
        await disablePush(thisDevice?.id);
        setEndpoint(null);
        toast.success("Notifications are off for this device");
      }
      refresh();
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Couldn't change notifications");
    } finally {
      setBusy(false);
    }
  };

  const setEvents = useMutation({
    mutationFn: (list: string[]) => api.put(`/push/subscriptions/${thisDevice!.id}`, { events: list }),
    onSuccess: refresh,
    onError: (e) => toast.error(e.message),
  });
  const test = useMutation({
    mutationFn: () => api.post(`/push/subscriptions/${thisDevice!.id}/test`),
    onSuccess: () => toast.success("Test notification sent"),
    onError: (e) => toast.error(e.message),
  });
  const remove = useMutation({
    mutationFn: (id: number) => api.del(`/push/subscriptions/${id}`),
    onSuccess: () => {
      refresh();
      toast.success("Device removed");
    },
    onError: (e) => toast.error(e.message),
  });

  const note =
    unavailable === "insecure"
      ? "Notifications need the admin on HTTPS. Without a domain, install.sh --tls local sets that up."
      : unavailable === "unsupported"
        ? "This browser doesn't support notifications. Safari, Chrome, Edge and Firefox do."
        : unavailable === "install"
          ? "On iPhone and iPad, notifications only work in the Home Screen app."
          : info.data?.error;

  return (
    <Section
      title="Notifications on this device"
      description="Alerts as notifications on this computer, even when Drix is closed. Tip: install Drix as an app (Safari → File → Add to Dock, or Chrome → Install) to get its own Dock icon and window."
    >
      {note ? (
        <p className="rounded-md border bg-muted/40 px-3 py-2 text-xs text-muted-foreground">{note}</p>
      ) : (
        <>
          <label className="flex items-center gap-2.5 text-[13px]">
            <Switch checked={!!thisDevice} disabled={!info.data?.public_key || busy} onCheckedChange={toggle} />
            <BellIcon className="size-4 text-muted-foreground" />
            <span className="flex-1">Notify me on this device</span>
            {busy && <Loader2Icon className="size-4 animate-spin text-muted-foreground" />}
          </label>

          {thisDevice && (
            <>
              <div className="grid gap-2 sm:grid-cols-2">
                {eventOptions.map((e) => (
                  <label key={e.key} className="flex cursor-pointer items-center gap-2 rounded-md px-1 py-0.5 text-[13px] hover:bg-muted/50">
                    <Checkbox
                      checked={thisDevice.events.includes(e.key)}
                      onCheckedChange={(on) => setEvents.mutate(on ? [...thisDevice.events, e.key] : thisDevice.events.filter((x) => x !== e.key))}
                    />
                    {e.label}
                  </label>
                ))}
              </div>
              <div>
                <Button type="button" variant="outline" size="sm" disabled={test.isPending} onClick={() => test.mutate()}>
                  {test.isPending ? <Loader2Icon className="animate-spin" /> : <SendIcon />}
                  Send a test notification
                </Button>
              </div>
            </>
          )}
        </>
      )}

      {others.length > 0 && (
        <div className="grid gap-2">
          <p className="text-xs font-medium text-muted-foreground">Your other devices</p>
          <ul className="divide-y rounded-lg border">
            {others.map((d) => {
              const Icon = deviceIcon(d);
              return (
                <li key={d.id} className="flex items-center gap-3 px-3 py-2 text-[13px]">
                  <Icon className="size-4 shrink-0 text-muted-foreground" />
                  <span className="min-w-0 flex-1">
                    <span className="block truncate">{d.device || "Device"}</span>
                    <span className={cn("block text-xs text-muted-foreground")}>
                      {d.last_used_at ? `Last alert ${timeAgo(d.last_used_at)}` : `Added ${timeAgo(d.created_at)}`}
                    </span>
                  </span>
                  <Button type="button" variant="ghost" size="sm" className="hover:text-destructive" onClick={() => remove.mutate(d.id)}>
                    Remove
                  </Button>
                </li>
              );
            })}
          </ul>
        </div>
      )}
    </Section>
  );
}
