import { Link } from "react-router";
import { ArrowRightIcon, CalendarClockIcon } from "lucide-react";
import { cn } from "@drix/ui/lib/utils";
import type { PublicIncident } from "./api";
import { useI18n } from "./i18n";

/** Accent for an incident: maintenance is blue, incidents follow impact. */
export function tone(inc: Pick<PublicIncident, "kind" | "impact">) {
  if (inc.kind === "maintenance") return { bar: "bg-maintenance", text: "text-maintenance", soft: "border-maintenance/25 bg-maintenance/[0.05]" };
  if (inc.impact === "major" || inc.impact === "critical") return { bar: "bg-down", text: "text-down", soft: "border-down/25 bg-down/[0.05]" };
  if (inc.impact === "minor") return { bar: "bg-degraded", text: "text-[color-mix(in_oklch,var(--degraded)_70%,var(--foreground))]", soft: "border-degraded/30 bg-degraded/[0.06]" };
  return { bar: "bg-pending", text: "text-muted-foreground", soft: "bg-card" };
}

/** A maintenance window, e.g. "Sat, Nov 22, 14:00 – 15:00 CET", in the visitor's language. */
export function windowLabel(inc: PublicIncident, locale: string) {
  if (!inc.scheduled_start || !inc.scheduled_end) return "";
  const dtf = new Intl.DateTimeFormat(locale, { weekday: "short", month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" });
  const tf = new Intl.DateTimeFormat(locale, { hour: "2-digit", minute: "2-digit", timeZoneName: "short" });
  const s = new Date(inc.scheduled_start);
  const e = new Date(inc.scheduled_end);
  const sameDay = s.toDateString() === e.toDateString();
  return sameDay ? `${dtf.format(s)} – ${tf.format(e)}` : `${dtf.format(s)} – ${dtf.format(e)}`;
}

/** Open incidents and maintenance, shown right under the overall banner. */
export function ActiveIncidents({ incidents, maintenance, base }: { incidents: PublicIncident[]; maintenance: PublicIncident[]; base: string }) {
  if (!incidents.length && !maintenance.length) return null;
  return (
    <div className="mt-4 grid gap-3">
      {incidents.map((inc) => (
        <IncidentCard key={inc.id} inc={inc} base={base} />
      ))}
      {maintenance.map((inc) => (
        <IncidentCard key={inc.id} inc={inc} base={base} />
      ))}
    </div>
  );
}

function IncidentCard({ inc, base }: { inc: PublicIncident; base: string }) {
  const t = tone(inc);
  const i18n = useI18n();
  const { m } = i18n;
  const latest = inc.updates[0];
  const upcoming = inc.kind === "maintenance" && inc.status === "scheduled";
  return (
    <Link
      to={`${base}/incidents/${inc.id}`}
      className={cn("group relative block overflow-hidden rounded-xl border px-4 py-3.5 transition-colors hover:bg-muted/30 sm:px-5", t.soft)}
    >
      <span className={cn("absolute inset-y-0 left-0 w-1", t.bar)} aria-hidden />
      <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
        {inc.kind === "maintenance" && <CalendarClockIcon className={cn("size-4", t.text)} />}
        <h3 className="text-[14px] font-semibold tracking-tight">{inc.title}</h3>
        <span className={cn("text-xs font-medium", t.text)}>
          {upcoming ? m.incidentStatus.scheduled : m.incidentStatus[inc.status]}
        </span>
        <ArrowRightIcon className="ml-auto size-4 text-muted-foreground opacity-0 transition-opacity group-hover:opacity-100" />
      </div>
      {inc.kind === "maintenance" && <p className="mt-0.5 text-xs text-muted-foreground">{windowLabel(inc, i18n.locale)}</p>}
      {latest && <p className="mt-1.5 line-clamp-2 text-[13px] text-muted-foreground">{latest.body}</p>}
      <div className="mt-2 flex flex-wrap items-center gap-x-3 gap-y-1 text-[11.5px] text-muted-foreground">
        {latest && <span title={i18n.dateTime(latest.created_at)}>{i18n.t(m.updated, { time: i18n.timeAgo(latest.created_at) })}</span>}
        {inc.affected.length > 0 && (
          <span className="truncate">{i18n.t(m.affects, { list: inc.affected.map((a) => a.name).join(", ") })}</span>
        )}
      </div>
    </Link>
  );
}

/** Compact list of recently closed incidents with a link to the full history. */
export function PastIncidents({ history, base }: { history: PublicIncident[]; base: string }) {
  const { m, locale } = useI18n();
  return (
    <section className="mt-12">
      <div className="mb-3 flex items-baseline justify-between">
        <h2 className="text-[14px] font-semibold tracking-tight">{m.pastIncidents}</h2>
        <Link to={`${base}/history`} className="text-xs text-muted-foreground hover:text-foreground">
          {m.incidentHistory} →
        </Link>
      </div>
      {history.length === 0 ? (
        <div className="rounded-xl border bg-card px-5 py-6 text-[13px] text-muted-foreground">{m.noIncidents14}</div>
      ) : (
        <ul className="divide-y rounded-xl border bg-card">
          {history.map((inc) => (
            <li key={inc.id}>
              <Link to={`${base}/incidents/${inc.id}`} className="flex items-start gap-3 px-4 py-3 transition-colors hover:bg-muted/30 sm:px-5">
                <span className={cn("mt-1.5 size-2 shrink-0 rounded-full", tone(inc).bar)} />
                <div className="min-w-0 flex-1">
                  <p className="truncate text-[13px] font-medium">{inc.title}</p>
                  {inc.updates[0] && <p className="line-clamp-1 text-xs text-muted-foreground">{inc.updates[0].body}</p>}
                </div>
                <time className="tabular shrink-0 text-xs text-muted-foreground" dateTime={inc.resolved_at}>
                  {inc.resolved_at ? new Date(inc.resolved_at).toLocaleDateString(locale, { month: "short", day: "numeric" }) : ""}
                </time>
              </Link>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

/** Timeline of updates, newest first. */
export function Timeline({ inc }: { inc: PublicIncident }) {
  const t = tone(inc);
  const { m, dateTime } = useI18n();
  return (
    <ol className="relative grid gap-6 border-l pl-5">
      {inc.updates.map((u, i) => (
        <li key={u.id} className="relative">
          <span className={cn("absolute top-1.5 -left-[25px] size-2.5 rounded-full ring-4 ring-background", i === 0 ? t.bar : "bg-border")} />
          <p className="text-[13px]">
            <span className={cn("font-semibold", i === 0 && t.text)}>{m.incidentStatus[u.status]}</span>
            <span className="text-muted-foreground"> · {dateTime(u.created_at)}</span>
          </p>
          <p className="mt-1 text-[14px] leading-relaxed whitespace-pre-line">{u.body}</p>
        </li>
      ))}
    </ol>
  );
}
