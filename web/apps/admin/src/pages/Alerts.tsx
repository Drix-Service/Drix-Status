import { useEffect, useState } from "react";
import { Link } from "react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { BellIcon, BellRingIcon, HashIcon, Loader2Icon, MailIcon, MegaphoneIcon, MoreHorizontalIcon, PencilIcon, PlusIcon, SendIcon, SirenIcon, SmartphoneIcon, Trash2Icon, UsersIcon, WebhookIcon, MessageSquareIcon } from "lucide-react";
import { cn, formatDateTime, timeAgo } from "@drix/ui/lib/utils";
import { Button } from "@drix/ui/components/button";
import { Badge, Card, CardContent, CardDescription, CardHeader, CardTitle, EmptyState, Skeleton, Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@drix/ui/components/display";
import { Checkbox, Field, Input, Label, Switch, Textarea } from "@drix/ui/components/form";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger, Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@drix/ui/components/menu";
import { ConfirmDialog, Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@drix/ui/components/overlay";
import { api, ApiError, type AlertEvent, type Channel, type ChannelType, type Delivery } from "../api";
import { useMonitors } from "../lib/queries";
import { useCan } from "../lib/roles";
import { CopyButton, PageHeader } from "../components/Page";
import { MonitorPicker } from "../components/MonitorPicker";

export const channelTypes: { value: ChannelType; label: string; icon: typeof MailIcon; hint: string }[] = [
  { value: "email", label: "Email", icon: MailIcon, hint: "Send to one or more addresses" },
  { value: "slack", label: "Slack", icon: HashIcon, hint: "Incoming webhook to a channel" },
  { value: "discord", label: "Discord", icon: MessageSquareIcon, hint: "Channel webhook" },
  { value: "webhook", label: "Webhook", icon: WebhookIcon, hint: "Signed JSON to your own URL" },
  { value: "telegram", label: "Telegram", icon: SendIcon, hint: "A bot posts to a chat or group" },
  { value: "teams", label: "Teams", icon: UsersIcon, hint: "Card in a Microsoft Teams channel" },
  { value: "pagerduty", label: "PagerDuty", icon: SirenIcon, hint: "Pages whoever is on call" },
  { value: "opsgenie", label: "Opsgenie", icon: BellRingIcon, hint: "Opens and closes on-call alerts" },
  { value: "ntfy", label: "ntfy", icon: MegaphoneIcon, hint: "Phone push via ntfy.sh or your server" },
  { value: "pushover", label: "Pushover", icon: SmartphoneIcon, hint: "Phone push via Pushover" },
];

/** Settings fields for the channel types configured with plain text values. */
type ConfigField = { key: string; label: string; placeholder?: string; hint?: React.ReactNode; mono?: boolean; optional?: boolean; options?: { value: string; label: string }[] };
const docLink = (href: string, text: string) => (
  <a href={href} target="_blank" rel="noreferrer" className="underline underline-offset-2 hover:text-foreground">
    {text}
  </a>
);
const channelFields: Partial<Record<ChannelType, ConfigField[]>> = {
  telegram: [
    { key: "bot_token", label: "Bot token", placeholder: "123456789:AA…", mono: true, hint: <>Open Telegram, talk to {docLink("https://t.me/BotFather", "@BotFather")} and send /newbot. Add the bot to your group.</> },
    { key: "chat_id", label: "Chat ID", placeholder: "-1001234567890 or @yourchannel", mono: true, hint: "A group's ID starts with -100. For a public channel, use @name (the bot must be an admin)." },
  ],
  teams: [
    { key: "url", label: "Workflow URL", placeholder: "https://prod-00.westeurope.logic.azure.com/workflows/…", mono: true, hint: <>In the channel: ⋯ → Workflows → “Post to a channel when a webhook request is received”, then copy the URL. {docLink("https://support.microsoft.com/office/create-incoming-webhooks-with-workflows-for-microsoft-teams-8ae491c7-0394-4861-ba59-055e33f75498", "Guide")}</> },
  ],
  pagerduty: [
    { key: "routing_key", label: "Integration key", placeholder: "32 characters", mono: true, hint: <>Service → Integrations → Add “Events API v2”, then copy the integration key. Down opens an incident and recovery resolves it. {docLink("https://support.pagerduty.com/main/docs/services-and-integrations", "Guide")}</> },
  ],
  opsgenie: [
    { key: "api_key", label: "API key", placeholder: "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx", mono: true, hint: <>Team → Integrations → Add “API”, then copy its key. Down opens an alert and recovery closes it. {docLink("https://support.atlassian.com/opsgenie/docs/create-a-default-api-integration/", "Guide")}</> },
    { key: "region", label: "Region", options: [{ value: "us", label: "US (api.opsgenie.com)" }, { value: "eu", label: "EU (api.eu.opsgenie.com)" }] },
  ],
  ntfy: [
    { key: "topic", label: "Topic", placeholder: "drix-alerts-x7k2", mono: true, hint: <>Subscribe to this topic in the {docLink("https://ntfy.sh", "ntfy app")}. On ntfy.sh anyone who knows the topic can read it, so pick something hard to guess.</> },
    { key: "server", label: "Server", placeholder: "https://ntfy.sh", mono: true, optional: true, hint: "Leave empty for ntfy.sh, or use your own server." },
    { key: "token", label: "Access token", placeholder: "tk_…", mono: true, optional: true, hint: "Only for protected topics." },
  ],
  pushover: [
    { key: "user_key", label: "User key", placeholder: "30 characters", mono: true, hint: <>Shown on your {docLink("https://pushover.net", "Pushover")} dashboard. A group key works too.</> },
    { key: "app_token", label: "API token", placeholder: "30 characters", mono: true, hint: "Create an application in Pushover (name it Drix) and copy its token." },
  ],
};
const eventOptions: { value: AlertEvent; label: string; hint: string }[] = [
  { value: "down", label: "Down", hint: "A monitor stops passing" },
  { value: "recovered", label: "Recovered", hint: "It comes back up" },
  { value: "degraded", label: "Degraded", hint: "Slow, or an SSL monitor near expiry" },
  { value: "incident", label: "Incidents & maintenance", hint: "Posted, updated or resolved" },
  { value: "cert_expiring", label: "Certificate expiring", hint: "HTTPS certificate expires within 14 days" },
];
const escalations = [
  { value: 0, label: "Right away" },
  { value: 5, label: "If not acknowledged in 5 minutes" },
  { value: 10, label: "If not acknowledged in 10 minutes" },
  { value: 15, label: "If not acknowledged in 15 minutes" },
  { value: 30, label: "If not acknowledged in 30 minutes" },
  { value: 60, label: "If not acknowledged in 1 hour" },
];

const reminders = [
  { value: 0, label: "Off" },
  { value: 15, label: "Every 15 minutes" },
  { value: 30, label: "Every 30 minutes" },
  { value: 60, label: "Every hour" },
  { value: 240, label: "Every 4 hours" },
];
export const channelIcon = (t: ChannelType) => channelTypes.find((c) => c.value === t)?.icon ?? BellIcon;

export default function AlertsPage() {
  const can = useCan();
  const [editing, setEditing] = useState<Channel | "new" | null>(null);
  const { data: channels, isLoading } = useQuery({ queryKey: ["channels"], queryFn: () => api.get<Channel[]>("/channels"), enabled: can("admin") });

  if (!can("admin")) {
    return (
      <>
        <PageHeader title="Alerts" description="Where Drix sends notifications when something breaks." />
        <Card>
          <EmptyState icon={<BellIcon />} title="Admins manage alerts" description="Ask an admin or owner to add or change alert channels." />
        </Card>
      </>
    );
  }

  return (
    <>
      <PageHeader
        title="Alerts"
        description="Where Drix sends notifications when monitors go down, recover or an incident is posted. Alerts pause during maintenance."
        actions={
          <Button onClick={() => setEditing("new")}>
            <PlusIcon /> Add channel
          </Button>
        }
      />
      {isLoading ? (
        <Skeleton className="h-40 rounded-xl" />
      ) : !channels?.length ? (
        <Card>
          <EmptyState
            icon={<BellIcon />}
            title="No alert channels yet"
            description="Add email, Slack, Discord or a webhook to hear about problems the moment they happen."
            action={
              <Button onClick={() => setEditing("new")}>
                <PlusIcon /> Add channel
              </Button>
            }
          />
        </Card>
      ) : (
        <div className="grid gap-3 md:grid-cols-2">
          {channels.map((c) => (
            <ChannelCard key={c.id} channel={c} onEdit={() => setEditing(c)} />
          ))}
        </div>
      )}
      <Deliveries />
      <ChannelDialog channel={editing === "new" ? null : editing} open={editing !== null} onOpenChange={(o) => !o && setEditing(null)} />
    </>
  );
}

function ChannelCard({ channel: c, onEdit }: { channel: Channel; onEdit: () => void }) {
  const qc = useQueryClient();
  const [confirm, setConfirm] = useState(false);
  const Icon = channelIcon(c.type);
  const test = useMutation({
    mutationFn: () => api.post<{ ok: boolean; sent?: number }>(`/channels/${c.id}/test`),
    onSuccess: (r) => {
      if (r.sent && r.sent > 1) toast.success(`${r.sent} sample alerts sent`, { description: `One of each kind. Check ${c.name}.` });
      else toast.success("Test alert sent", { description: `Check ${c.name}.` });
      qc.invalidateQueries({ queryKey: ["deliveries"] });
    },
    onError: (e) => {
      toast.error(e.message);
      qc.invalidateQueries({ queryKey: ["deliveries"] });
    },
  });
  const remove = useMutation({
    mutationFn: () => api.del(`/channels/${c.id}`),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["channels"] });
      toast.success(`${c.name} removed`);
    },
  });
  const toggle = useMutation({
    mutationFn: (enabled: boolean) => api.put(`/channels/${c.id}`, { name: c.name, events: c.events, apply_to_all: c.apply_to_all, monitor_ids: c.monitor_ids, remind_every_min: c.remind_every_min, escalate_after_min: c.escalate_after_min, enabled }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["channels"] }),
  });
  const target =
    c.type === "email"
      ? c.config.to?.join(", ")
      : c.type === "telegram"
        ? `Chat ${String(c.config.chat_id ?? "")}`
        : c.type === "ntfy"
          ? `${String(c.config.server || "ntfy.sh").replace(/^https?:\/\//, "")}/${String(c.config.topic ?? "")}`
          : c.type === "pagerduty" || c.type === "opsgenie" || c.type === "pushover"
            ? channelTypes.find((t) => t.value === c.type)?.hint
            : c.config.url?.replace(/^https?:\/\//, "").replace(/(\/api\/webhooks\/\d+\/).+/, "$1…").slice(0, 60);

  return (
    <Card className={cn("flex flex-col p-4", !c.enabled && "opacity-60")}>
      <div className="flex items-start gap-3">
        <span className="flex size-9 shrink-0 items-center justify-center rounded-lg border bg-muted/40">
          <Icon className="size-4" />
        </span>
        <div className="min-w-0 flex-1">
          <p className="truncate text-[14px] font-semibold tracking-tight">{c.name}</p>
          <p className="truncate font-mono text-[11.5px] text-muted-foreground">{target}</p>
        </div>
        <Switch checked={c.enabled} onCheckedChange={(v) => toggle.mutate(v)} aria-label="Enabled" />
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button variant="ghost" size="icon-sm" aria-label="Channel actions">
              <MoreHorizontalIcon />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuItem onSelect={onEdit}>
              <PencilIcon /> Edit
            </DropdownMenuItem>
            <DropdownMenuItem onSelect={() => test.mutate()}>
              <SendIcon /> Send test
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem variant="destructive" onSelect={() => setConfirm(true)}>
              <Trash2Icon /> Delete
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
      <div className="mt-4 flex flex-wrap gap-1.5">
        {c.events.map((e) => (
          <Badge key={e} variant="secondary">
            {eventOptions.find((o) => o.value === e)?.label ?? e}
          </Badge>
        ))}
      </div>
      <div className="mt-auto flex items-center justify-between gap-2 pt-4 text-xs text-muted-foreground">
        <span>
          {c.apply_to_all ? "All monitors" : `${c.monitor_ids.length} monitor${c.monitor_ids.length === 1 ? "" : "s"}`}
          {c.escalate_after_min > 0 && ` · if not acknowledged in ${c.escalate_after_min >= 60 ? `${c.escalate_after_min / 60}h` : `${c.escalate_after_min}m`}`}
          {c.remind_every_min > 0 && ` · reminds every ${c.remind_every_min >= 60 ? `${c.remind_every_min / 60}h` : `${c.remind_every_min}m`}`}
        </span>
        <Button variant="outline" size="sm" onClick={() => test.mutate()} disabled={test.isPending}>
          {test.isPending ? <Loader2Icon className="animate-spin" /> : <SendIcon />} Test
        </Button>
      </div>
      {c.type === "webhook" && c.config.secret && (
        <div className="mt-3 flex items-center gap-1 rounded-md border bg-muted/40 py-0.5 pr-0.5 pl-2 text-[11px]">
          <span className="text-muted-foreground">Signing secret</span>
          <code className="min-w-0 flex-1 truncate font-mono">{c.config.secret.slice(0, 8)}…</code>
          <CopyButton value={c.config.secret} />
        </div>
      )}
      <ConfirmDialog open={confirm} onOpenChange={setConfirm} title={`Delete ${c.name}?`} description="No more alerts are sent to it." confirmLabel="Delete" destructive onConfirm={() => remove.mutate()} />
    </Card>
  );
}

