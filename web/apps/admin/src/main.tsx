import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ThemeProvider } from "@drix/ui/lib/theme";
import { ApiError } from "./api";
import { App, MobileApp } from "./App";
import { persistQueries } from "./lib/persist";
import "./index.css";

const qc = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 10_000,
      refetchOnWindowFocus: true,
      retry: (n, err) => !(err instanceof ApiError && err.status < 500) && n < 2,
    },
  },
});

const inPhoneApp = window.location.pathname === "/m" || window.location.pathname.startsWith("/m/");

// The phone app opens on its last known data, then refreshes.
if (inPhoneApp) persistQueries(qc);

// A saved older version may ask for a file a newer deploy replaced: reload
// once to get the new version (the service worker has fetched it by now).
window.addEventListener("vite:preloadError", (e) => {
  try {
    const last = Number(sessionStorage.getItem("drix-reloaded") || 0);
    if (Date.now() - last < 30_000) return;
    sessionStorage.setItem("drix-reloaded", String(Date.now()));
  } catch {
    return;
  }
  e.preventDefault();
  window.location.reload();
});

// The service worker shows push notifications and keeps the app's files on the device.
if ("serviceWorker" in navigator) {
  navigator.serviceWorker.register("/sw.js").catch(() => {});
}

// The phone app's files come from the service worker in a few milliseconds;
// loading them before the first render means its screen shows at once.
if (inPhoneApp) {
  try {
    const mobile = await MobileApp.preload();
    await mobile.preloadScreen(window.location.pathname);
  } catch {
    /* render anyway: the screen loads the normal way */
  }
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <ThemeProvider>
      <QueryClientProvider client={qc}>
        <App />
      </QueryClientProvider>
    </ThemeProvider>
  </StrictMode>,
);
