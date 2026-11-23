import { lazy, Suspense, useEffect, useMemo, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowUpRightIcon, CheckIcon, ChevronDownIcon, GlobeIcon, MonitorIcon, MoonIcon, SunIcon } from "lucide-react";
import { cn, formatLatency, formatUptime, type Status } from "@drix/ui/lib/utils";
import { useTheme } from "@drix/ui/lib/theme";
import { useEventStream, type Broadcast } from "@drix/ui/lib/sse";
import { StatusBanner, StatusDot, LiveIndicator, StatusIcon } from "@drix/ui/drix/status";
import { UptimeBar, UptimeLegend } from "@drix/ui/drix/uptime-bar";
import { Skeleton } from "@drix/ui/components/display";
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@drix/ui/components/overlay";
import { basePath, fetchStatus, HttpError, pageApi, type PublicEvent, type PublicSection, type PublicMonitor, type Snapshot } from "./api";
import { Link, useParams } from "react-router";
import { ActiveIncidents, PastIncidents } from "./incidents";
import { SubscribeButton, SubscriptionNotice } from "./subscribe";
import { languages, useI18n, type Lang } from "./i18n";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@drix/ui/components/menu";

// Charts (Recharts) load only when a visitor expands a service.
const MonitorDetail = lazy(() => import("./MonitorDetail"));

/** Shared status query (also used by incident and history pages for branding). */
export function useStatus(slug?: string) {
  const query = useQuery({
    queryKey: ["status", slug ?? ""],
    queryFn: () => fetchStatus(slug),
    refetchInterval: 60_000,
    retry: (n, err) => !(err instanceof HttpError && err.status === 404) && n < 3,
  });
  // Brand accent and title from the page.
  const page = query.data?.page;
  useEffect(() => {
    if (page?.accent_color && /^#[0-9a-f]{6}$/i.test(page.accent_color)) {
      const root = document.documentElement.style;
      root.setProperty("--brand", page.accent_color);
      // Chart lines use the accent only where it stands out from the
      // background; a near-black accent on the dark theme gets a light grey.
      root.setProperty("--chart-light", contrast(page.accent_color, "#ffffff") >= 3 ? page.accent_color : "#3f3f46");
      root.setProperty("--chart-dark", contrast(page.accent_color, "#171717") >= 3 ? page.accent_color : "#d4d4d8");
    }
    if (page?.name) document.title = page.name;
  }, [page?.accent_color, page?.name]);
  return query;
}

/** WCAG contrast ratio between two #rrggbb colours. */
function contrast(a: string, b: string) {
  const lum = (hex: string) => {
    const [r, g, b] = [1, 3, 5].map((i) => {
      const c = parseInt(hex.slice(i, i + 2), 16) / 255;
      return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
    });
    return 0.2126 * r + 0.7152 * g + 0.0722 * b;
  };
  const [hi, lo] = [lum(a), lum(b)].sort((x, y) => y - x);
  return (hi + 0.05) / (lo + 0.05);
}

/** Page chrome: header with branding, content, footer. */
export function Frame({ data, children }: { data?: Snapshot; children: React.ReactNode }) {
  return (
    <TooltipProvider>
      <div className="min-h-dvh bg-subtle">
        <div className="mx-auto w-full max-w-[52rem] px-4 sm:px-6">
          <Header snapshot={data} />
          <main className="pb-16">
            <SubscriptionNotice />
            {children}
          </main>
          <Footer text={data?.page.footer_text} />
        </div>
      </div>
    </TooltipProvider>
  );
}

export function NotFound({ what = "page" }: { what?: "page" | "incident" }) {
  const { m } = useI18n();
  return (
    <div className="rounded-xl border bg-card p-10 text-center">
      <p className="text-sm font-medium">{what === "incident" ? m.incidentNotFound : m.pageNotFound}</p>
      <p className="mt-1 text-[13px] text-muted-foreground">{m.checkAddress}</p>
      <a href="/" className="mt-4 inline-block text-[13px] font-medium underline underline-offset-4">
        {m.mainStatusPage}
      </a>
    </div>
  );
}

