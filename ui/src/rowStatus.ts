/**
 * rowStatus.ts. The inline status a subscription row shows after a refresh.
 *
 * Two small formatters, kept pure so the row template stays declarative:
 *  - formatRelativeTime turns the record's RFC3339 last_fetch_at into
 *    "refreshed 3h ago"-style copy;
 *  - parseUserinfo reads the provider's subscription-userinfo header
 *    ("upload=…; download=…; total=…; expire=…") for a runtime too old to
 *    parse it itself, by the runtime's own rules.
 */

/**
 * The tags a row shows: the first `limit`, a "+N" for the rest, and the whole
 * list for a title. Five badges after a long name were what pushed the name
 * out of its cell; a row is for telling records apart, and two tags do that.
 * The migration marker counts as a tag here because it is shown as one.
 */
export function tagChips(
  tags: readonly string[] | undefined,
  imported: boolean | undefined,
  limit = 2,
): { shown: string[]; more: number; all: string[] } {
  const all = [...(tags ?? []), ...(imported ? ["migrated"] : [])];
  const shown = all.slice(0, limit);
  return { shown, more: all.length - shown.length, all };
}

/** "3h ago"-style phrasing for a timestamp, or "" when it does not parse. */
export function formatRelativeTime(iso: string, now: number = Date.now()): string {
  const then = Date.parse(iso);
  if (Number.isNaN(then)) return "";
  const seconds = Math.round((now - then) / 1000);
  // A clock slightly ahead of the browser's is normal (the server stamps the
  // fetch); treating a small negative gap as "just now" beats "-3s ago".
  if (seconds < 45) return "just now";
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.round(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.round(hours / 24);
  if (days < 14) return `${days}d ago`;
  // Past two weeks the relative phrasing stops helping; the date is shorter to
  // scan and exactly precise.
  return new Date(then).toISOString().slice(0, 10);
}

export interface Userinfo {
  upload?: number;
  download?: number;
  total?: number;
  /** Seconds since epoch. */
  expire?: number;
}

/** The runtime's number grammar (system-go/subscription_userinfo.go), exactly. */
const USAGE_NUMBER = /^[+-]?(\d+\.?\d*|\.\d+)([eE][+-]?\d+)?$/;
/** Unix seconds do not reach 1e12 before the year 33658; a value this large is milliseconds. */
const EXPIRE_MILLIS_THRESHOLD = 1e12;
/** 2^63: byte counts past it do not exist in any quota, and the runtime refuses them. */
const MAX_INT64 = 2 ** 63;
const USERINFO_KEYS = new Set(["upload", "download", "total", "expire"]);

/**
 * The provider's header, parsed by the same rules the runtime applies
 * (parseProviderUsage). The UI only needs this for a runtime older than that
 * parse; the cases both sides must agree on live in
 * system-go/testdata/userinfo_cases.json.
 *
 * Keys in any case, `;` or `,` between pairs, spaces anywhere, values
 * optionally quoted and written as plain decimals (a fraction or an exponent
 * is truncated). A negative, non-decimal or past-int64 value drops that field
 * and a later valid value for the same key may still fill it; otherwise the
 * first valid value wins. An expire of zero means "never" and is left out, and
 * one in milliseconds is scaled to seconds. Null when nothing parsed.
 */
export function parseUserinfo(raw: string | undefined): Userinfo | null {
  if (!raw) return null;
  const out: Userinfo = {};
  let seen = false;
  for (const field of raw.split(/[;,]/)) {
    const eq = field.indexOf("=");
    if (eq < 0) continue;
    const key = field.slice(0, eq).trim().toLowerCase();
    if (!USERINFO_KEYS.has(key)) continue;
    const text = field.slice(eq + 1).trim().replace(/^["']+|["']+$/g, "");
    if (!USAGE_NUMBER.test(text)) continue;
    const value = Number(text);
    if (!Number.isFinite(value) || value < 0 || value >= MAX_INT64) continue;
    let number = Math.trunc(value) + 0;
    const slot = key as keyof Userinfo;
    if (slot === "expire") {
      if (number === 0) continue;
      if (number >= EXPIRE_MILLIS_THRESHOLD) number = Math.trunc(number / 1000);
    }
    if (out[slot] !== undefined) continue;
    out[slot] = number;
    seen = true;
  }
  return seen ? out : null;
}

/** 1024-based, one decimal only when it adds information: 512 B, 1.5 GB, 2 TB. */
export function formatBytes(bytes: number): string {
  const units = ["B", "KB", "MB", "GB", "TB"] as const;
  let value = bytes;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit += 1;
  }
  const rounded = value >= 100 || Number.isInteger(value) ? Math.round(value).toString() : value.toFixed(1);
  return `${rounded} ${units[unit]}`;
}

