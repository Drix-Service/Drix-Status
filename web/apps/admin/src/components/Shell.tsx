import { useEffect, useState, type ReactNode } from "react";
import { NavLink, useLocation, useNavigate } from "react-router";
import { useQueryClient } from "@tanstack/react-query";
import {
  ActivityIcon,
  BellIcon,
  CalendarClockIcon,
  SirenIcon,
  ArrowUpRightIcon,
  CpuIcon,
  FolderTreeIcon,
  KeyRoundIcon,
  LayoutDashboardIcon,
  LogOutIcon,
  MenuIcon,
  MonitorIcon,
  MoonIcon,
  PlusIcon,
  RadarIcon,
  SearchIcon,
  SettingsIcon,
  SunIcon,
  UsersIcon,
  ShieldCheckIcon,
} from "lucide-react";
import { cn } from "@drix/ui/lib/utils";
import { useTheme } from "@drix/ui/lib/theme";
import { Button } from "@drix/ui/components/button";
import { Kbd } from "@drix/ui/components/display";
import { Sheet, SheetContent, SheetTitle } from "@drix/ui/components/overlay";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@drix/ui/components/menu";
import { CommandDialog, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList } from "@drix/ui/components/command";
import { StatusDot, LiveIndicator } from "@drix/ui/drix/status";
import { api } from "../api";
import { clearSavedCache } from "../lib/persist";
import { useAgents, useLiveAdmin, useMe, useMonitors } from "../lib/queries";
import { PasswordDialog } from "./PasswordDialog";
import { roleLabel, useCan } from "../lib/roles";

export const statusPageUrl =
  (import.meta.env.VITE_STATUS_URL as string | undefined) || `${location.protocol}//${location.hostname}:3000`;

const nav = [
  { to: "/", label: "Overview", icon: LayoutDashboardIcon, end: true },
  { to: "/monitors", label: "Monitors", icon: RadarIcon },
  { to: "/incidents", label: "Incidents", icon: SirenIcon },
  { to: "/maintenance", label: "Maintenance", icon: CalendarClockIcon },
  { to: "/groups", label: "Groups", icon: FolderTreeIcon },
  { to: "/alerts", label: "Alerts", icon: BellIcon },
  { to: "/workers", label: "Workers", icon: CpuIcon },
  { to: "/events", label: "Events", icon: ActivityIcon },
  { to: "/team", label: "Team", icon: UsersIcon },
  { to: "/settings", label: "Settings", icon: SettingsIcon },
];

