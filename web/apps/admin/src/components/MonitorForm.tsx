import { useEffect, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useCan } from "../lib/roles";
import { toast } from "sonner";
import { CheckCircle2Icon, FlaskConicalIcon, Loader2Icon, XCircleIcon, XIcon } from "lucide-react";
import { cn, formatLatency } from "@drix/ui/lib/utils";
import { Button } from "@drix/ui/components/button";
import { Checkbox, Field, Input, Label, Switch, Textarea } from "@drix/ui/components/form";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@drix/ui/components/menu";
import { Sheet, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "@drix/ui/components/overlay";
import { MonitorTypeIcon } from "@drix/ui/drix/status";
import { api, ApiError, type CheckResult, type Monitor, type MonitorConfig, type MonitorInput, type MonitorType, type JSONAssertion, type Channel } from "../api";
import { keys, useAgents, useGroups, useSettings } from "../lib/queries";
import { Link, useNavigate } from "react-router";

const types: { value: MonitorType; label: string; hint: string; placeholder: string; target: string }[] = [
  { value: "http", label: "HTTP", hint: "Website or API", placeholder: "https://api.example.com/health", target: "URL" },
  { value: "json", label: "JSON API", hint: "Check response values", placeholder: "https://api.example.com/status", target: "URL" },
  { value: "tcp", label: "TCP", hint: "Open port", placeholder: "db.internal:5432", target: "Host" },
  { value: "ping", label: "Ping", hint: "Host reachable", placeholder: "10.0.0.12 or host.example.com", target: "Host" },
  { value: "dns", label: "DNS", hint: "Record resolves", placeholder: "example.com", target: "Domain" },
  { value: "ssl", label: "SSL", hint: "Certificate expiry", placeholder: "example.com or example.com:8443", target: "Host" },
  { value: "smtp", label: "SMTP", hint: "Mail server", placeholder: "mail.example.com:587", target: "Mail server" },
  { value: "grpc", label: "gRPC", hint: "Health service", placeholder: "api.internal:50051", target: "Address" },
  { value: "push", label: "Heartbeat", hint: "Cron jobs ping us", placeholder: "", target: "" },
];

const assertionOps: { value: JSONAssertion["op"]; label: string; needsValue: boolean }[] = [
  { value: "equals", label: "equals", needsValue: true },
  { value: "not_equals", label: "doesn't equal", needsValue: true },
  { value: "contains", label: "contains", needsValue: true },
  { value: "gt", label: "greater than", needsValue: true },
  { value: "lt", label: "less than", needsValue: true },
  { value: "exists", label: "exists", needsValue: false },
  { value: "not_exists", label: "doesn't exist", needsValue: false },
];

const intervals = [10, 30, 60, 120, 300, 600];
const fmtInterval = (s: number) => (s < 60 ? `${s}s` : `${s / 60}m`);

type FormState = Omit<MonitorInput, "config"> & { config: MonitorConfig; headersText: string };

function initial(m: Monitor | null, defaults: { interval: number; timeout: number }, sectionId?: number | null): FormState {
  if (m) {
    return {
      name: m.name,
      description: m.description,
      type: m.type,
      target: m.target,
      config: { ...m.config },
      interval_s: m.interval_s,
      timeout_ms: m.timeout_ms,
      retries: m.retries,
      degraded_ms: m.degraded_ms,
      enabled: m.enabled,
      multi_location: m.multi_location,
      show_locations: m.show_locations,
      auto_incident: m.auto_incident,
      channel_ids: m.channel_ids ?? [],
      headersText: Object.entries(m.config.headers ?? {})
        .map(([k, v]) => `${k}: ${v}`)
        .join("\n"),
    };
  }
  return {
    section_id: sectionId ?? null,
    name: "",
    description: "",
    type: "http",
    target: "",
    config: {},
    interval_s: defaults.interval,
    timeout_ms: defaults.timeout,
    retries: 1,
    degraded_ms: 0,
    enabled: true,
    multi_location: false,
    show_locations: true,
    auto_incident: true,
    channel_ids: [],
    headersText: "",
  };
}

/** Strip config keys that don't belong to the chosen type. */
function toInput(f: FormState): MonitorInput {
  const c = f.config;
  let config: MonitorConfig = {};
  switch (f.type) {
    case "http":
    case "json": {
      const headers: Record<string, string> = {};
      f.headersText.split("\n").forEach((line) => {
        const i = line.indexOf(":");
        if (i > 0) headers[line.slice(0, i).trim()] = line.slice(i + 1).trim();
      });
      config = {
        method: c.method || undefined,
        expect_status_min: c.expect_status_min || undefined,
        expect_status_max: c.expect_status_max || undefined,
        keyword: c.keyword || undefined,
        keyword_absent: c.keyword_absent || undefined,
        headers: Object.keys(headers).length ? headers : undefined,
        body: c.body || undefined,
        follow_redirects: c.follow_redirects === false ? false : undefined,
        ignore_tls: c.ignore_tls || undefined,
      };
      if (f.type === "json") config.assertions = (c.assertions ?? []).filter((a) => a.path.trim());
      break;
    }
    case "ssl":
      config = { warn_days: c.warn_days || undefined, crit_days: c.crit_days || undefined, server_name: c.server_name || undefined };
      break;
    case "smtp":
      config = { starttls: c.starttls || undefined, ignore_tls: c.ignore_tls || undefined };
      break;
    case "grpc":
      config = { service: c.service || undefined, tls: c.tls || undefined, ignore_tls: c.ignore_tls || undefined };
      break;
    case "push":
      config = { grace_s: c.grace_s || undefined };
      break;
    case "tcp":
      config = { port: c.port || undefined };
      break;
    case "ping":
      config = { count: c.count || undefined };
      break;
    case "dns":
      config = { record_type: c.record_type || undefined, resolver: c.resolver || undefined, expect: c.expect || undefined };
      break;
  }
  const { headersText: _h, ...rest } = f;
  void _h;
  return { ...rest, config: JSON.parse(JSON.stringify(config)) };
}

export function MonitorSheet({
  open,
  onOpenChange,
  monitor,
  defaultSectionId,
}: {
  open: boolean;
  onOpenChange: (o: boolean) => void;
  monitor: Monitor | null;
  defaultSectionId?: number | null;
}) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const { data: groupsData } = useGroups();
  const groups = groupsData?.groups;
  const canAdmin = useCan()("admin");
  const { data: channels } = useQuery({ queryKey: ["channels"], queryFn: () => api.get<Channel[]>("/channels"), enabled: canAdmin });
  const { data: agentsData } = useAgents();
  const locations = [...new Set((agentsData?.agents ?? []).filter((a) => a.online && !a.disabled).map((a) => a.region.trim() || a.name))].sort();
  const { data: settings } = useSettings();
  const defaults = useMemo(
    () => ({ interval: settings?.default_interval_s ?? 60, timeout: settings?.default_timeout_ms ?? 10000 }),
    [settings],
  );
  const [f, setF] = useState<FormState>(() => initial(monitor, defaults, defaultSectionId));
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [test, setTest] = useState<{ result: CheckResult; agent: string } | { error: string } | null>(null);

  useEffect(() => {
    if (open) {
      setF(initial(monitor, defaults, defaultSectionId));
      setErrors({});
      setTest(null);
    }
  }, [open, monitor, defaults, defaultSectionId]);

  const set = <K extends keyof FormState>(k: K, v: FormState[K]) => setF((p) => ({ ...p, [k]: v }));
  const setC = <K extends keyof MonitorConfig>(k: K, v: MonitorConfig[K]) => setF((p) => ({ ...p, config: { ...p.config, [k]: v } }));

  const save = useMutation({
    mutationFn: (input: MonitorInput) =>
      monitor ? api.put<Monitor>(`/monitors/${monitor.id}`, input) : api.post<Monitor>("/monitors", input),
    onSuccess: (m) => {
      toast.success(monitor ? "Monitor updated" : "Monitor created", { description: m.name });
      qc.invalidateQueries({ queryKey: keys.monitors });
      qc.invalidateQueries({ queryKey: keys.monitor(m.id) });
      qc.invalidateQueries({ queryKey: keys.overview });
      qc.invalidateQueries({ queryKey: keys.groups });
      onOpenChange(false);
      if (!monitor && m.type === "push") navigate(`/monitors/${m.id}`);
    },
    onError: (err) => {
      if (err instanceof ApiError && err.fields) setErrors(err.fields);
      else toast.error(err instanceof Error ? err.message : "Could not save");
    },
  });

  const runTest = useMutation({
    mutationFn: (input: MonitorInput) => api.post<{ result: CheckResult; agent: string }>("/monitors/test", input),
    onMutate: () => setTest(null),
    onSuccess: (r) => setTest(r),
    onError: (err) => {
      if (err instanceof ApiError && err.fields) setErrors(err.fields);
      setTest({ error: err instanceof Error ? err.message : "Test failed" });
    },
  });

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    setErrors({});
    save.mutate(toInput(f));
  };

  const typeMeta = types.find((t) => t.value === f.type)!;

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent className="sm:max-w-[34rem]">
        <form onSubmit={submit} className="flex h-full flex-col">
          <SheetHeader>
            <SheetTitle>{monitor ? "Edit monitor" : "New monitor"}</SheetTitle>
            <SheetDescription>
              {monitor ? "Changes are pushed to workers instantly." : "Checks start within seconds of saving."}
            </SheetDescription>
          </SheetHeader>

          <div className="flex-1 overflow-y-auto px-5 py-5">
            <div className="grid gap-6">
              {/* Type */}
              <div className="grid gap-2">
                <Label>Check type</Label>
                <div className="grid grid-cols-3 gap-2">
                  {types.map((t) => (
                    <button
                      type="button"
                      key={t.value}
                      onClick={() => set("type", t.value)}
                      className={cn(
                        "flex flex-col items-start gap-1.5 rounded-lg border p-2.5 text-left transition-colors hover:bg-muted/50",
                        f.type === t.value && "border-foreground/60 bg-muted/60 ring-1 ring-foreground/10",
                      )}
                      aria-pressed={f.type === t.value}
                    >
                      <MonitorTypeIcon type={t.value} className={cn(f.type === t.value && "text-foreground")} />
                      <span className="text-[13px] font-medium">{t.label}</span>
                      <span className="hidden text-[11px] leading-tight text-muted-foreground sm:block">{t.hint}</span>
                    </button>
                  ))}
                </div>
              </div>

              <Section title="General">
                <Field label="Name" htmlFor="m-name" error={errors.name}>
                  <Input id="m-name" value={f.name} onChange={(e) => set("name", e.target.value)} placeholder="Production API" aria-invalid={!!errors.name} />
                </Field>
                {f.type !== "push" && <Field
                  label={typeMeta.target}
                  htmlFor="m-target"
                  error={errors.target}
                  hint={f.type === "tcp" ? "Use host:port, or set the port below." : undefined}
                >
                  <Input
                    id="m-target"
                    className="font-mono text-[12.5px]"
                    value={f.target}
                    onChange={(e) => set("target", e.target.value)}
                    placeholder={typeMeta.placeholder}
                    aria-invalid={!!errors.target}
                    spellCheck={false}
                    autoCapitalize="off"
                  />
                </Field>}
                <div className="grid gap-4 sm:grid-cols-2">
                  {monitor ? (
                    <Field label="Groups" hint="Move it between groups on the Groups page.">
                      <div className="flex min-h-8 flex-wrap items-center gap-1.5">
                        {monitor.groups.length ? (
                          monitor.groups.map((g) => (
                            <Link key={g.id} to="/groups" className="rounded-md border bg-muted/40 px-2 py-0.5 text-xs hover:bg-muted">
                              {g.name}
                            </Link>
                          ))
                        ) : (
                          <span className="text-xs text-muted-foreground">Not on the status page (private)</span>
                        )}
                      </div>
                    </Field>
                  ) : (
                    <Field label="Group" error={errors.section_id} hint="Where it shows on the status page.">
                      <Select value={f.section_id ? String(f.section_id) : "none"} onValueChange={(v) => set("section_id", v === "none" ? null : Number(v))}>
                        <SelectTrigger>
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                          <SelectItem value="none">No group (private)</SelectItem>
                          {groups?.map((g) => (
                            <SelectItem key={g.id} value={String(g.id)}>
                              {g.name}
                            </SelectItem>
                          ))}
                        </SelectContent>
                      </Select>
                    </Field>
                  )}
                  <Field label="Description" hint="Shown on the status page.">
                    <Input value={f.description} onChange={(e) => set("description", e.target.value)} placeholder="Optional" />
                  </Field>
                </div>
              </Section>

              {f.type === "json" && <AssertionsEditor assertions={f.config.assertions ?? []} error={errors["config.assertions"]} onChange={(a) => setC("assertions", a)} />}

              {f.type === "ssl" && (
                <Section title="Certificate">
                  <div className="grid grid-cols-2 gap-3">
                    <Field label="Warn below (days)" error={errors["config.warn_days"]} hint="Shown as degraded">
                      <Input type="number" min={1} max={365} placeholder="14" value={f.config.warn_days ?? ""} onChange={(e) => setC("warn_days", Number(e.target.value) || undefined)} />
                    </Field>
                    <Field label="Down below (days)" hint="Shown as down">
                      <Input type="number" min={0} max={365} placeholder="7" value={f.config.crit_days ?? ""} onChange={(e) => setC("crit_days", Number(e.target.value) || undefined)} />
                    </Field>
                  </div>
                  <Field label="Server name (SNI)" hint="Only if it differs from the host.">
                    <Input className="font-mono text-[12.5px]" value={f.config.server_name ?? ""} onChange={(e) => setC("server_name", e.target.value)} placeholder="Optional" />
                  </Field>
                </Section>
              )}

              {f.type === "smtp" && (
                <Section title="Mail server">
                  <div className="grid gap-3 sm:grid-cols-2">
                    <Toggle label="Require STARTTLS" checked={!!f.config.starttls} onChange={(v) => setC("starttls", v)} />
                    <Toggle label="Ignore TLS errors" checked={!!f.config.ignore_tls} onChange={(v) => setC("ignore_tls", v)} />
                  </div>
                </Section>
              )}

              {f.type === "grpc" && (
                <Section title="Health check">
                  <Field label="Service name" hint="Leave empty to check the whole server (grpc.health.v1).">
                    <Input className="font-mono text-[12.5px]" value={f.config.service ?? ""} onChange={(e) => setC("service", e.target.value)} placeholder="e.g. billing.v1.Billing" />
                  </Field>
                  <div className="grid gap-3 sm:grid-cols-2">
                    <Toggle label="Use TLS" checked={!!f.config.tls} onChange={(v) => setC("tls", v)} />
                    <Toggle label="Ignore TLS errors" checked={!!f.config.ignore_tls} onChange={(v) => setC("ignore_tls", v)} />
                  </div>
                </Section>
              )}

              {f.type === "push" && (
                <Section title="Heartbeat">
                  <p className="text-[13px] text-muted-foreground">
                    Your job calls a unique URL every time it runs. If no call arrives within the interval plus the grace period, the monitor goes down.
                    {!monitor && " You'll get the URL after saving."}
                  </p>
                  <Field label="Grace period (seconds)" error={errors["config.grace_s"]} hint="Extra time for slow runs.">
                    <Input type="number" min={0} max={86400} className="w-32" placeholder="0" value={f.config.grace_s ?? ""} onChange={(e) => setC("grace_s", Number(e.target.value) || undefined)} />
                  </Field>
                </Section>
              )}

              {(f.type === "http" || f.type === "json") && (
                <Section title="Request">
                  <div className="grid grid-cols-[7rem_1fr_1fr] gap-3">
                    <Field label="Method">
                      <Select value={f.config.method || "GET"} onValueChange={(v) => setC("method", v)}>
                        <SelectTrigger>
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                          {["GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"].map((m) => (
                            <SelectItem key={m} value={m}>
                              {m}
                            </SelectItem>
                          ))}
                        </SelectContent>
                      </Select>
                    </Field>
                    <Field label="Status from" error={errors["config.expect_status_min"]}>
                      <Input type="number" min={100} max={599} placeholder="200" value={f.config.expect_status_min ?? ""} onChange={(e) => setC("expect_status_min", Number(e.target.value) || undefined)} />
                    </Field>
                    <Field label="Status to">
                      <Input type="number" min={100} max={599} placeholder="399" value={f.config.expect_status_max ?? ""} onChange={(e) => setC("expect_status_max", Number(e.target.value) || undefined)} />
                    </Field>
                  </div>
                  <div className="grid gap-4 sm:grid-cols-2">
                    <Field label="Must contain" hint="Fail if the body lacks this text.">
                      <Input value={f.config.keyword ?? ""} onChange={(e) => setC("keyword", e.target.value)} placeholder="Optional" />
                    </Field>
                    <Field label="Must not contain" hint="Fail if the body has this text.">
                      <Input value={f.config.keyword_absent ?? ""} onChange={(e) => setC("keyword_absent", e.target.value)} placeholder="Optional" />
                    </Field>
                  </div>
                  <Field label="Headers" hint="One per line, e.g. Authorization: Bearer …">
                    <Textarea className="font-mono text-[12px]" rows={2} value={f.headersText} onChange={(e) => set("headersText", e.target.value)} spellCheck={false} />
                  </Field>
                  {["POST", "PUT", "PATCH"].includes(f.config.method ?? "") && (
                    <Field label="Body">
                      <Textarea className="font-mono text-[12px]" rows={3} value={f.config.body ?? ""} onChange={(e) => setC("body", e.target.value)} spellCheck={false} />
                    </Field>
                  )}
                  <div className="grid gap-3 sm:grid-cols-2">
                    <Toggle label="Follow redirects" checked={f.config.follow_redirects !== false} onChange={(v) => setC("follow_redirects", v)} />
                    <Toggle label="Ignore TLS errors" checked={!!f.config.ignore_tls} onChange={(v) => setC("ignore_tls", v)} />
                  </div>
                </Section>
              )}

              {f.type === "tcp" && (
                <Section title="Connection">
                  <Field label="Port" error={errors["config.port"]} hint="Leave empty if the host includes a port.">
                    <Input type="number" min={1} max={65535} className="w-32" value={f.config.port ?? ""} onChange={(e) => setC("port", Number(e.target.value) || undefined)} />
                  </Field>
                </Section>
              )}

              {f.type === "ping" && (
                <Section title="Ping">
                  <Field label="Packets per check" error={errors["config.count"]}>
                    <Input type="number" min={1} max={10} className="w-32" placeholder="3" value={f.config.count ?? ""} onChange={(e) => setC("count", Number(e.target.value) || undefined)} />
                  </Field>
                </Section>
              )}

              {f.type === "dns" && (
                <Section title="Query">
                  <div className="grid grid-cols-[7rem_1fr] gap-3">
                    <Field label="Record" error={errors["config.record_type"]}>
                      <Select value={f.config.record_type || "A"} onValueChange={(v) => setC("record_type", v)}>
                        <SelectTrigger>
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                          {["A", "AAAA", "CNAME", "MX", "TXT", "NS"].map((r) => (
                            <SelectItem key={r} value={r}>
                              {r}
                            </SelectItem>
                          ))}
                        </SelectContent>
                      </Select>
                    </Field>
                    <Field label="Resolver">
                      <Input className="font-mono text-[12.5px]" placeholder="1.1.1.1" value={f.config.resolver ?? ""} onChange={(e) => setC("resolver", e.target.value)} />
                    </Field>
                  </div>
                  <Field label="Expected value" hint="Optional. Fails if no answer contains this value.">
                    <Input className="font-mono text-[12.5px]" value={f.config.expect ?? ""} onChange={(e) => setC("expect", e.target.value)} />
                  </Field>
                </Section>
              )}

              <Section title="Schedule">
                <Field label="Check every" error={errors.interval_s}>
                  <div className="flex flex-wrap items-center gap-1.5">
                    {intervals.map((s) => (
                      <button
                        key={s}
                        type="button"
                        onClick={() => set("interval_s", s)}
                        className={cn(
                          "tabular h-7 rounded-md border px-2.5 text-xs font-medium transition-colors hover:bg-muted",
                          f.interval_s === s && "border-foreground/60 bg-muted",
                        )}
                      >
                        {fmtInterval(s)}
                      </button>
                    ))}
                    <div className="flex items-center gap-1.5">
                      <Input type="number" min={5} max={86400} className="h-7 w-20 text-xs" value={f.interval_s} onChange={(e) => set("interval_s", Number(e.target.value))} aria-label="Interval in seconds" />
                      <span className="text-xs text-muted-foreground">sec</span>
                    </div>
                  </div>
                </Field>
                <div className="grid grid-cols-3 gap-3">
                  <Field label="Timeout (ms)" error={errors.timeout_ms}>
                    <Input type="number" min={100} max={120000} value={f.timeout_ms} onChange={(e) => set("timeout_ms", Number(e.target.value))} />
                  </Field>
                  <Field label="Retries" error={errors.retries} hint="Failures before down">
                    <Input type="number" min={0} max={10} value={f.retries} onChange={(e) => set("retries", Number(e.target.value))} />
                  </Field>
                  <Field label="Slow after (ms)" error={errors.degraded_ms} hint="0 = off">
                    <Input type="number" min={0} value={f.degraded_ms} onChange={(e) => set("degraded_ms", Number(e.target.value))} />
                  </Field>
                </div>
              </Section>

              {f.type !== "push" && (
                <Section title="Locations">
                  <Toggle label="Check from every location" checked={f.multi_location} onChange={(v) => set("multi_location", v)} />
                  <p className="-mt-2 text-xs text-muted-foreground">
                    {locations.length > 1
                      ? `One worker in each of your ${locations.length} locations (${locations.join(", ")}) checks it. It's marked down only when most locations fail.`
                      : locations.length === 1
                        ? `You have one location (${locations[0]}). Give workers a region on the Workers page to check from more places.`
                        : "Give workers a region on the Workers page; each region becomes a location."}
                  </p>
                  {f.multi_location && <Toggle label="Show locations on the status page" checked={f.show_locations} onChange={(v) => set("show_locations", v)} />}
                </Section>
              )}

              {canAdmin && channels && channels.length > 0 && (
                <Section title="Alerts">
                  <div className="grid gap-1.5">
                    {channels.map((c) =>
                      c.apply_to_all ? (
                        <p key={c.id} className="flex items-center gap-2 px-1 text-[13px] text-muted-foreground">
                          <Checkbox checked disabled /> {c.name} <span className="text-[11px]">(all monitors)</span>
                        </p>
                      ) : (
                        <label key={c.id} className="flex cursor-pointer items-center gap-2 rounded px-1 py-0.5 text-[13px] hover:bg-muted/50">
                          <Checkbox
                            checked={(f.channel_ids ?? []).includes(c.id)}
                            onCheckedChange={(v) => set("channel_ids", v ? [...(f.channel_ids ?? []), c.id] : (f.channel_ids ?? []).filter((x) => x !== c.id))}
                          />
                          {c.name}
                        </label>
                      ),
                    )}
                  </div>
                </Section>
              )}

              <Section title="Status">
                <div className="grid gap-3 sm:grid-cols-2">
                  <Toggle label="Active (checks are running)" checked={f.enabled} onChange={(v) => set("enabled", v)} />
                  <Toggle label="Post automatic incidents" checked={f.auto_incident} onChange={(v) => set("auto_incident", v)} />
                </div>
                <p className="-mt-1 text-xs text-muted-foreground">
                  When automatic incidents are on (Settings → Status page) and this monitor is on the status page, a long outage is posted there by itself.
                </p>
              </Section>

            </div>
          </div>

          {test && (
            <div className="border-t px-5 py-3">
              <TestResult test={test} onDismiss={() => setTest(null)} />
            </div>
          )}

          <SheetFooter className="justify-between">
            {f.type !== "push" ? (
              <Button type="button" variant="outline" onClick={() => runTest.mutate(toInput(f))} disabled={runTest.isPending || !f.target}>
                {runTest.isPending ? <Loader2Icon className="animate-spin" /> : <FlaskConicalIcon />}
                Test check
              </Button>
            ) : (
              <span />
            )}
            <div className="flex gap-2">
              <Button type="button" variant="ghost" onClick={() => onOpenChange(false)}>
                Cancel
              </Button>
              <Button type="submit" disabled={save.isPending}>
                {save.isPending && <Loader2Icon className="animate-spin" />}
                {monitor ? "Save changes" : "Create monitor"}
              </Button>
            </div>
          </SheetFooter>
        </form>
      </SheetContent>
    </Sheet>
  );
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <fieldset className="grid gap-4">
      <legend className="mb-3 text-[11px] font-semibold tracking-wide text-muted-foreground uppercase">{title}</legend>
      {children}
    </fieldset>
  );
}

