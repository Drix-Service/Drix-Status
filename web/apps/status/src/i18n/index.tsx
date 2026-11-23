import { createContext, Fragment, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { UiTextProvider, type UiText } from "@drix/ui/lib/i18n";
import { fetchStatus } from "../api";
import { en, type Messages, type Text } from "./en";
import { es } from "./es";
import { fr } from "./fr";
import { de } from "./de";
import { pt } from "./pt";
import { it } from "./it";
import { nl } from "./nl";
import { pl } from "./pl";
import { da } from "./da";
import { sv } from "./sv";
import { nb } from "./nb";
import { fi } from "./fi";
import { ja } from "./ja";
import { zh } from "./zh";
import { ko } from "./ko";

/** Supported languages: code, name in that language, locale for dates and numbers. */
export const languages = [
  { code: "en", name: "English", locale: "en", messages: en },
  { code: "es", name: "Español", locale: "es", messages: es },
  { code: "fr", name: "Français", locale: "fr", messages: fr },
  { code: "de", name: "Deutsch", locale: "de", messages: de },
  { code: "pt", name: "Português", locale: "pt", messages: pt },
  { code: "it", name: "Italiano", locale: "it", messages: it },
  { code: "nl", name: "Nederlands", locale: "nl", messages: nl },
  { code: "pl", name: "Polski", locale: "pl", messages: pl },
  { code: "da", name: "Dansk", locale: "da", messages: da },
  { code: "sv", name: "Svenska", locale: "sv", messages: sv },
  { code: "nb", name: "Norsk", locale: "nb", messages: nb },
  { code: "fi", name: "Suomi", locale: "fi", messages: fi },
  { code: "ja", name: "日本語", locale: "ja", messages: ja },
  { code: "zh", name: "中文（简体）", locale: "zh-CN", messages: zh },
  { code: "ko", name: "한국어", locale: "ko", messages: ko },
] as const;

export type Lang = (typeof languages)[number]["code"];

const KEY = "drix-lang";
const isLang = (c: string | null | undefined): c is Lang => !!c && languages.some((l) => l.code === c);

/** The first browser language we support (nn/no count as Norwegian). */
function browserLang(): Lang | null {
  for (const raw of navigator.languages ?? [navigator.language]) {
    const base = raw.toLowerCase().split("-")[0];
    const code = base === "no" || base === "nn" ? "nb" : base;
    if (isLang(code)) return code;
  }
  return null;
}

function storedLang(): Lang | null {
  try {
    const v = localStorage.getItem(KEY);
    return isLang(v) ? v : null;
  } catch {
    return null;
  }
}

/** Fills {placeholders} and picks the plural form for {n}. */
function format(text: Text, vars: Record<string, string | number> | undefined, plural: Intl.PluralRules): string {
  let s: string;
  if (typeof text === "string") s = text;
  else {
    const form = typeof vars?.n === "number" ? plural.select(vars.n) : "other";
    s = text[form] ?? text.other;
  }
  return vars ? s.replace(/\{(\w+)\}/g, (m, k) => (k in vars ? String(vars[k]) : m)) : s;
}

type I18n = {
  lang: Lang;
  locale: string;
  m: Messages;
  setLang: (l: Lang) => void;
  /** A text with values filled in. */
  t: (text: Text, vars?: Record<string, string | number>) => string;
  /** A text with React elements (e.g. a bold name) in its placeholders. */
  tx: (text: Text, vars: Record<string, ReactNode>) => ReactNode;
  timeAgo: (input: string | number | Date, now?: number) => string;
  duration: (fromIso: string | null | undefined, now?: number) => string;
  dateTime: (d: string | Date) => string;
};

const Ctx = createContext<I18n | null>(null);

export function useI18n() {
  const v = useContext(Ctx);
  if (!v) throw new Error("useI18n outside I18nProvider");
  return v;
}

/** Picks the visitor's language: their choice, their browser's, or the page default. */
export function I18nProvider({ children }: { children: ReactNode }) {
  const { data } = useQuery({ queryKey: ["status", ""], queryFn: () => fetchStatus(undefined), staleTime: 60_000 });
  const pageDefault = data?.page.language;
  const [chosen, setChosen] = useState<Lang | null>(storedLang);
  const lang: Lang = chosen ?? browserLang() ?? (isLang(pageDefault) ? pageDefault : "en");
  const entry = languages.find((l) => l.code === lang)!;

  const setLang = useCallback((l: Lang) => {
    try {
      localStorage.setItem(KEY, l);
    } catch {
      /* private mode: keep it for this visit */
    }
    setChosen(l);
  }, []);

  useEffect(() => {
    document.documentElement.lang = entry.locale;
  }, [entry.locale]);

  const value = useMemo<I18n>(() => {
    const m = entry.messages as Messages;
    const plural = new Intl.PluralRules(entry.locale);
    const rtf = new Intl.RelativeTimeFormat(entry.locale, { numeric: "auto", style: "short" });
    const dtf = new Intl.DateTimeFormat(entry.locale, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" });
    const unit = (u: "day" | "hour" | "minute" | "second", n: number) =>
      new Intl.NumberFormat(entry.locale, { style: "unit", unit: u, unitDisplay: "narrow" }).format(n);
    const t = (text: Text, vars?: Record<string, string | number>) => format(text, vars, plural);
    return {
      lang,
      locale: entry.locale,
      m,
      setLang,
      t,
      tx: (text, vars) => {
        const s = format(text, undefined, plural);
        return s.split(/(\{\w+\})/).map((part, i) => {
          const k = part.match(/^\{(\w+)\}$/)?.[1];
          return <Fragment key={i}>{k && k in vars ? vars[k] : part}</Fragment>;
        });
      },
      timeAgo: (input, now = Date.now()) => {
        const diff = Math.round((new Date(input).getTime() - now) / 1000);
        const abs = Math.abs(diff);
        if (abs < 5) return rtf.format(0, "second");
        if (abs < 60) return rtf.format(diff, "second");
        if (abs < 3600) return rtf.format(Math.round(diff / 60), "minute");
        if (abs < 86400) return rtf.format(Math.round(diff / 3600), "hour");
        return rtf.format(Math.round(diff / 86400), "day");
      },
      duration: (fromIso, now = Date.now()) => {
        if (!fromIso) return "—";
        let s = Math.max(0, Math.floor((now - new Date(fromIso).getTime()) / 1000));
        const d = Math.floor(s / 86400);
        s -= d * 86400;
        const h = Math.floor(s / 3600);
        s -= h * 3600;
        const min = Math.floor(s / 60);
        if (d > 0) return `${unit("day", d)} ${unit("hour", h)}`;
        if (h > 0) return `${unit("hour", h)} ${unit("minute", min)}`;
        if (min > 0) return unit("minute", min);
        return unit("second", s);
      },
      dateTime: (d) => dtf.format(new Date(d)),
    };
  }, [lang, entry, setLang]);

  // Texts for the shared components (banner, uptime bars, charts).
  const ui = useMemo<UiText>(() => {
    const m = value.m;
    return {
      status: m.status,
      overall: m.overall,
      live: m.live,
      reconnecting: m.reconnecting,
      uptimeHistory: (n) => value.t(m.uptimeHistory, { n }),
      noData: m.noData,
      uptime: m.uptime,
      failedChecks: m.failedChecks,
      slowChecks: m.slowChecks,
      noIncidents: m.noIncidents,
      more: (n) => value.t(m.more, { n }),
      daysAgo: (n) => value.t(m.daysAgo, { n }),
      today: m.today,
      noChecks: m.noChecks,
      average: m.average,
      max: m.max,
      failed: m.failed,
      locale: value.locale,
    };
  }, [value]);

  return (
    <Ctx.Provider value={value}>
      <UiTextProvider value={ui}>{children}</UiTextProvider>
    </Ctx.Provider>
  );
}