export function Logo({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 32 32" className={cn("size-6", className)} aria-hidden>
      <rect width="32" height="32" rx="8" className="fill-primary" />
      <path d="M9 16h4l2.5-6 3 12 2.5-6h2" fill="none" className="stroke-primary-foreground" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

function Sidebar({ onNavigate }: { onNavigate?: () => void }) {
  const { data: monitors } = useMonitors();
  const { data: agents } = useAgents();
  const down = monitors?.filter((m) => m.live.status === "down").length ?? 0;
  const online = agents?.agents.filter((a) => a.online).length ?? 0;

  return (
    <div className="flex h-full flex-col">
      <div className="flex h-14 items-center gap-2.5 px-4">
        <Logo />
        <span className="text-[14px] font-semibold tracking-tight">Drix</span>
        <span className="rounded border px-1 py-px text-[10px] font-medium text-muted-foreground">Admin</span>
      </div>
      <nav className="grid gap-0.5 px-2 py-2">
        {nav.map(({ to, label, icon: Icon, end }) => (
          <NavLink
            key={to}
            to={to}
            end={end}
            onClick={onNavigate}
            className={({ isActive }) =>
              cn(
                "flex h-8 items-center gap-2.5 rounded-md px-2.5 text-[13px] font-medium text-muted-foreground transition-colors hover:bg-accent hover:text-foreground",
                isActive && "bg-accent text-foreground",
              )
            }
          >
            <Icon className="size-4" />
            <span className="flex-1">{label}</span>
            {to === "/monitors" && down > 0 && (
              <span className="tabular rounded-full bg-down/12 px-1.5 text-[11px] font-semibold text-down">{down}</span>
            )}
          </NavLink>
        ))}
      </nav>
      <div className="mt-auto border-t p-3">
        <NavLink
          to="/workers"
          onClick={onNavigate}
          className="flex items-center gap-2.5 rounded-md px-1.5 py-1.5 text-xs text-muted-foreground transition-colors hover:text-foreground"
        >
          <StatusDot status={online > 0 ? "up" : "down"} size="sm" />
          <span className="tabular">
            {online} {online === 1 ? "worker" : "workers"} online
          </span>
        </NavLink>
        <a
          href={statusPageUrl}
          target="_blank"
          rel="noreferrer"
          className="flex items-center gap-2.5 rounded-md px-1.5 py-1.5 text-xs text-muted-foreground transition-colors hover:text-foreground"
        >
          <ArrowUpRightIcon className="size-3.5" />
          Open status page
        </a>
      </div>
    </div>
  );
}

function CommandMenu({ open, setOpen }: { open: boolean; setOpen: (o: boolean) => void }) {
  const navigate = useNavigate();
  const { data: monitors } = useMonitors();
  const { setTheme } = useTheme();
  const can = useCan();
  const go = (to: string) => {
    setOpen(false);
    navigate(to);
  };
  return (
    <CommandDialog open={open} onOpenChange={setOpen}>
      <CommandInput placeholder="Search monitors, pages and actions…" />
      <CommandList>
        <CommandEmpty>No results.</CommandEmpty>
        {can("editor") && (
          <CommandGroup heading="Actions">
            <CommandItem onSelect={() => go("/monitors?new=1")}>
              <PlusIcon /> New monitor
            </CommandItem>
            <CommandItem onSelect={() => go("/incidents?new=1")}>
              <SirenIcon /> Declare incident
            </CommandItem>
            <CommandItem onSelect={() => go("/maintenance?new=1")}>
              <CalendarClockIcon /> Schedule maintenance
            </CommandItem>
            {can("admin") && (
              <CommandItem onSelect={() => go("/workers?new=1")}>
                <CpuIcon /> Add worker
              </CommandItem>
            )}
            <CommandItem onSelect={() => go("/groups")}>
              <FolderTreeIcon /> Edit groups
            </CommandItem>
          </CommandGroup>
        )}
        <CommandGroup heading="Pages">
          {nav.map(({ to, label, icon: Icon }) => (
            <CommandItem key={to} onSelect={() => go(to)}>
              <Icon /> {label}
            </CommandItem>
          ))}
        </CommandGroup>
        {!!monitors?.length && (
          <CommandGroup heading="Monitors">
            {monitors.map((m) => (
              <CommandItem key={m.id} value={`${m.name} ${m.target} #${m.id}`} onSelect={() => go(`/monitors/${m.id}`)}>
                <StatusDot status={m.live.status} size="sm" />
                <span className="truncate">{m.name}</span>
                <span className="ml-auto truncate text-xs text-muted-foreground">{m.target}</span>
              </CommandItem>
            ))}
          </CommandGroup>
        )}
        <CommandGroup heading="Theme">
          <CommandItem onSelect={() => { setTheme("light"); setOpen(false); }}><SunIcon /> Light</CommandItem>
          <CommandItem onSelect={() => { setTheme("dark"); setOpen(false); }}><MoonIcon /> Dark</CommandItem>
          <CommandItem onSelect={() => { setTheme("system"); setOpen(false); }}><MonitorIcon /> System</CommandItem>
        </CommandGroup>
      </CommandList>
    </CommandDialog>
  );
}

export function Shell({ children }: { children: ReactNode }) {
  const [cmd, setCmd] = useState(false);
  const [mobileNav, setMobileNav] = useState(false);
  const [pwOpen, setPwOpen] = useState(false);
  const { data: me } = useMe();
  const { theme, setTheme } = useTheme();
  const connected = useLiveAdmin(!!me);
  const navigate = useNavigate();
  const qc = useQueryClient();
  const location = useLocation();

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "k" && (e.metaKey || e.ctrlKey)) {
        e.preventDefault();
        setCmd((o) => !o);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  useEffect(() => {
    // Braces matter: newer browsers return a Promise from scrollTo, which React would treat as a cleanup.
    window.scrollTo(0, 0);
  }, [location.pathname]);

  const logout = async () => {
    await api.post("/auth/logout").catch(() => {});
    qc.clear();
    clearSavedCache();
    navigate("/login", { replace: true });
  };

  const ThemeIcon = theme === "dark" ? MoonIcon : theme === "light" ? SunIcon : MonitorIcon;

  return (
    <div className="min-h-dvh bg-background">
      <aside className="fixed inset-y-0 left-0 z-30 hidden w-[232px] border-r bg-sidebar lg:block">
        <Sidebar />
      </aside>
      <Sheet open={mobileNav} onOpenChange={setMobileNav}>
        <SheetContent className="left-0 right-auto w-[260px] border-r border-l-0 bg-sidebar data-[state=open]:slide-in-from-left data-[state=closed]:slide-out-to-left sm:max-w-[260px]">
          <SheetTitle className="sr-only">Navigation</SheetTitle>
          <Sidebar onNavigate={() => setMobileNav(false)} />
        </SheetContent>
      </Sheet>

      <div className="lg:pl-[232px]">
        <header className="sticky top-0 z-20 flex h-14 items-center gap-2 border-b bg-background/85 px-4 backdrop-blur-md sm:px-6">
          <Button variant="ghost" size="icon" className="lg:hidden" onClick={() => setMobileNav(true)} aria-label="Open navigation">
            <MenuIcon />
          </Button>
          <button
            onClick={() => setCmd(true)}
            className="flex h-8 w-full max-w-72 items-center gap-2 rounded-md border bg-muted/40 px-2.5 text-[13px] text-muted-foreground transition-colors hover:bg-muted"
          >
            <SearchIcon className="size-4" />
            <span className="flex-1 text-left">Search…</span>
            <Kbd className="hidden sm:inline-flex">⌘K</Kbd>
          </button>
          <div className="ml-auto flex items-center gap-1.5">
            <LiveIndicator connected={connected} className="mr-2 hidden sm:inline-flex" />
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button variant="ghost" size="icon" aria-label="Theme">
                  <ThemeIcon />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end" className="min-w-36">
                <DropdownMenuItem onSelect={() => setTheme("light")}><SunIcon /> Light</DropdownMenuItem>
                <DropdownMenuItem onSelect={() => setTheme("dark")}><MoonIcon /> Dark</DropdownMenuItem>
                <DropdownMenuItem onSelect={() => setTheme("system")}><MonitorIcon /> System</DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <button className="flex size-8 items-center justify-center rounded-full bg-muted text-xs font-semibold uppercase outline-none ring-ring/40 focus-visible:ring-2">
                  {me?.user.email.slice(0, 1) ?? "?"}
                </button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end" className="min-w-52">
                <DropdownMenuLabel className="font-normal">
                  <p className="text-[13px] font-medium text-foreground">{me?.user.name || "Account"}</p>
                  <p className="truncate">{me?.user.email}</p>
                  {me && <p className="mt-1 text-[11px]">{roleLabel[me.user.role]}</p>}
                </DropdownMenuLabel>
                <DropdownMenuSeparator />
                <DropdownMenuItem onSelect={() => setPwOpen(true)}>
                  <KeyRoundIcon /> Change password
                </DropdownMenuItem>
                <DropdownMenuItem onSelect={() => navigate("/settings#two-factor")}>
                  <ShieldCheckIcon /> Two-factor sign-in
                </DropdownMenuItem>
                <DropdownMenuItem onSelect={logout}>
                  <LogOutIcon /> Sign out
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </div>
        </header>
        <main className="mx-auto w-full max-w-[76rem] px-4 py-6 sm:px-6 sm:py-8">{children}</main>
      </div>
      <CommandMenu open={cmd} setOpen={setCmd} />
      <PasswordDialog open={pwOpen} onOpenChange={setPwOpen} />
    </div>
  );
}
