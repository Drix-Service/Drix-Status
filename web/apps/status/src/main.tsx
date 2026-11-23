import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ThemeProvider } from "@drix/ui/lib/theme";
import { BrowserRouter, Route, Routes } from "react-router";
import { App } from "./App";
import { HistoryPage, IncidentPage } from "./pages";
import { I18nProvider } from "./i18n";
import "./index.css";

const qc = new QueryClient({
  defaultOptions: {
    queries: { refetchOnWindowFocus: true, retry: 2, staleTime: 5_000 },
  },
});

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <ThemeProvider>
      <QueryClientProvider client={qc}>
        <I18nProvider>
        <BrowserRouter>
          <Routes>
            <Route path="/" element={<App />} />
            <Route path="/history" element={<HistoryPage />} />
            <Route path="/incidents/:id" element={<IncidentPage />} />
          </Routes>
        </BrowserRouter>
        </I18nProvider>
      </QueryClientProvider>
    </ThemeProvider>
  </StrictMode>,
);
