import { lazy, Suspense, useEffect } from "react";
import { BrowserRouter, Navigate, Route, Routes, useLocation, useNavigate } from "react-router";
import { useQueryClient } from "@tanstack/react-query";
import { Toaster } from "sonner";
import { TooltipProvider } from "@drix/ui/components/overlay";
import { useTheme } from "@drix/ui/lib/theme";
import { Shell } from "./components/Shell";
import { LoginPage } from "./pages/Login";
import { AcceptInvitePage } from "./pages/AcceptInvite";
import { AckPage } from "./pages/Ack";
import { useMe } from "./lib/queries";
import { api, ApiError } from "./api";
import { TwoFactorSetupScreen } from "./components/TwoFactor";
import { PageSkeleton } from "./components/Page";
import { clearSavedCache } from "./lib/persist";
import { lazyScreen } from "./lib/screen";
import { desktopPath, isPhone, isStandalone } from "./lib/device";

const Overview = lazy(() => import("./pages/Overview"));
const Monitors = lazy(() => import("./pages/Monitors"));
const MonitorDetail = lazy(() => import("./pages/MonitorDetail"));
const Groups = lazy(() => import("./pages/Groups"));
const Workers = lazy(() => import("./pages/Workers"));
const Events = lazy(() => import("./pages/Events"));
const Settings = lazy(() => import("./pages/Settings"));
const Team = lazy(() => import("./pages/Team"));
const Incidents = lazy(() => import("./pages/Incidents"));
const Maintenance = lazy(() => import("./pages/Maintenance"));
const Alerts = lazy(() => import("./pages/Alerts"));
const IncidentDetail = lazy(() => import("./pages/IncidentDetail"));
/** The phone app; main.tsx loads it before the first render when it opens on /m. */
export const MobileApp = lazyScreen(() => import("./mobile/MobileApp"));

/** On a phone, the Home Screen app opens on the phone screens, unless
 *  "Open full admin" was chosen. Macs, PCs and iPads stay on the full admin. */
function StandaloneHome({ children }: { children: React.ReactNode }) {
  const location = useLocation();
  let wantsFull = false;
  try {
    wantsFull = sessionStorage.getItem("drix-full-admin") === "1";
  } catch {
    /* ignore */
  }
  if (location.pathname === "/" && isStandalone() && isPhone() && !wantsFull) return <Navigate to="/m" replace />;
  return <>{children}</>;
}

/** Phone-app addresses (from notifications or bookmarks) open the matching
 *  full-admin page on Macs, PCs and iPads. */
function PhoneOnly({ children }: { children: React.ReactNode }) {
  const { pathname, search, hash } = useLocation();
  if (!isPhone()) {
    const q = new URLSearchParams(search);
    q.delete("phone");
    const rest = q.toString();
    const to = desktopPath(pathname);
    return <Navigate to={`${to}${rest ? `?${rest}` : ""}${to.includes("#") ? "" : hash}`} replace />;
  }
  return <>{children}</>;
}

function AppToaster() {
  const { theme } = useTheme();
  const mobile = useLocation().pathname.startsWith("/m");
  return (
    <Toaster
      theme={theme}
      position={mobile ? "top-center" : "bottom-right"}
      offset={mobile ? "calc(env(safe-area-inset-top) + 12px)" : undefined}
      toastOptions={mobile ? undefined : { className: "!rounded-lg !border !text-[13px] !shadow-lg" }}
    />
  );
}

function AuthGate({ children }: { children: React.ReactNode }) {
  const { data, isLoading, error } = useMe();
  const location = useLocation();
  const navigate = useNavigate();
  const qc = useQueryClient();

  useEffect(() => {
    const onUnauthorized = () => {
      qc.clear();
      clearSavedCache();
      navigate("/login", { replace: true, state: { from: location.pathname } });
    };
    const onSetupRequired = () => qc.invalidateQueries({ queryKey: ["me"] });
    window.addEventListener("drix:unauthorized", onUnauthorized);
    window.addEventListener("drix:2fa-required", onSetupRequired);
    return () => {
      window.removeEventListener("drix:unauthorized", onUnauthorized);
      window.removeEventListener("drix:2fa-required", onSetupRequired);
    };
  }, [navigate, location.pathname, qc]);

  if (isLoading) {
    return (
      <div className="flex h-dvh items-center justify-center">
        <div className="size-5 animate-spin rounded-full border-2 border-muted border-t-foreground" />
      </div>
    );
  }
  // Only "not signed in" means sign in again; if the server is just unreachable,
  // keep showing what we have (the phone app opens on its saved data).
  const signedOut = error instanceof ApiError && error.status === 401;
  if (signedOut || !data) return <Navigate to="/login" replace state={{ from: location.pathname }} />;
  if (data.two_factor_setup_required) {
    return (
      <TwoFactorSetupScreen
        onSignOut={async () => {
          await api.post("/auth/logout").catch(() => {});
          qc.clear();
          clearSavedCache();
          navigate("/login", { replace: true });
        }}
      />
    );
  }
  return <>{children}</>;
}

export function App() {
  return (
    <BrowserRouter>
      <TooltipProvider>
        <Routes>
          <Route path="/login" element={<LoginPage />} />
          <Route path="/accept-invite" element={<AcceptInvitePage />} />
          <Route path="/ack/:token" element={<AckPage />} />
          <Route
            path="/m/*"
            element={
              <PhoneOnly>
                <AuthGate>
                  <Suspense fallback={null}>
                    <MobileApp />
                  </Suspense>
                </AuthGate>
              </PhoneOnly>
            }
          />
          <Route
            path="*"
            element={
              <AuthGate>
                <StandaloneHome>
                <Shell>
                  <Suspense fallback={<PageSkeleton />}>
                    <Routes>
                      <Route index element={<Overview />} />
                      <Route path="monitors" element={<Monitors />} />
                      <Route path="monitors/:id" element={<MonitorDetail />} />
                      <Route path="groups" element={<Groups />} />
                      <Route path="pages/*" element={<Navigate to="/groups" replace />} />
                      <Route path="workers" element={<Workers />} />
                      <Route path="events" element={<Events />} />
                      <Route path="settings" element={<Settings />} />
                      <Route path="team" element={<Team />} />
                      <Route path="incidents" element={<Incidents />} />
                      <Route path="incidents/:id" element={<IncidentDetail />} />
                      <Route path="maintenance" element={<Maintenance />} />
                      <Route path="alerts" element={<Alerts />} />
                      <Route path="*" element={<Navigate to="/" replace />} />
                    </Routes>
                  </Suspense>
                </Shell>
                </StandaloneHome>
              </AuthGate>
            }
          />
        </Routes>
        <AppToaster />
      </TooltipProvider>
    </BrowserRouter>
  );
}
