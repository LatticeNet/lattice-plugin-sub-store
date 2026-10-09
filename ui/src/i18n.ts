/**
 * i18n.ts, the page in English, simplified Chinese and Russian.
 *
 * A typed message table in our own code rather than vue-i18n. The frame
 * refuses code built from strings: the console serves plugin documents with
 * `script-src 'self' <console origin>` and no 'unsafe-eval'
 * (lattice-server internal/server/server_plugin_assets.go, pluginAssetCSP).
 * vue-i18n before 9.3 compiled messages with eval and would not run there;
 * its JIT compiler (the default from v10) would, but it still parses every
 * message at run time, needs a custom rule for Russian plurals, and checks
 * keys but not the values a message takes. Here every message is a plain
 * string or a function of its values, so nothing is parsed at run time, and
 * a key missing from any locale, or a message taking different values, is a
 * type error (each table is declared as `Messages`, the English table's type).
 *
 * The locale is the host's: HostInit.locale, a BCP 47 tag, matched by its
 * language. Any language other than Chinese or Russian reads English, and so
 * does a page before the handshake. `t` reads the active table through a
 * proxy, so a template or a computed that reads a message re-renders when the
 * locale arrives.
 *
 * English ships in the main chunk, because the page reads it before the
 * handshake. The Chinese and Russian tables are chunks of their own, fetched
 * from the frame's origin when the host asks for them, so an operator loads
 * the one table their console speaks and not the other two. The page keeps
 * the table it has until the new one has arrived, then switches whole.
 *
 * Counts, sizes, dates, relative times and sorting go through Intl with the
 * active locale; the helpers below are the only place they are formatted.
 */
import { ref } from "vue";

import { en, type Messages } from "./messages/en";
import { numberFormat } from "./messages/format";

export type Locale = "en" | "zh-CN" | "ru";
export const LOCALES: readonly Locale[] = ["en", "zh-CN", "ru"];

const tables: Partial<Record<Locale, Messages>> = { en };
const loaders: Record<Exclude<Locale, "en">, () => Promise<Messages>> = {
  "zh-CN": () => import("./messages/zh-CN").then((module) => module.zhCN),
  ru: () => import("./messages/ru").then((module) => module.ru),
};

const active = ref<Locale>("en");
/** The locale the last setLocale asked for, so a slower earlier load cannot win. */
let wanted: Locale = "en";

/**
 * The supported locale for a host's tag: `zh` in any region or script reads
 * simplified Chinese (the only Chinese table), `ru` reads Russian, and
 * everything else, an empty tag included, reads English.
 */
export function matchLocale(tag: string | null | undefined): Locale {
  const language = (tag ?? "").trim().toLowerCase().split(/[-_]/)[0];
  if (language === "zh") return "zh-CN";
  if (language === "ru") return "ru";
  return "en";
}

/** The table of one locale, fetched the first time it is asked for. */
export async function messagesFor(locale: Locale): Promise<Messages> {
  const loaded = tables[locale];
  if (loaded) return loaded;
  const table = await loaders[locale as Exclude<Locale, "en">]();
  tables[locale] = table;
  return table;
}

/**
 * Adopt the host's locale once its table is in hand, and say it on <html
 * lang> for assistive technology and hyphenation. A table that fails to load
 * leaves the page in the locale it was already in; a later call supersedes an
 * earlier one still loading. Resolves to the locale the page now reads.
 */
export async function setLocale(tag: string | null | undefined): Promise<Locale> {
  const next = matchLocale(tag);
  wanted = next;
  try {
    await messagesFor(next);
  } catch {
    return active.value;
  }
  if (wanted !== next) return active.value;
  active.value = next;
  if (typeof document !== "undefined") document.documentElement.lang = next;
  return next;
}

export function currentLocale(): Locale {
  return active.value;
}

/** The active message table. Read it where the text is drawn, never into a constant. */
export const t: Messages = new Proxy({} as Messages, {
  get: (_target, key) => Reflect.get(tables[active.value] ?? en, key),
});

// ── formatting with the active locale ────────────────────────────────────────

/** A count as the locale writes it: 12,345 or 12 345. */
export function formatCount(value: number): string {
  return numberFormat(active.value).format(value);
}

/** A ratio as a whole percentage: 82%, or 82 % in Russian. */
export function formatPercent(ratio: number): string {
  return numberFormat(active.value, { style: "percent", maximumFractionDigits: 0 }).format(ratio);
}

/**
 * 1024-based, one decimal only when it adds information: 512 B, 1.5 GB, 2 TB.
 * The number is the locale's (1,5 in Russian) and so is the unit's name.
 */
export function formatBytes(bytes: number): string {
  const units = t.units.bytes;
  let value = bytes;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit += 1;
  }
  const digits = value >= 100 || Number.isInteger(value) ? 0 : 1;
  const rounded = numberFormat(active.value, { maximumFractionDigits: digits, minimumFractionDigits: digits, useGrouping: false }).format(
    digits ? value : Math.round(value),
  );
  return t.units.size(rounded, units[unit] ?? "");
}

