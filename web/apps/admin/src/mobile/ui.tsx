import * as React from "react";
import { Link, useNavigate } from "react-router";
import { Dialog as DialogPrimitive, Switch as SwitchPrimitive } from "radix-ui";
import { CheckIcon, ChevronLeftIcon, ChevronRightIcon, Loader2Icon } from "lucide-react";
import { cn } from "@drix/ui/lib/utils";

/* ---------------- navigation ---------------- */

/** Marks the next view transition as a push (slide in) or pop (slide out). */
export function setNav(kind: "push" | "pop" | "tab") {
  document.documentElement.dataset.nav = kind;
}

/**
 * A screen with an iOS navigation bar: a large title that collapses into a
 * small centred title in a blurred bar on scroll, an optional back button
 * and right action, and pull to refresh.
 */
export function NavScreen({
  title,
  caption,
  back,
  right,
  onRefresh,
  children,
}: {
  title: string;
  caption?: React.ReactNode;
  back?: { to: string; label: string };
  right?: React.ReactNode;
  onRefresh?: () => Promise<unknown>;
  children: React.ReactNode;
}) {
  const navigate = useNavigate();
  const [collapsed, setCollapsed] = React.useState(false);
  const [pull, setPull] = React.useState(0);
  const [refreshing, setRefreshing] = React.useState(false);

  React.useEffect(() => {
    const onScroll = () => setCollapsed(window.scrollY > 36);
    onScroll();
    window.addEventListener("scroll", onScroll, { passive: true });
    return () => window.removeEventListener("scroll", onScroll);
  }, []);

  // Pull to refresh: drag down from the top of the page.
  React.useEffect(() => {
    if (!onRefresh) return;
    let startY: number | null = null;
    let dist = 0;
    const onStart = (e: TouchEvent) => {
      startY = window.scrollY <= 0 ? e.touches[0].clientY : null;
      dist = 0;
    };
    const onMove = (e: TouchEvent) => {
      if (startY === null) return;
      dist = Math.max(0, e.touches[0].clientY - startY);
      setPull(Math.min(dist * 0.45, 90));
    };
    const onEnd = async () => {
      if (startY === null) return;
      startY = null;
      if (dist * 0.45 > 64) {
        setRefreshing(true);
        setPull(56);
        try {
          await onRefresh();
        } finally {
          setRefreshing(false);
          setPull(0);
        }
      } else {
        setPull(0);
      }
    };
    window.addEventListener("touchstart", onStart, { passive: true });
    window.addEventListener("touchmove", onMove, { passive: true });
    window.addEventListener("touchend", onEnd);
    return () => {
      window.removeEventListener("touchstart", onStart);
      window.removeEventListener("touchmove", onMove);
      window.removeEventListener("touchend", onEnd);
    };
  }, [onRefresh]);

  return (
    <>
      <header
        className={cn(
          "fixed inset-x-0 top-0 z-30 pt-[env(safe-area-inset-top)] transition-[background-color,border-color] duration-200",
          collapsed ? "border-b-[0.5px] border-[var(--ios-sep)] bg-[var(--ios-bar)] backdrop-blur-xl backdrop-saturate-150" : "border-b-[0.5px] border-transparent",
        )}
      >
        <div className="relative mx-auto grid h-11 max-w-lg grid-cols-[1fr_auto_1fr] items-center px-2">
          <div className="min-w-0">
            {back && (
              <button
                type="button"
                onClick={() => {
                  setNav("pop");
                  navigate(back.to, { viewTransition: true });
                }}
                className="flex items-center text-[17px] text-[var(--ios-tint)] active:opacity-40"
              >
                <ChevronLeftIcon className="size-7 -ml-1" strokeWidth={2.4} />
                <span className="-ml-0.5 truncate">{back.label}</span>
              </button>
            )}
          </div>
          <p className={cn("max-w-[52vw] truncate text-[17px] font-semibold transition-opacity duration-200", collapsed ? "opacity-100" : "opacity-0")}>{title}</p>
          <div className="flex justify-end pr-2">{right}</div>
        </div>
      </header>

      {onRefresh && (
        <div className="pointer-events-none fixed inset-x-0 z-20 flex justify-center" style={{ top: `calc(env(safe-area-inset-top) + 44px + ${pull / 2 - 14}px)`, opacity: pull / 56 }}>
          <Loader2Icon className={cn("size-6 text-[var(--ios-label2)]", refreshing && "animate-spin")} style={{ transform: refreshing ? undefined : `rotate(${pull * 4}deg)` }} />
        </div>
      )}

      <div style={{ transform: pull ? `translateY(${pull}px)` : undefined, transition: pull === 0 || refreshing ? "transform .3s cubic-bezier(.32,.72,0,1)" : undefined }}>
        <div className="px-1 pt-11 pb-2">
          {caption && <div className="mb-0.5 text-[13px] text-[var(--ios-label2)]">{caption}</div>}
          <h1 className="text-[34px] leading-[41px] font-bold tracking-[0.01em]">{title}</h1>
        </div>
        {children}
      </div>
    </>
  );
}

