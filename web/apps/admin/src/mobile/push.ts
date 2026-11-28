import { api } from "../api";

export type PushDevice = { id: number; endpoint: string; events: string[]; device: string; created_at: string; last_used_at: string | null };
export type PushInfo = { public_key: string; events: string[]; devices: PushDevice[]; error?: string };

/** Why this browser can't do push, or null when it can. */
export function pushUnavailable(): "insecure" | "install" | "unsupported" | null {
  // Browsers only allow push (and the service worker) over HTTPS.
  if (!window.isSecureContext) return "insecure";
  const ios = /iPhone|iPad|iPod/.test(navigator.userAgent);
  const standalone = window.matchMedia("(display-mode: standalone)").matches || (navigator as { standalone?: boolean }).standalone === true;
  if (ios && !standalone) return "install"; // iOS only allows push for Home Screen apps
  if (!("serviceWorker" in navigator) || !("PushManager" in window) || !("Notification" in window)) return "unsupported";
  return null;
}

/** A short name for this device, e.g. "iPhone · Safari". */
export function deviceLabel(): string {
  const ua = navigator.userAgent;
  const device = /iPhone/.test(ua) ? "iPhone" : /iPad/.test(ua) ? "iPad" : /Android/.test(ua) ? "Android" : /Mac/.test(ua) ? "Mac" : /Windows/.test(ua) ? "Windows" : "Browser";
  const app = window.matchMedia("(display-mode: standalone)").matches ? "Drix app" : /Edg\//.test(ua) ? "Edge" : /Chrome\//.test(ua) ? "Chrome" : /Firefox\//.test(ua) ? "Firefox" : /Safari\//.test(ua) ? "Safari" : "";
  return [device, app].filter(Boolean).join(" · ");
}

function keyBytes(base64url: string): Uint8Array<ArrayBuffer> {
  const pad = "=".repeat((4 - (base64url.length % 4)) % 4);
  const raw = atob((base64url + pad).replace(/-/g, "+").replace(/_/g, "/"));
  const out = new Uint8Array(new ArrayBuffer(raw.length));
  for (let i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i);
  return out;
}

async function registration() {
  return (await navigator.serviceWorker.getRegistration("/")) ?? navigator.serviceWorker.register("/sw.js");
}

/** This browser's current push subscription endpoint, if any. */
export async function currentEndpoint(): Promise<string | null> {
  if (pushUnavailable()) return null;
  const reg = await navigator.serviceWorker.getRegistration("/");
  const sub = await reg?.pushManager.getSubscription();
  return sub?.endpoint ?? null;
}

/** Asks for permission, subscribes and saves the device on the server. */
export async function enablePush(publicKey: string, events: string[]): Promise<PushDevice> {
  const permission = await Notification.requestPermission();
  if (permission !== "granted") {
    throw new Error(permission === "denied" ? "Notifications are blocked. Allow them in Settings → Notifications → Drix." : "Notifications weren't allowed.");
  }
  const reg = await registration();
  await navigator.serviceWorker.ready;
  let sub = await reg.pushManager.getSubscription();
  if (!sub) {
    sub = await reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: keyBytes(publicKey) });
  }
  const json = sub.toJSON();
  return api.post<PushDevice>("/push/subscribe", {
    endpoint: sub.endpoint,
    p256dh: json.keys?.p256dh,
    auth: json.keys?.auth,
    events,
    device: deviceLabel(),
  });
}

/** Unsubscribes this browser and removes the device on the server. */
export async function disablePush(deviceId?: number) {
  const reg = await navigator.serviceWorker.getRegistration("/");
  const sub = await reg?.pushManager.getSubscription();
  await sub?.unsubscribe().catch(() => {});
  if (deviceId) await api.del(`/push/subscriptions/${deviceId}`);
}