const relativeFormats = new Map<string, Intl.RelativeTimeFormat>();

/**
 * English and Chinese read best narrow ("3h ago", "3小时前"); Russian narrow
 * drops the word for "ago" ("-3 ч"), so it takes the short style ("3 ч назад").
 */
function relativeFormat(): Intl.RelativeTimeFormat {
  const locale = active.value;
  let format = relativeFormats.get(locale);
  if (!format) {
    format = new Intl.RelativeTimeFormat(locale, { style: locale === "ru" ? "short" : "narrow", numeric: "always" });
    relativeFormats.set(locale, format);
  }
  return format;
}

const dateFormats = new Map<string, Intl.DateTimeFormat>();

function dateFormat(options: Intl.DateTimeFormatOptions): Intl.DateTimeFormat {
  const key = `${active.value}|${JSON.stringify(options)}`;
  let format = dateFormats.get(key);
  if (!format) {
    format = new Intl.DateTimeFormat(active.value, options);
    dateFormats.set(key, format);
  }
  return format;
}

/** A calendar date: Sep 30, 2026, 2026年9月30日, 30 сент. 2026 г. */
export function formatDate(at: number): string {
  return dateFormat({ dateStyle: "medium" }).format(new Date(at));
}

/** A date and a time to the second, for a title an operator matches against a log. */
export function formatDateTime(at: number): string {
  return dateFormat({ dateStyle: "medium", timeStyle: "medium" }).format(new Date(at));
}

/** A time of day to the second on a 24-hour clock: 12:12:16. */
export function formatTime(at: number): string {
  return dateFormat({ hour: "2-digit", minute: "2-digit", second: "2-digit", hourCycle: "h23" }).format(new Date(at));
}

/**
 * How long ago a timestamp was, or "" when it does not parse: "just now"
 * inside 45 seconds (a server clock slightly ahead of the browser's is
 * normal), then minutes, hours and days; past two weeks the date itself,
 * which is shorter to scan and exact.
 */
export function formatRelativeTime(iso: string, now: number = Date.now()): string {
  const then = Date.parse(iso);
  if (Number.isNaN(then)) return "";
  const seconds = Math.round((now - then) / 1000);
  if (seconds < 45) return t.time.justNow;
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) return relativeFormat().format(-minutes, "minute");
  const hours = Math.round(minutes / 60);
  if (hours < 24) return relativeFormat().format(-hours, "hour");
  const days = Math.round(hours / 24);
  if (days < 14) return relativeFormat().format(-days, "day");
  return formatDate(then);
}

const dayFormats = new Map<string, Intl.RelativeTimeFormat>();

/**
 * Whole days from now in words, for a sentence: "today", "tomorrow", "in 6
 * days", and once past "3 days ago" (never "yesterday", which an expiry
 * reads as vaguer than it is).
 */
export function formatDays(days: number): string {
  const numeric = days >= 0 ? "auto" : "always";
  const key = `${active.value}|${numeric}`;
  let format = dayFormats.get(key);
  if (!format) {
    format = new Intl.RelativeTimeFormat(active.value, { style: "long", numeric });
    dayFormats.set(key, format);
  }
  return format.format(days, "day");
}

/** A whole number of a unit, narrow: 43s, 2m, 3h, 2d in English; 43秒; 43 с. */
export function formatUnit(value: number, unit: "second" | "minute" | "hour" | "day"): string {
  return numberFormat(active.value, { style: "unit", unit, unitDisplay: "narrow" }).format(value);
}

const collators = new Map<string, Intl.Collator>();

/** Compare two names the way the active locale sorts them. */
export function compareText(a: string, b: string): number {
  let collator = collators.get(active.value);
  if (!collator) {
    collator = new Intl.Collator(active.value);
    collators.set(active.value, collator);
  }
  return collator.compare(a, b);
}

const listFormats = new Map<string, Intl.ListFormat>();

/**
 * "a, b and c" in the active locale's words. Chinese joins the last two with
 * 和 and no space, so where 和 meets Latin text or a digit ("1 个组合和 1 个
 * 文件") the space the table keeps between them is put back.
 */
export function formatList(items: readonly string[]): string {
  let format = listFormats.get(active.value);
  if (!format) {
    format = new Intl.ListFormat(active.value, { type: "conjunction" });
    listFormats.set(active.value, format);
  }
  if (active.value !== "zh-CN") return format.format(items);
  const parts = format.formatToParts(items);
  return parts
    .map((part, at) => {
      if (part.type !== "literal" || part.value !== "和") return part.value;
      const before = /[A-Za-z0-9]$/.test(parts[at - 1]?.value ?? "") ? " " : "";
      const after = /^[A-Za-z0-9]/.test(parts[at + 1]?.value ?? "") ? " " : "";
      return `${before}和${after}`;
    })
    .join("");
}
