import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { ActivityIcon, BellIcon, CalendarClockIcon, ExternalLinkIcon, LogOutIcon, MoonIcon, RefreshCwIcon, SendIcon, ShieldAlertIcon, ShieldCheckIcon, SmartphoneIcon, TriangleAlertIcon } from "lucide-react";
import { timeAgo } from "@drix/ui/lib/utils";
import { useTheme, type ThemePref } from "@drix/ui/lib/theme";
import { api } from "../api";
import { clearSavedCache } from "../lib/persist";
import { useMe } from "../lib/queries";
import { roleLabel } from "../lib/roles";
import { currentEndpoint, disablePush, enablePush, pushUnavailable, type PushInfo } from "./push";
import { IconTile, IosSwitch, ListGroup, NavScreen, Row, Segmented } from "./ui";

const events: { key: string; label: string; color: string; icon: React.ReactNode }[] = [
  { key: "down", label: "Monitor Down", color: "var(--ios-red)", icon: <ActivityIcon /> },
  { key: "recovered", label: "Recovered", color: "var(--ios-green)", icon: <RefreshCwIcon /> },
  { key: "degraded", label: "Degraded", color: "var(--ios-orange)", icon: <TriangleAlertIcon /> },
  { key: "incident", label: "Incidents & Maintenance", color: "var(--ios-indigo)", icon: <CalendarClockIcon /> },
  { key: "cert_expiring", label: "Certificate Expiring", color: "var(--ios-gray)", icon: <ShieldAlertIcon /> },
];
const defaultEvents = ["down", "recovered", "incident", "cert_expiring"];