export function App() {
  const { slug } = useParams();
  const qc = useQueryClient();
  const queryKey = ["status", slug ?? ""];
  const { data, isLoading, isError, error, dataUpdatedAt } = useStatus(slug);
  const notFound = error instanceof HttpError && error.status === 404;
  const base = basePath(slug);

  // Live updates: patch monitors in place; when a status flips, refetch the
  // snapshot shortly after so group and overall states are recomputed.
  const connected = useEventStream<Broadcast>(`${pageApi(slug)}/stream`, (msg) => {
    if (msg.type !== "status") return;
    let flipped = false;
    qc.setQueryData<Snapshot>(queryKey, (prev) => {
      if (!prev) return prev;
      const byId = new Map(msg.items.map((i) => [i.id, i]));
      return {
        ...prev,
        generated_at: new Date().toISOString(),
        sections: prev.sections.map((g) => ({
          ...g,
          monitors: g.monitors.map((m) => {
            const u = byId.get(m.id);
            if (!u) return m;
            if (u.status !== m.status) flipped = true;
            return {
              ...m,
              status: u.status,
              latency_ms: u.latency_ms,
              last_checked: new Date(u.at).toISOString(),
              since: u.status !== m.status ? new Date(u.at).toISOString() : m.since,
            };
          }),
        })),
      };
    });
    if (flipped) setTimeout(() => qc.invalidateQueries({ queryKey }), 1500);
  });

  const now = useNow(10_000);
  const i18n = useI18n();
  const { m, t } = i18n;

  return (
    <Frame data={data}>
            {isLoading ? (
              <LoadingState />
            ) : notFound ? (
              <NotFound />
            ) : isError || !data ? (
              <div className="rounded-xl border bg-card p-8 text-center text-sm text-muted-foreground">
                {m.loadError}
              </div>
            ) : (
              <>
                <StatusBanner
                  overall={data.overall}
                  subtitle={
                    <span className="tabular">
                      {summaryLine(data, i18n)} · {t(m.updated, { time: i18n.timeAgo(Math.max(dataUpdatedAt, new Date(data.generated_at).getTime()), now) })}
                    </span>
                  }
                  right={<LiveIndicator connected={connected} className="hidden sm:inline-flex" />}
                />
                <ActiveIncidents incidents={data.incidents} maintenance={data.maintenance} base={base} />
                <div className="mt-8 grid gap-6">
                  {data.sections.length === 0 ? (
                    <div className="rounded-xl border bg-card p-10 text-center text-sm text-muted-foreground">
                      {m.noServices}
                    </div>
                  ) : (
                    data.sections.map((g) => <GroupCard key={g.id} group={g} showLatency={data.page.show_latency} now={now} />)
                  )}
                </div>
                <PastIncidents history={data.history} base={base} />
                <Activity events={data.events} now={now} />
              </>
            )}
    </Frame>
  );
}

function useNow(every: number) {
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), every);
    return () => clearInterval(t);
  }, [every]);
  return now;
}

function summaryLine(s: Snapshot, { m, t }: ReturnType<typeof useI18n>) {
  const active = s.summary.total - s.summary.paused - s.summary.maintenance;
  const ok = s.summary.up;
  if (active === 0 && s.summary.maintenance) return t(m.sumUnderMaintenance, { n: s.summary.maintenance });
  if (active === 0) return m.sumNoActive;
  if (ok === active) return t(m.sumServicesOnline, { n: active });
  const parts = [];
  if (s.summary.down) parts.push(t(m.sumDown, { n: s.summary.down }));
  if (s.summary.degraded) parts.push(t(m.sumDegraded, { n: s.summary.degraded }));
  if (s.summary.maintenance) parts.push(t(m.sumInMaintenance, { n: s.summary.maintenance }));
  return `${t(m.sumOfOnline, { ok, n: active })}${parts.length ? ` · ${parts.join(", ")}` : ""}`;
}

/* ---------------- Header & footer ---------------- */

function Header({ snapshot }: { snapshot?: Snapshot }) {
  const site = snapshot?.page;
  const { slug } = useParams();
  const home = slug ? `/${slug}` : "/";
  const { m } = useI18n();
  return (
    <header className="flex items-center justify-between gap-4 py-8 sm:py-10">
      <Link to={home} className="flex min-w-0 items-center gap-3">
        {site?.logo_url ? (
          <img src={site.logo_url} alt="" className="h-8 w-auto max-w-40 object-contain" />
        ) : (
          <div className="flex size-8 items-center justify-center rounded-lg bg-primary text-sm font-semibold text-primary-foreground">
            {(site?.name ?? "S").slice(0, 1).toUpperCase()}
          </div>
        )}
        <div className="min-w-0">
          {site ? (
            <>
              <h1 className="truncate text-[15px] font-semibold tracking-tight">{site.name}</h1>
              {site.description && <p className="truncate text-[13px] text-muted-foreground">{site.description}</p>}
            </>
          ) : (
            <Skeleton className="h-4 w-36" />
          )}
        </div>
      </Link>
      <div className="flex items-center gap-1">
        {site && <SubscribeButton slug={slug} pageName={site.name} />}
        {site?.website_url && (
          <a
            href={site.website_url}
            className="hidden items-center gap-1 rounded-md px-2 py-1.5 text-[13px] text-muted-foreground transition-colors hover:text-foreground sm:inline-flex"
          >
            {m.website} <ArrowUpRightIcon className="size-3.5" />
          </a>
        )}
        <ThemeToggle />
      </div>
    </header>
  );
}