function ChannelDialog({ channel, open, onOpenChange }: { channel: Channel | null; open: boolean; onOpenChange: (o: boolean) => void }) {
  const qc = useQueryClient();
  const [type, setType] = useState<ChannelType>("email");
  const [name, setName] = useState("");
  const [to, setTo] = useState("");
  const [url, setUrl] = useState("");
  const [cfg, setCfg] = useState<Record<string, string>>({});
  const [events, setEvents] = useState<AlertEvent[]>(["down", "recovered"]);
  const [all, setAll] = useState(true);
  const [monitorIds, setMonitorIds] = useState<number[]>([]);
  const [remind, setRemind] = useState(0);
  const [escalate, setEscalate] = useState(0);
  const [errors, setErrors] = useState<Record<string, string>>({});

  useEffect(() => {
    if (!open) return;
    setType(channel?.type ?? "email");
    setName(channel?.name ?? "");
    setTo(channel?.config.to?.join(", ") ?? "");
    setUrl(channel?.config.url ?? "");
    setCfg(Object.fromEntries(Object.entries(channel?.config ?? {}).filter(([, v]) => typeof v === "string")) as Record<string, string>);
    setEvents(channel?.events ?? ["down", "recovered"]);
    setAll(channel?.apply_to_all ?? true);
    setMonitorIds(channel?.monitor_ids ?? []);
    setRemind(channel?.remind_every_min ?? 0);
    setEscalate(channel?.escalate_after_min ?? 0);
    setErrors({});
  }, [open, channel]);

  const save = useMutation({
    mutationFn: () => {
      const config =
        type === "email"
          ? { to: [to] }
          : channelFields[type]
            ? Object.fromEntries(channelFields[type]!.map((f) => [f.key, (cfg[f.key] ?? (f.options ? f.options[0].value : "")).trim()]))
            : { url, ...(channel?.config.secret ? { secret: channel.config.secret } : {}) };
      const body = { name: name || channelTypes.find((t) => t.value === type)!.label, type, config, events, apply_to_all: all, monitor_ids: monitorIds, remind_every_min: remind, escalate_after_min: escalate, enabled: channel?.enabled ?? true };
      return channel ? api.put<Channel>(`/channels/${channel.id}`, body) : api.post<Channel>("/channels", body);
    },
    onSuccess: (c) => {
      qc.invalidateQueries({ queryKey: ["channels"] });
      qc.invalidateQueries({ queryKey: ["monitors"] });
      toast.success(channel ? "Channel saved" : "Channel added", { description: "Use “Test” to check it works." });
      onOpenChange(false);
      void c;
    },
    onError: (e) => {
      if (e instanceof ApiError && e.fields) setErrors(e.fields);
      else toast.error(e.message);
    },
  });
  const toggleEvent = (e: AlertEvent) => setEvents((ev) => (ev.includes(e) ? ev.filter((x) => x !== e) : [...ev, e]));

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <form
          className="grid gap-4"
          onSubmit={(e) => {
            e.preventDefault();
            setErrors({});
            save.mutate();
          }}
        >
          <DialogHeader>
            <DialogTitle>{channel ? "Edit channel" : "Add alert channel"}</DialogTitle>
            <DialogDescription>Choose where alerts go and which events trigger them.</DialogDescription>
          </DialogHeader>
          {!channel && (
            <div className="grid grid-cols-5 gap-1.5">
              {channelTypes.map((t) => (
                <button
                  key={t.value}
                  type="button"
                  onClick={() => setType(t.value)}
                  className={cn("flex flex-col items-center gap-1 rounded-lg border px-2 py-2.5 text-[12.5px] font-medium transition-colors hover:bg-muted/50", type === t.value && "border-foreground/60 bg-muted/60")}
                  aria-pressed={type === t.value}
                >
                  <t.icon className="size-4" /> {t.label}
                </button>
              ))}
            </div>
          )}
          <Field label="Name" htmlFor="ch-name" error={errors.name}>
            <Input id="ch-name" value={name} onChange={(e) => setName(e.target.value)} placeholder={type === "email" ? "On-call team" : type === "slack" ? "#ops" : "Ops alerts"} />
          </Field>
          {channelFields[type] ? (
            channelFields[type]!.map((f) => (
              <Field key={f.key} label={f.optional ? `${f.label} (optional)` : f.label} htmlFor={`ch-${f.key}`} error={errors[`config.${f.key}`]} hint={f.hint}>
                {f.options ? (
                  <Select value={cfg[f.key] || f.options[0].value} onValueChange={(v) => setCfg((c) => ({ ...c, [f.key]: v }))}>
                    <SelectTrigger id={`ch-${f.key}`}>
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      {f.options.map((o) => (
                        <SelectItem key={o.value} value={o.value}>
                          {o.label}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                ) : (
                  <Input
                    id={`ch-${f.key}`}
                    className={f.mono ? "font-mono text-[12px]" : undefined}
                    value={cfg[f.key] ?? ""}
                    onChange={(e) => setCfg((c) => ({ ...c, [f.key]: e.target.value }))}
                    placeholder={f.placeholder}
                    autoComplete="off"
                    spellCheck={false}
                  />
                )}
              </Field>
            ))
          ) : type === "email" ? (
            <Field label="Send to" htmlFor="ch-to" error={errors["config.to"]} hint="Separate addresses with commas.">
              <Textarea id="ch-to" rows={2} value={to} onChange={(e) => setTo(e.target.value)} placeholder="oncall@example.com, lead@example.com" />
            </Field>
          ) : (
            <Field
              label="Webhook URL"
              htmlFor="ch-url"
              error={errors["config.url"]}
              hint={
                type === "slack"
                  ? "Slack → Apps → Incoming Webhooks → Add to a channel."
                  : type === "discord"
                    ? "Discord → Channel settings → Integrations → Webhooks."
                    : "We POST JSON with an X-Drix-Signature HMAC-SHA256 header."
              }
            >
              <Input id="ch-url" className="font-mono text-[12px]" value={url} onChange={(e) => setUrl(e.target.value)} placeholder={type === "slack" ? "https://hooks.slack.com/services/…" : type === "discord" ? "https://discord.com/api/webhooks/…" : "https://example.com/drix-hook"} />
            </Field>
          )}
          <div className="grid gap-1.5">
            <Label>Send when</Label>
            <div className="grid gap-1 sm:grid-cols-2">
              {eventOptions.map((o) => (
                <label key={o.value} className="flex cursor-pointer items-start gap-2 rounded-md border px-2.5 py-2 text-[13px] has-[[data-state=checked]]:border-foreground/40">
                  <Checkbox checked={events.includes(o.value)} onCheckedChange={() => toggleEvent(o.value)} className="mt-px" />
                  <span>
                    <span className="block font-medium">{o.label}</span>
                    <span className="block text-[11.5px] text-muted-foreground">{o.hint}</span>
                  </span>
                </label>
              ))}
            </div>
            {errors.events && <p className="text-xs text-destructive">{errors.events}</p>}
          </div>
          <div className="grid gap-2">
            <label className="flex cursor-pointer items-center justify-between gap-3 rounded-lg border px-3 py-2 text-[13px]">
              <span>
                <span className="block font-medium">All monitors</span>
                <span className="block text-xs text-muted-foreground">Including ones added later</span>
              </span>
              <Switch checked={all} onCheckedChange={setAll} />
            </label>
            {!all && (
              <Field error={errors.monitor_ids}>
                <MonitorPicker value={monitorIds} onChange={setMonitorIds} />
              </Field>
            )}
          </div>
          {events.includes("down") && (
            <Field
              label="Send down alerts"
              error={errors.escalate_after_min}
              hint={escalate > 0 ? "Only if nobody acknowledges the outage in time, e.g. to page someone else. Recoveries follow only when it was sent." : undefined}
            >
              <Select value={String(escalate)} onValueChange={(v) => setEscalate(Number(v))}>
                <SelectTrigger className="w-72">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {escalations.map((r) => (
                    <SelectItem key={r.value} value={String(r.value)}>
                      {r.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
          )}
          {events.includes("down") && (
            <Field label="Remind while down">
              <Select value={String(remind)} onValueChange={(v) => setRemind(Number(v))}>
                <SelectTrigger className="w-52">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {reminders.map((r) => (
                    <SelectItem key={r.value} value={String(r.value)}>
                      {r.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
          )}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button type="submit" disabled={save.isPending}>
              {save.isPending && <Loader2Icon className="animate-spin" />}
              {channel ? "Save" : "Add channel"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function Deliveries() {
  const { data: monitors } = useMonitors();
  const { data } = useQuery({ queryKey: ["deliveries"], queryFn: () => api.get<Delivery[]>("/deliveries?limit=50"), refetchInterval: 15_000 });
  return (
    <Card className="mt-6 overflow-hidden">
      <CardHeader>
        <div>
          <CardTitle>Recent deliveries</CardTitle>
          <CardDescription>Every alert that was sent or failed. Failed sends are retried up to 5 times.</CardDescription>
        </div>
      </CardHeader>
      {!data?.length ? (
        <CardContent className="text-[13px] text-muted-foreground">Nothing sent yet.</CardContent>
      ) : (
        <Table>
          <TableHeader>
            <TableRow className="hover:bg-transparent">
              <TableHead className="pl-4">When</TableHead>
              <TableHead>Channel</TableHead>
              <TableHead>Alert</TableHead>
              <TableHead>Result</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {data.map((d) => (
              <TableRow key={d.id}>
                <TableCell className="pl-4 text-xs text-muted-foreground" title={formatDateTime(d.created_at)}>
                  {timeAgo(d.created_at)}
                </TableCell>
                <TableCell className="text-xs">{d.channel_name || "—"}</TableCell>
                <TableCell className="max-w-xs">
                  <p className="truncate text-[13px]">{d.title}</p>
                  {d.monitor_id && (
                    <Link to={`/monitors/${d.monitor_id}`} className="text-[11px] text-muted-foreground hover:underline">
                      {monitors?.find((m) => m.id === d.monitor_id)?.name ?? `Monitor #${d.monitor_id}`}
                    </Link>
                  )}
                </TableCell>
                <TableCell className="max-w-xs">
                  <Badge variant={d.status === "sent" ? "up" : "down"}>{d.status === "sent" ? "Sent" : "Failed"}</Badge>
                  {d.attempts > 1 && <span className="ml-1.5 text-[11px] text-muted-foreground">after {d.attempts} tries</span>}
                  {d.error && <p className="mt-0.5 truncate font-mono text-[11px] text-destructive">{d.error}</p>}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </Card>
  );
}