export default function SettingsScreen() {
  const qc = useQueryClient();
  const { data: me } = useMe();
  const { theme, setTheme } = useTheme();
  const unavailable = pushUnavailable();
  const info = useQuery({ queryKey: ["push"], queryFn: () => api.get<PushInfo>("/push") });
  const [endpoint, setEndpoint] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    currentEndpoint().then(setEndpoint);
  }, []);

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
        toast.success("Notifications are on");
      } else {
        await disablePush(thisDevice?.id);
        setEndpoint(null);
        toast.success("Notifications are off");
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
  const remove = useMutation({ mutationFn: (id: number) => api.del(`/push/subscriptions/${id}`), onSuccess: refresh });
  const logout = useMutation({
    mutationFn: () => api.post("/auth/logout"),
    onSettled: () => {
      qc.clear();
      clearSavedCache();
      window.location.href = "/login";
    },
  });

  const email = me?.user.email ?? "";
  const initials = (me?.user.name || email).split(/[\s@.]+/).filter(Boolean).slice(0, 2).map((w) => w[0]?.toUpperCase()).join("");

  return (
    <NavScreen title="Settings">
      {/* Account, like the Apple ID cell. */}
      <ListGroup>
        <div className="flex items-center gap-3.5 px-4 py-3">
          <span className="flex size-[60px] shrink-0 items-center justify-center rounded-full bg-gradient-to-b from-[#a1a1aa] to-[#71717a] text-[24px] font-semibold text-white">{initials}</span>
          <div className="min-w-0">
            <p className="truncate text-[20px] leading-6 font-semibold">{me?.user.name || email.split("@")[0]}</p>
            <p className="truncate text-[15px] text-[var(--ios-label2)]">
              {email} · {me ? roleLabel[me.user.role] : ""}
            </p>
          </div>
        </div>
      </ListGroup>

      <ListGroup
        header="Notifications"
        footer={
          unavailable === "install"
            ? "On iPhone, notifications only work in the Home Screen app. In Safari, tap Share, then Add to Home Screen, and open Drix from there."
            : unavailable === "unsupported"
              ? "This browser doesn't support notifications."
              : thisDevice
                ? "Alerts arrive even when Drix is closed. They pause during maintenance."
                : info.data?.error ?? "Get alerts on this device when something goes down."
        }
      >
        <Row
          leading={<IconTile color="var(--ios-red)"><BellIcon fill="currentColor" /></IconTile>}
          leadingKind="tile"
          title="Allow Notifications"
          loading={busy}
          accessory={<IosSwitch checked={!!thisDevice} disabled={!!unavailable || !info.data?.public_key || busy} onCheckedChange={toggle} />}
        />
        {thisDevice && (
          <Row
            leading={<IconTile color="var(--ios-green)"><SendIcon /></IconTile>}
            leadingKind="tile"
            title="Send Test Notification"
            loading={test.isPending}
            onClick={() => test.mutate()}
            accessory="none"
          />
        )}
      </ListGroup>

      {thisDevice && (
        <ListGroup header="Notify Me About">
          {events.map((e) => (
            <Row
              key={e.key}
              leading={<IconTile color={e.color}>{e.icon}</IconTile>}
              leadingKind="tile"
              title={e.label}
              accessory={
                <IosSwitch
                  checked={thisDevice.events.includes(e.key)}
                  onCheckedChange={(on) => setEvents.mutate(on ? [...thisDevice.events, e.key] : thisDevice.events.filter((x) => x !== e.key))}
                />
              }
            />
          ))}
        </ListGroup>
      )}

      {others.length > 0 && (
        <ListGroup header="Other Devices" footer="These also get your notifications.">
          {others.map((d) => (
            <Row
              key={d.id}
              leading={<IconTile color="var(--ios-gray)"><SmartphoneIcon /></IconTile>}
              leadingKind="tile"
              title={d.device || "Device"}
              subtitle={d.last_used_at ? `Last alert ${timeAgo(d.last_used_at)}` : `Added ${timeAgo(d.created_at)}`}
              accessory={
                <button type="button" onClick={() => remove.mutate(d.id)} className="text-[17px] text-[var(--ios-red)] active:opacity-40">
                  Remove
                </button>
              }
            />
          ))}
        </ListGroup>
      )}

      <ListGroup header="Appearance">
        <Row
          leading={<IconTile color="var(--ios-indigo)"><MoonIcon fill="currentColor" /></IconTile>}
          leadingKind="tile"
          title="Theme"
          accessory={
            <Segmented<ThemePref>
              className="w-[190px]"
              value={theme}
              onChange={setTheme}
              options={[
                { value: "system", label: "Auto" },
                { value: "light", label: "Light" },
                { value: "dark", label: "Dark" },
              ]}
            />
          }
        />
      </ListGroup>

      <ListGroup header="Security" footer="Passkeys let you sign in with Face ID. Manage them in the full admin.">
        <Row
          leading={<IconTile color="var(--ios-green)"><ShieldCheckIcon /></IconTile>}
          leadingKind="tile"
          title="Two-Factor Sign-In"
          detail={me?.user.two_factor ? "On" : "Off"}
          accessory="chevron"
          onClick={() => {
            try {
              sessionStorage.setItem("drix-full-admin", "1");
            } catch {
              /* ignore */
            }
            window.location.href = "/settings#two-factor";
          }}
        />
      </ListGroup>

      <ListGroup>
        <Row
          leading={<IconTile color="var(--ios-gray)"><ExternalLinkIcon /></IconTile>}
          leadingKind="tile"
          title="Open Full Admin"
          accessory="chevron"
          onClick={() => {
            try {
              sessionStorage.setItem("drix-full-admin", "1");
            } catch {
              /* ignore */
            }
            window.location.href = "/";
          }}
        />
      </ListGroup>

      <ListGroup>
        <Row
          leading={<IconTile color="var(--ios-red)"><LogOutIcon /></IconTile>}
          leadingKind="tile"
          title="Sign Out"
          tone="destructive"
          loading={logout.isPending}
          onClick={() => logout.mutate()}
        />
      </ListGroup>
    </NavScreen>
  );
}
