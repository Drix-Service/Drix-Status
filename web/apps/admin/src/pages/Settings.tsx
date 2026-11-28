import { useEffect, useState } from "react";
import { useLocation } from "react-router";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { ArrowUpRightIcon, Loader2Icon, SearchIcon, XIcon } from "lucide-react";
import { Button } from "@drix/ui/components/button";
import { Badge, Card, Skeleton } from "@drix/ui/components/display";
import { Field, Input, Switch, Textarea } from "@drix/ui/components/form";
import { api, ApiError, type EmailSettings, type Settings } from "../api";
import { useQuery } from "@tanstack/react-query";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@drix/ui/components/menu";
import { keys, useBranding, useSettings } from "../lib/queries";
import type { Page } from "../api";
import { PageHeader } from "../components/Page";
import { useCan } from "../lib/roles";
import { ApiKeysSection } from "../components/ApiKeys";
import { BackupsSection } from "../components/Backups";
import { TwoFactorPanel } from "../components/TwoFactor";
import { DeviceNotificationsSection } from "../components/DeviceNotifications";
import { AutoIncidentsSection } from "../components/AutoIncidents";

export default function SettingsPage() {
  const { data, isLoading } = useSettings();
  const qc = useQueryClient();
  const can = useCan();
  const canAdmin = can("admin");
  const canEditor = can("editor");
  const [s, setS] = useState<Settings | null>(null);
  const [errors, setErrors] = useState<Record<string, string>>({});

  useEffect(() => {
    if (data) setS(data);
  }, [data]);

  // Links like /settings#backups jump to their section once the page is drawn.
  const { hash } = useLocation();
  const ready = !isLoading && !!s;
  useEffect(() => {
    if (!ready || !hash) return;
    const t = window.setTimeout(() => document.getElementById(hash.slice(1))?.scrollIntoView({ behavior: "smooth", block: "start" }), 50);
    return () => window.clearTimeout(t);
  }, [ready, hash]);

  const save = useMutation({
    mutationFn: (v: Settings) => api.put<Settings>("/settings", v),
    onSuccess: (v) => {
      qc.setQueryData(keys.settings, v);
      setErrors({});
      toast.success("Settings saved");
    },
    onError: (e) => {
      if (e instanceof ApiError && e.fields) setErrors(e.fields);
      toast.error(e.message);
    },
  });

  if (isLoading || !s) {
    return (
      <div className="grid gap-6">
        <Skeleton className="h-8 w-48" />
        <Skeleton className="h-48 rounded-xl" />
      </div>
    );
  }

  const set = <K extends keyof Settings>(k: K, v: Settings[K]) => setS((p) => (p ? { ...p, [k]: v } : p));
  const dirty = JSON.stringify(s) !== JSON.stringify(data);

  return (
    <>
    <form
      onSubmit={(e) => {
        e.preventDefault();
        save.mutate(s);
      }}
    >
      <PageHeader
        title="Settings"
        description="Your status page, email, subscribers, API keys, backups and monitoring defaults."
        actions={
          canAdmin && (
            <Button type="submit" disabled={!dirty || save.isPending}>
              {save.isPending && <Loader2Icon className="animate-spin" />}
              Save changes
            </Button>
          )
        }
      />
      {!canAdmin && (
        <p className="mb-4 rounded-lg border bg-muted/40 px-3 py-2 text-[13px] text-muted-foreground">Only admins and owners can change settings.</p>
      )}
      <fieldset disabled={!canAdmin} className="grid gap-6">
        <Section title="Monitoring defaults" description="Applied to new monitors. Existing monitors keep their own values.">
          <div className="grid gap-4 sm:grid-cols-3">
            <Field label="Check interval (s)" error={errors.default_interval_s}>
              <Input type="number" min={5} value={s.default_interval_s} onChange={(e) => set("default_interval_s", Number(e.target.value))} />
            </Field>
            <Field label="Timeout (ms)" error={errors.default_timeout_ms}>
              <Input type="number" min={100} value={s.default_timeout_ms} onChange={(e) => set("default_timeout_ms", Number(e.target.value))} />
            </Field>
            <Field label="Keep raw results (days)" error={errors.retention_days} hint="Daily uptime is kept for good.">
              <Input type="number" min={1} max={365} value={s.retention_days} onChange={(e) => set("retention_days", Number(e.target.value))} />
            </Field>
          </div>
        </Section>
      </fieldset>
    </form>
    <div className="mt-6 grid gap-6">
      <div id="notifications" className="scroll-mt-20">
        <DeviceNotificationsSection Section={Section} />
      </div>
      <div id="two-factor" className="scroll-mt-20">
        <Section title="Two-factor sign-in" description="Your own account. A passkey or an authenticator app keeps the admin safe even if your password leaks.">
          <TwoFactorPanel />
        </Section>
      </div>
      <BrandingSection canEdit={canAdmin} />
      <AutoIncidentsSection Section={Section} canEdit={canAdmin} />
      {canAdmin && <EmailSettingsSection />}
      {canEditor && <SubscribersSection />}
      {canAdmin && <ApiKeysSection Section={Section} />}
      {canAdmin && (
        <div id="backups" className="scroll-mt-20">
          <BackupsSection Section={Section} />
        </div>
      )}
    </div>
    </>
  );
}