/** A round button for the navigation bar (e.g. +). */
export function BarButton({ label, onClick, children }: { label: string; onClick: () => void; children: React.ReactNode }) {
  return (
    <button type="button" aria-label={label} onClick={onClick} className="flex size-9 items-center justify-center text-[var(--ios-tint)] active:opacity-40 [&_svg]:size-[26px]">
      {children}
    </button>
  );
}

/* ---------------- lists ---------------- */

/** An inset grouped list: grey header, rounded cells, grey footer. */
export function ListGroup({ header, footer, children, className }: { header?: React.ReactNode; footer?: React.ReactNode; children: React.ReactNode; className?: string }) {
  return (
    <section className={cn("mb-[35px]", className)}>
      {header && <h2 className="mb-[7px] px-4 text-[13px] text-[var(--ios-label2)] uppercase">{header}</h2>}
      <div className="overflow-hidden rounded-[10px] bg-[var(--ios-cell)]">{children}</div>
      {footer && <p className="mt-[7px] px-4 text-[13px] leading-[18px] text-[var(--ios-label2)]">{footer}</p>}
    </section>
  );
}

/** A small coloured rounded square with a white icon, as in iOS Settings. */
export function IconTile({ color, children }: { color: string; children: React.ReactNode }) {
  return (
    <span className="flex size-[29px] items-center justify-center rounded-[7px] text-white [&_svg]:size-[18px]" style={{ background: color }}>
      {children}
    </span>
  );
}

type RowProps = {
  to?: string;
  onClick?: () => void;
  /** What sits before the title; sets where the separator starts. */
  leading?: React.ReactNode;
  leadingKind?: "dot" | "tile";
  title: React.ReactNode;
  subtitle?: React.ReactNode;
  detail?: React.ReactNode;
  accessory?: "chevron" | "check" | "none" | React.ReactNode;
  tone?: "default" | "tint" | "destructive";
  disabled?: boolean;
  loading?: boolean;
};

