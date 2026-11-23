import { Suspense, useEffect } from "react";
import { NavLink, Navigate, Route, Routes } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { ActivityIcon, SettingsIcon, TriangleAlertIcon } from "lucide-react";
import { cn } from "@drix/ui/lib/utils";
import { useLiveAdmin } from "../lib/queries";
import { openIncidentsQuery } from "./queries";
import { setNav } from "./ui";
import { lazyScreen } from "../lib/screen";
import StatusScreen from "./StatusScreen";
import "./ios.css";

const MonitorScreen = lazyScreen(() => import("./MonitorScreen"));
const IncidentsScreen = lazyScreen(() => import("./IncidentsScreen"));
const IncidentScreen = lazyScreen(() => import("./IncidentScreen"));
const SettingsScreen = lazyScreen(() => import("./SettingsScreen"));
const screens = [MonitorScreen, IncidentsScreen, IncidentScreen, SettingsScreen];

/** Loads the screen a link (e.g. from a notification) opens on, before the first render. */
export function preloadScreen(path: string): Promise<unknown> {
  if (/^\/m\/monitors\/./.test(path)) return MonitorScreen.preload();
  if (/^\/m\/incidents\/./.test(path)) return IncidentScreen.preload();
  if (path.startsWith("/m/incidents")) return IncidentsScreen.preload();
  if (path.startsWith("/m/settings")) return SettingsScreen.preload();
  return Promise.resolve();
}

/** The phone app: iOS look, screens above a tab bar, inside the safe areas. */
export default function MobileApp() {
  useLiveAdmin(true);
  const open = useQuery(openIncidentsQuery);
  const badge = (open.data ?? []).filter((i) => i.kind === "incident").length;

  // Once the first screen is up, fetch the others quietly so tapping never waits.
  useEffect(() => {
    const load = () => screens.forEach((s) => void s.preload().catch(() => {}));
    const w = window as Window & { requestIdleCallback?: (cb: () => void) => number };
    if (w.requestIdleCallback) w.requestIdleCallback(load);
    else window.setTimeout(load, 1500);
  }, []);

  // The iOS look applies to <html> so sheets (rendered in a portal) get it too.
  useEffect(() => {
    const root = document.documentElement;
    root.classList.add("ios");
    return () => root.classList.remove("ios");
  }, []);

  const tabs = [
    { to: "/m", label: "Status", icon: ActivityIcon, end: true },
    { to: "/m/incidents", label: "Incidents", icon: TriangleAlertIcon, badge },
    { to: "/m/settings", label: "Settings", icon: SettingsIcon },
  ];

  return (
    <div className="min-h-dvh text-[var(--ios-label)]">
      <main className="mx-auto max-w-lg px-4 pt-[env(safe-area-inset-top)] pb-[calc(env(safe-area-inset-bottom)+84px)]">
        <Suspense fallback={null}>
          <Routes>
            <Route index element={<StatusScreen />} />
            <Route path="monitors/:id" element={<MonitorScreen />} />
            <Route path="incidents" element={<IncidentsScreen />} />
            <Route path="incidents/:id" element={<IncidentScreen />} />
            <Route path="settings" element={<SettingsScreen />} />
            <Route path="*" element={<Navigate to="/m" replace />} />
          </Routes>
        </Suspense>
      </main>

      <nav className="ios-tabbar fixed inset-x-0 bottom-0 z-40 border-t-[0.5px] border-[var(--ios-sep)] bg-[var(--ios-bar)] pb-[env(safe-area-inset-bottom)] backdrop-blur-xl backdrop-saturate-150">
        <div className="mx-auto flex h-[49px] max-w-lg">
          {tabs.map(({ to, label, icon: Icon, end, badge }) => (
            <NavLink
              key={to}
              to={to}
              end={end}
              viewTransition
              onClick={() => {
                setNav("tab");
                window.scrollTo(0, 0);
              }}
              className={({ isActive }) =>
                cn("flex flex-1 flex-col items-center justify-center gap-[2px] pt-1 text-[10px] font-medium tracking-[0.01em]", isActive ? "text-[var(--ios-tint)]" : "text-[var(--ios-gray)]")
              }
            >
              {({ isActive }) => (
                <>
                  <span className="relative">
                    <Icon className="size-[25px]" strokeWidth={isActive ? 2.3 : 1.7} />
                    {!!badge && (
                      <span className="absolute -top-1 -right-2.5 flex h-[18px] min-w-[18px] items-center justify-center rounded-full bg-[var(--ios-red)] px-1 text-[12px] font-medium text-white">
                        {badge}
                      </span>
                    )}
                  </span>
                  {label}
                </>
              )}
            </NavLink>
          ))}
        </div>
      </nav>
    </div>
  );
}