function Section({ title, description, children }: { title: string; description: string; children: React.ReactNode }) {
  return (
    <Card className="grid gap-6 p-5 md:grid-cols-[16rem_1fr]">
      <div>
        <h2 className="text-[14px] font-semibold tracking-tight">{title}</h2>
        <p className="mt-1 text-[13px] text-muted-foreground">{description}</p>
      </div>
      <div className="grid min-w-0 gap-4">{children}</div>
    </Card>
  );
}

function EmailSettingsSection() {
  const qc = useQueryClient();
  const { data } = useQuery({ queryKey: ["email-settings"], queryFn: () => api.get<EmailSettings>("/settings/email") });
  const [f, setF] = useState({ host: "", port: 587, username: "", password: "", from: "", tls: "starttls" as EmailSettings["tls"] });
  const [pwTouched, setPwTouched] = useState(false);
  const [testTo, setTestTo] = useState("");
  const [errors, setErrors] = useState<Record<string, string>>({});
  useEffect(() => {
    if (data) setF({ host: data.host, port: data.port || 587, username: data.username, password: "", from: data.from, tls: data.tls || "starttls" });
  }, [data]);
  const save = useMutation({
    mutationFn: () => api.put<EmailSettings>("/settings/email", { ...f, password: pwTouched ? f.password : undefined }),
    onSuccess: (d) => {
      qc.setQueryData(["email-settings"], d);
      setPwTouched(false);
      setErrors({});
      toast.success("Email settings saved");
    },
    onError: (e) => {
      if (e instanceof ApiError && e.fields) setErrors(e.fields);
      toast.error(e.message);
    },
  });
  const test = useMutation({
    mutationFn: () => api.post<{ to: string }>("/settings/email/test", { to: testTo }),
    onSuccess: (r) => toast.success(`Test email sent to ${r.to}`),
    onError: (e) => toast.error(e.message),
  });
  const set = <K extends keyof typeof f>(k: K, v: (typeof f)[K]) => setF((p) => ({ ...p, [k]: v }));
  return (
    <Section title="Email" description="Used for email alerts, team invites and subscriber updates. Works with any SMTP provider.">
      {data?.source === "env" && (
        <p className="rounded-md border bg-muted/40 px-3 py-2 text-xs text-muted-foreground">
          Currently using SMTP_URL from the server environment ({data.host}:{data.port}). Saving here overrides it.
        </p>
      )}
      {data && !data.can_store_secrets && (
        <p className="rounded-md border border-degraded/40 bg-degraded/10 px-3 py-2 text-xs">Set SECRET_KEY on the server to store a password.</p>
      )}
      <div className="grid gap-4 sm:grid-cols-[1fr_6rem_9rem]">
        <Field label="SMTP server" error={errors.host}>
          <Input value={f.host} onChange={(e) => set("host", e.target.value)} placeholder="smtp.example.com" />
        </Field>
        <Field label="Port" error={errors.port}>
          <Input type="number" value={f.port} onChange={(e) => set("port", Number(e.target.value))} />
        </Field>
        <Field label="Security" error={errors.tls}>
          <Select value={f.tls} onValueChange={(v) => set("tls", v as EmailSettings["tls"])}>
            <SelectTrigger>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="starttls">STARTTLS</SelectItem>
              <SelectItem value="tls">TLS</SelectItem>
              <SelectItem value="none">None</SelectItem>
            </SelectContent>
          </Select>
        </Field>
      </div>
      <div className="grid gap-4 sm:grid-cols-2">
        <Field label="Username">
          <Input value={f.username} onChange={(e) => set("username", e.target.value)} autoComplete="off" />
        </Field>
        <Field label="Password" hint={data?.has_password && !pwTouched ? "Saved. Type to replace it." : undefined}>
          <Input
            type="password"
            autoComplete="new-password"
            value={f.password}
            placeholder={data?.has_password ? "••••••••" : ""}
            onChange={(e) => {
              setPwTouched(true);
              set("password", e.target.value);
            }}
          />
        </Field>
      </div>
      <Field label="From" error={errors.from} hint="For example: Acme Status <status@acme.com>">
        <Input value={f.from} onChange={(e) => set("from", e.target.value)} />
      </Field>
      <div className="flex flex-wrap items-center gap-2">
        <Button type="button" onClick={() => save.mutate()} disabled={save.isPending}>
          {save.isPending && <Loader2Icon className="animate-spin" />}
          Save email settings
        </Button>
        <div className="ml-auto flex items-center gap-2">
          <Input className="w-56" value={testTo} onChange={(e) => setTestTo(e.target.value)} placeholder="Send test to (default: you)" />
          <Button type="button" variant="outline" onClick={() => test.mutate()} disabled={test.isPending}>
            {test.isPending && <Loader2Icon className="animate-spin" />}
            Send test
          </Button>
        </div>
      </div>
    </Section>
  );
}