/** One cell: leading icon, title (+ subtitle), detail text, accessory. */
export function Row({ to, onClick, leading, leadingKind, title, subtitle, detail, accessory, tone = "default", disabled, loading }: RowProps) {
  const inset = leadingKind === "tile" ? 60 : leadingKind === "dot" ? 38 : 16;
  const acc = accessory ?? (to ? "chevron" : "none");
  const body = (
    <>
      {leading && <span className="flex shrink-0 items-center">{leading}</span>}
      <span className="min-w-0 flex-1 py-[11px]">
        <span
          className={cn(
            "block truncate text-[17px] leading-[22px]",
            tone === "tint" && "text-[var(--ios-tint)] font-medium",
            tone === "destructive" && "text-[var(--ios-red)]",
          )}
        >
          {title}
        </span>
        {subtitle && <span className="block truncate text-[15px] leading-[20px] text-[var(--ios-label2)]">{subtitle}</span>}
      </span>
      {detail !== undefined && <span className="shrink-0 text-[17px] text-[var(--ios-label2)]">{detail}</span>}
      {loading && <Loader2Icon className="size-4 shrink-0 animate-spin text-[var(--ios-label2)]" />}
      {acc === "chevron" && <ChevronRightIcon className="-mr-1 size-[18px] shrink-0 text-[var(--ios-label3)]" strokeWidth={2.6} />}
      {acc === "check" && <CheckIcon className="size-[20px] shrink-0 text-[var(--ios-tint)]" strokeWidth={2.6} />}
      {acc !== "chevron" && acc !== "check" && acc !== "none" && <span className="shrink-0">{acc}</span>}
    </>
  );
  const cls = cn(
    "ios-cell flex min-h-11 w-full items-center gap-3 px-4 text-left transition-colors duration-150",
    (to || onClick) && !disabled && "active:bg-[var(--ios-cell-press)] active:duration-0",
    disabled && "opacity-40",
  );
  const style = { "--sep-inset": `${inset}px` } as React.CSSProperties;
  if (to) {
    return (
      <Link to={to} viewTransition onClick={() => setNav("push")} className={cls} style={style}>
        {body}
      </Link>
    );
  }
  if (onClick) {
    return (
      <button type="button" disabled={disabled} onClick={onClick} className={cls} style={style}>
        {body}
      </button>
    );
  }
  return (
    <div className={cls} style={style}>
      {body}
    </div>
  );
}

/** Single-choice rows with a checkmark on the selected one. */
export function CheckList<T extends string | number>({ value, options, onChange }: { value: T; options: { value: T; label: string; subtitle?: string }[]; onChange: (v: T) => void }) {
  return (
    <>
      {options.map((o) => (
        <Row key={String(o.value)} title={o.label} subtitle={o.subtitle} onClick={() => onChange(o.value)} accessory={value === o.value ? "check" : "none"} />
      ))}
    </>
  );
}

/** A text field inside a cell. */
export function FieldCell(props: React.ComponentProps<"input">) {
  return (
    <div className="ios-cell px-4">
      <input {...props} className="h-11 w-full bg-transparent text-[17px] outline-none placeholder:text-[var(--ios-label3)]" />
    </div>
  );
}

export function TextAreaCell(props: React.ComponentProps<"textarea">) {
  return (
    <div className="ios-cell px-4 py-[11px]">
      <textarea {...props} className="min-h-24 w-full resize-none bg-transparent text-[17px] leading-[22px] outline-none placeholder:text-[var(--ios-label3)]" />
    </div>
  );
}

/** Grey placeholder cells while loading. */
export function SkeletonGroup({ rows = 4 }: { rows?: number }) {
  return (
    <ListGroup>
      {Array.from({ length: rows }, (_, i) => (
        <div key={i} className="ios-cell flex h-11 items-center gap-3 px-4">
          <span className="size-2.5 rounded-full bg-[var(--ios-fill)]" />
          <span className="h-3.5 rounded bg-[var(--ios-fill)]" style={{ width: `${40 + ((i * 17) % 35)}%` }} />
        </div>
      ))}
    </ListGroup>
  );
}

/* ---------------- controls ---------------- */

/** iOS switch: 51×31, green when on. */
export function IosSwitch(props: React.ComponentProps<typeof SwitchPrimitive.Root>) {
  return (
    <SwitchPrimitive.Root
      {...props}
      className="relative inline-flex h-[31px] w-[51px] shrink-0 items-center rounded-full bg-[var(--ios-fill)] transition-colors duration-200 outline-none disabled:opacity-40 data-[state=checked]:bg-[var(--ios-green)]"
    >
      <SwitchPrimitive.Thumb className="block size-[27px] translate-x-[2px] rounded-full bg-white shadow-[0_3px_8px_rgba(0,0,0,0.15),0_3px_1px_rgba(0,0,0,0.06)] transition-transform duration-200 data-[state=checked]:translate-x-[22px]" />
    </SwitchPrimitive.Root>
  );
}

