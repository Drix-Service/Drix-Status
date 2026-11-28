/**
 * Which screens a device gets: phones get the phone app (/m), everything else
 * (Macs, PCs, iPads) gets the full admin, also when installed as an app.
 */

const PHONE_VIEW = "drix-phone-view";

/** Opened from a Home Screen / Dock / "Install app" icon, not a browser tab. */
export const isStandalone = () =>
  window.matchMedia("(display-mode: standalone)").matches || (navigator as { standalone?: boolean }).standalone === true;

/**
 * iPhones and Android phones: a touch screen whose shorter side is small.
 * Decided by the device, not the window, so a narrow Mac window stays desktop.
 * `?phone` on any /m address shows the phone app anyway (for trying it out).
 */
export function isPhone(): boolean {
  try {
    if (new URLSearchParams(window.location.search).has("phone")) sessionStorage.setItem(PHONE_VIEW, "1");
    if (sessionStorage.getItem(PHONE_VIEW) === "1") return true;
  } catch {
    /* storage unavailable */
  }
  const coarse = window.matchMedia("(pointer: coarse)").matches;
  return coarse && Math.min(window.screen.width, window.screen.height) < 600;
}

/** The phone app opens at /m; same path test everywhere. */
export const isPhonePath = (path: string) => path === "/m" || path.startsWith("/m/");

/** The full-admin page that matches a phone-app address. */
export function desktopPath(path: string): string {
  const m = path.match(/^\/m\/(monitors\/\d+|incidents(?:\/\d+)?|settings)\/?$/);
  if (!m) return "/";
  // The phone app's Settings is mostly notifications: open that section.
  return m[1] === "settings" ? "/settings#notifications" : `/${m[1]}`;
}