function Toggle({ label, checked, onChange }: { label: string; checked: boolean; onChange: (v: boolean) => void }) {
  return (
    <label className="flex h-9 cursor-pointer items-center justify-between gap-3 rounded-md border px-3 text-[13px]">
      {label}
      <Switch checked={checked} onCheckedChange={onChange} />
    </label>
  );
}

function TestResult({ test, onDismiss }: { test: { result: CheckResult; agent: string } | { error: string }; onDismiss: () => void }) {
  const close = (
    <button type="button" onClick={onDismiss} className="ml-1 rounded p-0.5 text-muted-foreground hover:text-foreground" aria-label="Dismiss test result">
      <XIcon className="size-3.5" />
    </button>
  );
  if ("error" in test) {
    return (
      <div className="flex items-start gap-2.5 rounded-lg border border-down/25 bg-down/[0.06] p-3 text-[13px] animate-in fade-in-0 slide-in-from-bottom-1">
        <XCircleIcon className="mt-px size-4 shrink-0 text-down" />
        <p className="flex-1">{test.error}</p>
        {close}
      </div>
    );
  }
  const r = test.result;
  return (
    <div className={cn("rounded-lg border p-3 text-[13px] animate-in fade-in-0 slide-in-from-bottom-1", r.ok ? "border-up/25 bg-up/[0.06]" : "border-down/25 bg-down/[0.06]")}>
      <div className="flex items-center gap-2.5">
        {r.ok ? <CheckCircle2Icon className="size-4 text-up" /> : <XCircleIcon className="size-4 text-down" />}
        <span className="font-medium">{r.ok ? "Check passed" : "Check failed"}</span>
        <span className="tabular ml-auto text-xs text-muted-foreground">
          {formatLatency(r.lat)} · from {test.agent}
        </span>
        {close}
      </div>
      {r.msg && <p className="mt-1.5 pl-[26px] font-mono text-[12px] break-all text-muted-foreground">{r.msg}</p>}
      {r.cert && <p className="mt-1 pl-[26px] text-xs text-muted-foreground">TLS certificate valid until {new Date(r.cert).toLocaleDateString()}</p>}
    </div>
  );
}

