import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { Link, useParams } from "react-router";
import { ChevronLeftIcon, Loader2Icon } from "lucide-react";
import { cn } from "@drix/ui/lib/utils";
import { Skeleton } from "@drix/ui/components/display";
import { basePath, fetchHistory, fetchIncident, HttpError, type PublicIncident } from "./api";
import { Frame, NotFound, useStatus } from "./App";
import { Timeline, tone, windowLabel } from "./incidents";
import { useI18n } from "./i18n";

function Back({ base }: { base: string }) {
  const { m } = useI18n();
  return (
    <Link to={base || "/"} className="mb-5 inline-flex items-center gap-1 text-[13px] text-muted-foreground hover:text-foreground">
      <ChevronLeftIcon className="size-4" /> {m.currentStatus}
    </Link>
  );
}

export function IncidentPage() {
  const { slug, id } = useParams();
  const base = basePath(slug);
  const { data: snap } = useStatus(slug);
  const { data: inc, isLoading, error } = useQuery({
    queryKey: ["incident", slug ?? "", id],
    queryFn: () => fetchIncident(slug, Number(id)),
    refetchInterval: 30_000,
    retry: (n, err) => !(err instanceof HttpError && err.status === 404) && n < 2,
  });
  return (
    <Frame data={snap}>
      <Back base={base} />
      {isLoading ? (
        <Skeleton className="h-64 rounded-xl" />
      ) : !inc || error ? (
        <NotFound what="incident" />
      ) : (
        <article className="rounded-xl border bg-card p-5 sm:p-7">
          <Header inc={inc} />
          <div className="mt-7">
            <Timeline inc={inc} />
          </div>
        </article>
      )}
    </Frame>
  );
}

function Header({ inc }: { inc: PublicIncident }) {
  const t = tone(inc);
  const closed = !!inc.resolved_at;
  const i18n = useI18n();
  const { m } = i18n;
  return (
    <header>
      <div className="flex flex-wrap items-center gap-2 text-xs font-medium">
        <span className={cn("inline-flex items-center gap-1.5", t.text)}>
          <span className={cn("size-2 rounded-full", t.bar)} />
          {inc.kind === "maintenance" ? m.maintenance : m.incidentKind[inc.impact]}
        </span>
        <span className="text-muted-foreground">·</span>
        <span className={closed ? "text-muted-foreground" : t.text}>{m.incidentStatus[inc.status]}</span>
      </div>
      <h1 className="mt-2 text-xl font-semibold tracking-tight text-balance">{inc.title}</h1>
      <p className="mt-1.5 text-[13px] text-muted-foreground">
        {inc.kind === "maintenance" ? windowLabel(inc, i18n.locale) : i18n.t(m.started, { time: i18n.dateTime(inc.created_at) })}
        {closed && inc.kind === "incident" && inc.resolved_at && ` · ${i18n.t(m.resolvedAt, { time: i18n.dateTime(inc.resolved_at) })}`}
      </p>
      {inc.affected.length > 0 && (
        <div className="mt-4 flex flex-wrap items-center gap-1.5">
          <span className="text-xs text-muted-foreground">{m.affected}</span>
          {inc.affected.map((a) => (
            <span key={a.id} className="rounded-md border bg-muted/40 px-2 py-0.5 text-xs">
              {a.name}
            </span>
          ))}
        </div>
      )}
    </header>
  );
}

export function HistoryPage() {
  const i18n = useI18n();
  const { m } = i18n;
  const monthFmt = new Intl.DateTimeFormat(i18n.locale, { month: "long", year: "numeric" });
  const { slug } = useParams();
  const base = basePath(slug);
  const { data: snap } = useStatus(slug);
  const q = useInfiniteQuery({
    queryKey: ["history", slug ?? ""],
    queryFn: ({ pageParam }) => fetchHistory(slug, pageParam || undefined),
    initialPageParam: 0,
    getNextPageParam: (last) => (last.has_more && last.incidents.length ? last.incidents[last.incidents.length - 1].id : undefined),
  });
  const all = q.data?.pages.flatMap((p) => p.incidents) ?? [];
  const groups = new Map<string, PublicIncident[]>();
  for (const inc of all) {
    const key = monthFmt.format(new Date(inc.scheduled_start ?? inc.created_at));
    groups.set(key, [...(groups.get(key) ?? []), inc]);
  }

  return (
    <Frame data={snap}>
      <Back base={base} />
      <h1 className="mb-6 text-lg font-semibold tracking-tight">{m.incidentHistory}</h1>
      {q.isLoading ? (
        <Skeleton className="h-64 rounded-xl" />
      ) : all.length === 0 ? (
        <div className="rounded-xl border bg-card px-5 py-8 text-center text-[13px] text-muted-foreground">{m.noneReported}</div>
      ) : (
        <div className="grid gap-8">
          {[...groups.entries()].map(([month, list]) => (
            <section key={month}>
              <h2 className="mb-2 text-[13px] font-semibold text-muted-foreground">{month}</h2>
              <ul className="divide-y rounded-xl border bg-card">
                {list.map((inc) => (
                  <li key={inc.id}>
                    <Link to={`${base}/incidents/${inc.id}`} className="block px-4 py-3.5 transition-colors hover:bg-muted/30 sm:px-5">
                      <div className="flex items-start gap-3">
                        <span className={cn("mt-1.5 size-2 shrink-0 rounded-full", tone(inc).bar)} />
                        <div className="min-w-0 flex-1">
                          <p className="text-[13.5px] font-medium">{inc.title}</p>
                          {inc.updates[0] && <p className="mt-0.5 line-clamp-2 text-[13px] text-muted-foreground">{inc.updates[0].body}</p>}
                          <p className="mt-1 text-[11.5px] text-muted-foreground">
                            {inc.kind === "maintenance" ? windowLabel(inc, i18n.locale) : i18n.dateTime(inc.created_at)}
                            {inc.affected.length > 0 && ` · ${inc.affected.map((a) => a.name).join(", ")}`}
                          </p>
                        </div>
                      </div>
                    </Link>
                  </li>
                ))}
              </ul>
            </section>
          ))}
          {q.hasNextPage && (
            <button
              onClick={() => q.fetchNextPage()}
              disabled={q.isFetchingNextPage}
              className="mx-auto inline-flex items-center gap-2 rounded-md border bg-card px-3 py-1.5 text-[13px] hover:bg-muted"
            >
              {q.isFetchingNextPage && <Loader2Icon className="size-4 animate-spin" />}
              {m.loadOlder}
            </button>
          )}
        </div>
      )}
    </Frame>
  );
}