function ThemeToggle() {
  const { theme, setTheme } = useTheme();
  const next = theme === "system" ? "light" : theme === "light" ? "dark" : "system";
  const Icon = theme === "system" ? MonitorIcon : theme === "light" ? SunIcon : MoonIcon;
  const { m } = useI18n();
  const name = { system: m.themeSystem, light: m.themeLight, dark: m.themeDark };
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <button
          onClick={() => setTheme(next)}
          className="inline-flex size-8 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
          aria-label={`${m.theme}: ${name[theme]} → ${name[next]}`}
        >
          <Icon className="size-4" />
        </button>
      </TooltipTrigger>
      <TooltipContent>
        {m.theme}: {name[theme]}
      </TooltipContent>
    </Tooltip>
  );
}

function Footer({ text }: { text?: string }) {
  const { m, tx } = useI18n();
  return (
    <footer className="flex flex-col items-center justify-between gap-3 border-t py-6 text-xs text-muted-foreground sm:flex-row">
      <span>{text || " "}</span>
      <span className="flex items-center gap-4">
        <LanguageMenu />
        <span>{tx(m.poweredBy, { name: <span className="font-medium text-foreground">Drix Status</span> })}</span>
      </span>
    </footer>
  );
}

/** Language picker in the footer; the choice is remembered in this browser. */
function LanguageMenu() {
  const { lang, setLang, m } = useI18n();
  const current = languages.find((l) => l.code === lang)!;
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button className="inline-flex items-center gap-1.5 rounded-md px-1.5 py-1 transition-colors hover:text-foreground" aria-label={m.language}>
          <GlobeIcon className="size-3.5" />
          {current.name}
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="max-h-80 overflow-y-auto">
        {languages.map((l) => (
          <DropdownMenuItem key={l.code} lang={l.locale} onSelect={() => setLang(l.code as Lang)} className="justify-between gap-6">
            {l.name}
            {l.code === lang && <CheckIcon className="size-3.5" />}
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

/* ---------------- Groups & monitors ---------------- */

function GroupCard({ group, showLatency, now }: { group: PublicSection; showLatency: boolean; now: number }) {
  const [open, setOpen] = useState(!group.collapsed);
  const counts = useMemo(() => {
    const c = { up: 0, total: group.monitors.length };
    group.monitors.forEach((m) => m.status === "up" && c.up++);
    return c;
  }, [group.monitors]);

  return (
    <section className="overflow-hidden rounded-xl border bg-card shadow-xs">
      <button
        onClick={() => setOpen((o) => !o)}
        className="flex w-full items-center gap-3 px-4 py-3 text-left transition-colors hover:bg-muted/30 sm:px-5"
        aria-expanded={open}
      >
        <ChevronDownIcon className={cn("size-4 text-muted-foreground transition-transform", !open && "-rotate-90")} />
        <div className="min-w-0 flex-1">
          <h2 className="truncate text-[14px] font-semibold tracking-tight">{group.name}</h2>
          {group.description && <p className="truncate text-xs text-muted-foreground">{group.description}</p>}
        </div>
        {!open && (
          <span className="tabular text-xs text-muted-foreground">
            {counts.up}/{counts.total}
          </span>
        )}
        <span className="flex items-center gap-1.5 text-xs font-medium">
          <StatusDot status={group.status} />
          <span className="hidden sm:inline">
            <GroupLabel group={group} />
          </span>
        </span>
      </button>
      {open && (
        <ul className="divide-y border-t">
          {group.monitors.map((m) => (
            <MonitorRow key={m.id} monitor={m} showLatency={showLatency} now={now} />
          ))}
        </ul>
      )}
    </section>
  );
}

function GroupLabel({ group: g }: { group: PublicSection }) {
  const { m } = useI18n();
  if (g.status !== "down") return <>{m.status[g.status]}</>;
  const down = g.monitors.filter((x) => x.status === "down").length;
  return <>{down === g.monitors.length ? m.outage : m.partialOutage}</>;
}

function MonitorRow({ monitor: m, showLatency, now }: { monitor: PublicMonitor; showLatency: boolean; now: number }) {
  const [expanded, setExpanded] = useState(false);
  const i18n = useI18n();
  return (
    <li className="px-4 py-4 sm:px-5">
      <div className="flex items-center gap-3">
        <button
          className="group flex min-w-0 flex-1 items-center gap-2.5 text-left"
          onClick={() => setExpanded((e) => !e)}
          aria-expanded={expanded}
        >
          <StatusDot status={m.status} />
          <span className="truncate text-[13.5px] font-medium group-hover:underline group-hover:underline-offset-4">{m.name}</span>
          {m.description && (
            <Tooltip>
              <TooltipTrigger asChild>
                <span className="hidden size-4 shrink-0 items-center justify-center rounded-full border text-[10px] text-muted-foreground sm:inline-flex">
                  ?
                </span>
              </TooltipTrigger>
              <TooltipContent>{m.description}</TooltipContent>
            </Tooltip>
          )}
        </button>
        <div className="flex shrink-0 items-center gap-3 text-xs">
          {showLatency && m.status !== "down" && m.latency_ms > 0 && (
            <span className="tabular hidden text-muted-foreground sm:inline">{formatLatency(m.latency_ms)}</span>
          )}
          <StatusText status={m.status} since={m.since} now={now} />
        </div>
      </div>
      <div className="mt-3">
        <UptimeBar days={m.days} />
        <UptimeLegend days={m.days.length} center={<span className="tabular font-medium">{i18n.t(i18n.m.uptimeValue, { value: formatUptime(m.uptime_90) })}</span>} />
      </div>
      {expanded && (
        <Suspense fallback={<Skeleton className="mt-4 h-[226px] w-full rounded-lg" />}>
          <MonitorDetail monitor={m} />
        </Suspense>
      )}
    </li>
  );
}

function StatusText({ status, since, now }: { status: Status; since?: string; now: number }) {
  const { m, t, duration } = useI18n();
  const color =
    status === "up"
      ? "text-up"
      : status === "down"
        ? "text-down"
        : status === "degraded"
          ? "text-[color-mix(in_oklch,var(--degraded)_70%,var(--foreground))]"
          : "text-muted-foreground";
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span className={cn("font-medium", color)}>{m.status[status]}</span>
      </TooltipTrigger>
      {since && <TooltipContent>{t(m.forDuration, { duration: duration(since, now) })}</TooltipContent>}
    </Tooltip>
  );
}

/* ---------------- Activity ---------------- */

function eventText(e: PublicEvent, m: ReturnType<typeof useI18n>["m"]) {
  switch (e.to_status) {
    case "down":
      return m.evDown;
    case "degraded":
      return m.evDegraded;
    case "up":
      return e.from_status === "down" ? m.evRecovered : m.evNormal;
    default:
      return m.evChanged;
  }
}

function Activity({ events, now }: { events: PublicEvent[]; now: number }) {
  const i18n = useI18n();
  const { m, tx } = i18n;
  return (
    <section className="mt-12">
      <h2 className="mb-3 text-[14px] font-semibold tracking-tight">{m.recentActivity}</h2>
      {events.length === 0 ? (
        <div className="rounded-xl border bg-card px-5 py-6 text-[13px] text-muted-foreground">
          {m.noRecentActivity}
        </div>
      ) : (
        <ol className="relative grid gap-0 rounded-xl border bg-card px-4 py-2 sm:px-5">
          {events.map((e) => (
            <li key={e.id} className="flex items-start gap-3 border-b py-3 last:border-0">
              <StatusIcon status={e.to_status} className="mt-px size-4 [&_svg]:size-2.5" />
              <div className="min-w-0 flex-1">
                <p className="text-[13px] text-muted-foreground">
                  {tx(eventText(e, m), { name: <span className="font-medium text-foreground">{e.monitor_name}</span> })}
                </p>
              </div>
              <time className="tabular shrink-0 text-xs text-muted-foreground" dateTime={e.created_at} title={new Date(e.created_at).toLocaleString(i18n.locale)}>
                {i18n.timeAgo(e.created_at, now)}
              </time>
            </li>
          ))}
        </ol>
      )}
    </section>
  );
}

function LoadingState() {
  return (
    <div className="grid gap-6">
      <Skeleton className="h-[68px] w-full rounded-xl" />
      {[0, 1].map((i) => (
        <div key={i} className="rounded-xl border bg-card p-5">
          <Skeleton className="h-4 w-40" />
          <div className="mt-5 grid gap-6">
            {[0, 1, 2].map((j) => (
              <div key={j} className="grid gap-3">
                <Skeleton className="h-3.5 w-52" />
                <Skeleton className="h-8 w-full" />
              </div>
            ))}
          </div>
        </div>
      ))}
    </div>
  );
}