function AssertionsEditor({ assertions, error, onChange }: { assertions: JSONAssertion[]; error?: string; onChange: (a: JSONAssertion[]) => void }) {
  const rows = assertions.length ? assertions : [{ path: "", op: "equals" as const, value: "" }];
  const update = (i: number, patch: Partial<JSONAssertion>) => onChange(rows.map((r, j) => (j === i ? { ...r, ...patch } : r)));
  return (
    <Section title="Assertions">
      <p className="-mt-1 text-xs text-muted-foreground">
        All must pass. Paths use dots, e.g. <code className="rounded bg-muted px-1 font-mono">data.items.0.status</code>.
      </p>
      <div className="grid gap-2">
        {rows.map((a, i) => {
          const op = assertionOps.find((o) => o.value === a.op) ?? assertionOps[0];
          return (
            <div key={i} className="grid grid-cols-[1fr_8.5rem_1fr_auto] items-center gap-1.5">
              <Input className="h-8 font-mono text-[12px]" placeholder="status" value={a.path} onChange={(e) => update(i, { path: e.target.value })} aria-label="JSON path" />
              <Select value={a.op} onValueChange={(v) => update(i, { op: v as JSONAssertion["op"] })}>
                <SelectTrigger className="h-8 text-xs">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {assertionOps.map((o) => (
                    <SelectItem key={o.value} value={o.value}>
                      {o.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <Input
                className="h-8 font-mono text-[12px]"
                placeholder={op.needsValue ? "ok" : "—"}
                disabled={!op.needsValue}
                value={op.needsValue ? (a.value ?? "") : ""}
                onChange={(e) => update(i, { value: e.target.value })}
                aria-label="Expected value"
              />
              <Button type="button" variant="ghost" size="icon-sm" onClick={() => onChange(rows.filter((_, j) => j !== i))} aria-label="Remove assertion" disabled={rows.length === 1}>
                <XIcon />
              </Button>
            </div>
          );
        })}
      </div>
      {error && <p className="text-xs text-destructive">{error}</p>}
      <Button type="button" variant="outline" size="sm" className="w-fit" onClick={() => onChange([...rows, { path: "", op: "equals", value: "" }])} disabled={rows.length >= 20}>
        Add assertion
      </Button>
    </Section>
  );
}