const swatches = ["#2563eb", "#4f46e5", "#0d9488", "#16a34a", "#ea580c", "#db2777", "#18181b"];

const pageLanguages: [string, string][] = [
  ["en", "English"], ["es", "Español (Spanish)"], ["fr", "Français (French)"], ["de", "Deutsch (German)"],
  ["pt", "Português (Portuguese)"], ["it", "Italiano (Italian)"], ["nl", "Nederlands (Dutch)"], ["pl", "Polski (Polish)"],
  ["da", "Dansk (Danish)"], ["sv", "Svenska (Swedish)"], ["nb", "Norsk (Norwegian)"], ["fi", "Suomi (Finnish)"],
  ["ja", "日本語 (Japanese)"], ["zh", "中文 (Chinese, Simplified)"], ["ko", "한국어 (Korean)"],
];

function BrandingSection({ canEdit }: { canEdit: boolean }) {
  const qc = useQueryClient();
  const { data } = useBranding();
  const [f, setF] = useState<Page | null>(null);
  const [errors, setErrors] = useState<Record<string, string>>({});
  useEffect(() => {
    if (data) setF(data.page);
  }, [data]);
  const save = useMutation({
    mutationFn: (p: Page) =>
      api.put<{ page: Page; url: string }>("/branding", {
        name: p.name, description: p.description, logo_url: p.logo_url, website_url: p.website_url,
        accent_color: p.accent_color, footer_text: p.footer_text, show_latency: p.show_latency, language: p.language,
      }),
    onSuccess: (d) => {
      qc.setQueryData(keys.branding, d);
      setErrors({});
      toast.success("Status page updated", { description: "Visitors see the change within a second." });
    },
    onError: (e) => {
      if (e instanceof ApiError && e.fields) setErrors(e.fields);
      toast.error(e.message);
    },
  });
  if (!f || !data) return <Skeleton className="h-64 rounded-xl" />;
  const set = <K extends keyof Page>(k: K, v: Page[K]) => setF((p) => (p ? { ...p, [k]: v } : p));
  const dirty = JSON.stringify(f) !== JSON.stringify(data.page);
  return (
    <Section title="Status page" description="Name, logo and colors visitors see. Arrange monitors under Groups.">
      <fieldset disabled={!canEdit} className="grid gap-4">
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="Name" error={errors.name}>
            <Input value={f.name} onChange={(e) => set("name", e.target.value)} />
          </Field>
          <Field label="Website URL" error={errors.website_url} hint="Linked from the header.">
            <Input value={f.website_url} onChange={(e) => set("website_url", e.target.value)} placeholder="https://example.com" />
          </Field>
        </div>
        <Field label="Description" error={errors.description}>
          <Input value={f.description} onChange={(e) => set("description", e.target.value)} placeholder="Live status of our services" />
        </Field>
        <Field label="Logo URL" error={errors.logo_url} hint="SVG or PNG, about 32px tall. Leave empty to show your initial.">
          <Input value={f.logo_url} onChange={(e) => set("logo_url", e.target.value)} placeholder="https://example.com/logo.svg" />
        </Field>
        <Field label="Accent color" error={errors.accent_color}>
          <div className="flex flex-wrap items-center gap-2">
            {swatches.map((c) => (
              <button
                key={c}
                type="button"
                onClick={() => set("accent_color", c)}
                className="size-7 rounded-full border-2 border-background ring-1 ring-border transition-transform hover:scale-110 data-[on=true]:ring-2 data-[on=true]:ring-foreground"
                data-on={f.accent_color.toLowerCase() === c}
                style={{ background: c }}
                aria-label={`Use ${c}`}
              />
            ))}
            <Input value={f.accent_color} onChange={(e) => set("accent_color", e.target.value)} className="ml-1 w-28 font-mono text-xs" maxLength={7} />
          </div>
        </Field>
        <Field
          label="Default language"
          error={errors.language}
          hint="Visitors see the page in their browser's language when it's one of these, and can switch in the footer. This one is used otherwise."
        >
          <Select value={f.language || "en"} onValueChange={(v) => set("language", v)}>
            <SelectTrigger className="sm:w-64">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {pageLanguages.map(([code, name]) => (
                <SelectItem key={code} value={code}>
                  {name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
        <Field label="Footer text" error={errors.footer_text}>
          <Textarea rows={2} value={f.footer_text} onChange={(e) => set("footer_text", e.target.value)} placeholder="© Your company" />
        </Field>
        <label className="flex cursor-pointer items-center justify-between gap-4 rounded-lg border px-3 py-2.5">
          <span>
            <span className="block text-[13px] font-medium">Show response times</span>
            <span className="block text-xs text-muted-foreground">Display the latest latency next to each service.</span>
          </span>
          <Switch checked={f.show_latency} onCheckedChange={(v) => set("show_latency", v)} />
        </label>
      </fieldset>
      <div className="flex flex-wrap items-center gap-2">
        {canEdit && (
          <Button type="button" onClick={() => save.mutate(f)} disabled={!dirty || save.isPending}>
            {save.isPending && <Loader2Icon className="animate-spin" />}
            Save status page
          </Button>
        )}
        <Button asChild variant="outline">
          <a href={data.url} target="_blank" rel="noreferrer">
            View <ArrowUpRightIcon />
          </a>
        </Button>
      </div>
    </Section>
  );
}

type SubscriberRow = { id: number; email: string; confirmed_at: string | null; created_at: string };

function SubscribersSection() {
  const qc = useQueryClient();
  const [q, setQ] = useState("");
  const { data } = useQuery({
    queryKey: ["subscribers", q],
    queryFn: () => api.get<{ subscribers: SubscriberRow[]; confirmed: number; pending: number }>(`/subscribers?q=${encodeURIComponent(q)}`),
  });
  const remove = useMutation({
    mutationFn: (id: number) => api.del(`/subscribers/${id}`),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["subscribers"] });
      toast.success("Subscriber removed");
    },
  });
  return (
    <Section
      title="Subscribers"
      description={`People who get emails about incidents and maintenance. ${data ? `${data.confirmed} confirmed, ${data.pending} waiting to confirm.` : ""} Unconfirmed signups expire after 7 days.`}
    >
      <div className="flex flex-wrap items-center gap-2">
        <div className="relative">
          <SearchIcon className="pointer-events-none absolute top-1/2 left-2 size-3.5 -translate-y-1/2 text-muted-foreground" />
          <Input value={q} onChange={(e) => setQ(e.target.value)} placeholder="Search email" className="h-8 w-56 pl-7 text-xs" />
        </div>
        <Button asChild variant="outline" size="sm" className="ml-auto">
          <a href="/api/admin/subscribers.csv">Export CSV</a>
        </Button>
      </div>
      {!data?.subscribers.length ? (
        <p className="rounded-lg border border-dashed px-4 py-6 text-center text-[13px] text-muted-foreground">
          {q ? "No matches." : "No subscribers yet. Visitors subscribe with the button on your status page."}
        </p>
      ) : (
        <ul className="divide-y rounded-lg border">
          {data.subscribers.map((s) => (
            <li key={s.id} className="flex items-center gap-3 px-3 py-2 text-[13px]">
              <span className="min-w-0 flex-1 truncate">{s.email}</span>
              <Badge variant={s.confirmed_at ? "up" : "muted"}>{s.confirmed_at ? "Confirmed" : "Pending"}</Badge>
              <span className="hidden w-24 text-right text-xs text-muted-foreground sm:block">{new Date(s.created_at).toLocaleDateString()}</span>
              <Button variant="ghost" size="icon-sm" aria-label={`Remove ${s.email}`} onClick={() => remove.mutate(s.id)} className="hover:text-destructive">
                <XIcon />
              </Button>
            </li>
          ))}
        </ul>
      )}
    </Section>
  );
}