/** iOS segmented control with a sliding thumb. */
export function Segmented<T extends string>({ value, options, onChange, className }: { value: T; options: { value: T; label: string }[]; onChange: (v: T) => void; className?: string }) {
  const i = Math.max(0, options.findIndex((o) => o.value === value));
  return (
    <div className={cn("relative grid h-8 rounded-[9px] bg-[var(--ios-fill)] p-[2px]", className)} style={{ gridTemplateColumns: `repeat(${options.length}, minmax(0, 1fr))` }}>
      <span
        className="absolute top-[2px] bottom-[2px] left-[2px] rounded-[7px] bg-[var(--ios-thumb)] shadow-[0_3px_8px_rgba(0,0,0,0.12),0_3px_1px_rgba(0,0,0,0.04)] transition-transform duration-300 ease-[cubic-bezier(.32,.72,0,1)]"
        style={{ width: `calc((100% - 4px) / ${options.length})`, transform: `translateX(${i * 100}%)` }}
      />
      {options.map((o) => (
        <button
          key={o.value}
          type="button"
          onClick={() => onChange(o.value)}
          className={cn("relative z-10 px-2 text-[13px] transition-[font-weight]", value === o.value ? "font-semibold" : "font-medium")}
        >
          {o.label}
        </button>
      ))}
    </div>
  );
}

/* ---------------- sheets ---------------- */

/** iOS modal sheet: Cancel · Title · Action, grouped content below. */
export function Sheet({
  open,
  onOpenChange,
  title,
  action,
  children,
}: {
  open: boolean;
  onOpenChange: (o: boolean) => void;
  title: string;
  action?: { label: string; onClick: () => void; disabled?: boolean; loading?: boolean };
  children: React.ReactNode;
}) {
  return (
    <DialogPrimitive.Root open={open} onOpenChange={onOpenChange}>
      <DialogPrimitive.Portal>
        <DialogPrimitive.Overlay className="fixed inset-0 z-50 bg-black/40 data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:animate-in data-[state=open]:fade-in-0" />
        <DialogPrimitive.Content
          aria-describedby={undefined}
          className="ios-sheet fixed inset-x-0 bottom-0 z-50 flex max-h-[calc(100dvh-env(safe-area-inset-top)-10px)] flex-col rounded-t-[12px] bg-[var(--ios-bg)] text-[var(--ios-label)] shadow-2xl outline-none sm:mx-auto sm:max-w-lg"
        >
          <div className="mx-auto mt-[5px] h-[5px] w-9 rounded-full bg-[var(--ios-label3)]" />
          <div className="grid h-[52px] grid-cols-[1fr_auto_1fr] items-center px-4">
            <DialogPrimitive.Close className="justify-self-start text-[17px] text-[var(--ios-tint)] outline-none active:opacity-40">Cancel</DialogPrimitive.Close>
            <DialogPrimitive.Title className="text-[17px] font-semibold">{title}</DialogPrimitive.Title>
            {action ? (
              <button
                type="button"
                disabled={action.disabled || action.loading}
                onClick={action.onClick}
                className="flex items-center gap-1.5 justify-self-end text-[17px] font-semibold text-[var(--ios-tint)] active:opacity-40 disabled:opacity-30"
              >
                {action.loading && <Loader2Icon className="size-4 animate-spin" />}
                {action.label}
              </button>
            ) : (
              <span />
            )}
          </div>
          <div className="min-h-0 flex-1 overflow-y-auto px-4 pt-3 pb-[calc(env(safe-area-inset-bottom)+20px)]">{children}</div>
        </DialogPrimitive.Content>
      </DialogPrimitive.Portal>
    </DialogPrimitive.Root>
  );
}
