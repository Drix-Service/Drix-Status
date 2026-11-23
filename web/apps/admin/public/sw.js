// Drix app service worker: shows push notifications, opens the right screen
// when one is tapped, and keeps the app's files on the device so it opens
// without waiting for the network.
//
// - /assets/* have a content hash in their name and never change: served from
//   the cache, fetched once.
// - Phone app pages (/m) get the saved page at once while a fresh copy is
//   fetched for next time, so a new version shows on the following launch.
//   Other pages try the network first and use the saved page only offline.
// - /api is never cached.

const PAGES = "drix-pages-v1";
const ASSETS = "drix-assets-v1";
const KEEP = [PAGES, ASSETS];
const MAX_AGE = 60 * 24 * 60 * 60 * 1000; // unused assets are dropped after 60 days

self.addEventListener("install", (event) => {
  event.waitUntil(
    (async () => {
      try {
        const res = await fetch("/m", { cache: "no-cache" });
        if (res.ok) await (await caches.open(PAGES)).put("/index.html", res);
      } catch {
        /* offline: the first real load fills the cache */
      }
      await self.skipWaiting();
    })(),
  );
});

self.addEventListener("activate", (event) => {
  event.waitUntil(
    (async () => {
      for (const name of await caches.keys()) if (!KEEP.includes(name)) await caches.delete(name);
      const cache = await caches.open(ASSETS);
      const now = Date.now();
      for (const req of await cache.keys()) {
        const res = await cache.match(req);
        const at = Number(res && res.headers.get("x-drix-cached-at"));
        if (!at || now - at > MAX_AGE) await cache.delete(req);
      }
      await self.clients.claim();
    })(),
  );
});

/** Stores a copy stamped with the time, so old files can be cleaned up. */
async function stamp(res) {
  const headers = new Headers(res.headers);
  headers.set("x-drix-cached-at", String(Date.now()));
  return new Response(await res.blob(), { status: res.status, statusText: res.statusText, headers });
}

async function asset(request) {
  const cache = await caches.open(ASSETS);
  const hit = await cache.match(request);
  if (hit) return hit;
  const res = await fetch(request);
  if (res.ok && res.type === "basic") await cache.put(request, await stamp(res.clone()));
  return res;
}

async function page(event, savedFirst) {
  const cache = await caches.open(PAGES);
  const fresh = fetch(event.request, { cache: "no-cache" })
    .then(async (res) => {
      const type = res.headers.get("content-type") || "";
      if (res.ok && res.type === "basic" && !res.redirected && type.includes("text/html")) await cache.put("/index.html", res.clone());
      return res;
    })
    .catch(() => null);
  if (savedFirst) {
    const saved = await cache.match("/index.html");
    if (saved) {
      event.waitUntil(fresh);
      return saved;
    }
  }
  return (await fresh) || (await cache.match("/index.html")) || Response.error();
}

self.addEventListener("fetch", (event) => {
  const { request } = event;
  if (request.method !== "GET") return;
  const url = new URL(request.url);
  if (url.origin !== self.location.origin || url.pathname.startsWith("/api/")) return;
  if (url.pathname.startsWith("/assets/")) return event.respondWith(asset(request));
  if (request.mode === "navigate") return event.respondWith(page(event, url.pathname === "/m" || url.pathname.startsWith("/m/")));
});

self.addEventListener("push", (event) => {
  let data = {};
  try {
    data = event.data ? event.data.json() : {};
  } catch {
    data = { title: "Drix", body: event.data ? event.data.text() : "" };
  }
  const title = data.title || "Drix";
  event.waitUntil(
    self.registration.showNotification(title, {
      body: data.body || "",
      tag: data.tag || undefined,
      renotify: Boolean(data.tag),
      icon: "/icon-192.png",
      badge: "/icon-192.png",
      data: { url: data.url || "/m", ack: data.ack || "" },
      // Android and desktop show this button; iOS opens the monitor instead.
      actions: data.ack ? [{ action: "ack", title: "Acknowledge" }] : [],
    }),
  );
});

self.addEventListener("notificationclick", (event) => {
  event.notification.close();
  const d = event.notification.data || {};
  const path = (event.action === "ack" && d.ack) || d.url || "/m";
  const target = new URL(path, self.location.origin).href;
  event.waitUntil(
    (async () => {
      const windows = await self.clients.matchAll({ type: "window", includeUncontrolled: true });
      for (const w of windows) {
        if (new URL(w.url).origin === self.location.origin) {
          await w.focus();
          if ("navigate" in w) return w.navigate(target);
          return;
        }
      }
      return self.clients.openWindow(target);
    })(),
  );
});
