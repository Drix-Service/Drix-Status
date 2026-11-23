import { dehydrate, hydrate, type DehydratedState, type QueryClient } from "@tanstack/react-query";

/**
 * The phone app keeps its last answers (who is signed in, monitors, groups,
 * open incidents) on the device, so it opens straight to the last known
 * status and then refreshes. Signing out or losing the session clears it.
 */
const KEY = "drix-app-cache";
const MAX_AGE = 24 * 60 * 60 * 1000;
const SAVED = new Set(["me", "monitors", "groups", "m-incidents"]);

/** When this page load started: data older than this was restored, not fetched. */
export const bootTime = Date.now();

export function clearSavedCache() {
  try {
    localStorage.removeItem(KEY);
  } catch {
    /* storage unavailable */
  }
}

/** Restores the saved answers into the cache and keeps saving changes. */
export function persistQueries(qc: QueryClient) {
  try {
    const raw = localStorage.getItem(KEY);
    if (raw) {
      const saved = JSON.parse(raw) as { at: number; state: DehydratedState };
      if (Date.now() - saved.at < MAX_AGE) {
        hydrate(qc, saved.state);
        // Saved answers are only a head start: always ask again on open.
        void qc.invalidateQueries({ predicate: (q) => SAVED.has(String(q.queryKey[0])), refetchType: "none" });
      } else clearSavedCache();
    }
  } catch {
    clearSavedCache();
  }

  let timer: number | undefined;
  const save = () => {
    timer = undefined;
    const state = dehydrate(qc, {
      // Keep the last good answer even if the latest refresh failed (server down).
      shouldDehydrateQuery: (q) => q.state.data !== undefined && SAVED.has(String(q.queryKey[0])),
    });
    for (const q of state.queries) {
      q.state = { ...q.state, status: "success", error: null, fetchFailureReason: null, fetchStatus: "idle" };
    }
    try {
      if (state.queries.length === 0) localStorage.removeItem(KEY);
      else localStorage.setItem(KEY, JSON.stringify({ at: Date.now(), state }));
    } catch {
      /* full or unavailable: the app still works, just without the head start */
    }
  };
  qc.getQueryCache().subscribe((e) => {
    if (e.type !== "updated" && e.type !== "removed") return;
    if (!SAVED.has(String(e.query.queryKey[0]))) return;
    if (timer === undefined) timer = window.setTimeout(save, 2000);
  });
}
