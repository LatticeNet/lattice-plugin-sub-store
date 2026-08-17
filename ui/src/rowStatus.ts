/**
 * rowStatus.ts — the inline status a subscription row shows after a refresh.
 *
 * Two small formatters, kept pure so the row template stays declarative:
 *  - formatRelativeTime turns the record's RFC3339 last_fetch_at into
 *    "refreshed 3h ago"-style copy;
 *  - parseUserinfo + formatTraffic turn the provider's subscription-userinfo
 *    header ("upload=…; download=…; total=…; expire=…") into a compact quota
 *    summary, guarding providers that send junk.
 */

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
  /** Seconds since epoch, as the header carries it. */
  expire?: number;
}

/**
 * The header is a query-string-shaped list: "upload=1; download=2; total=3".
 * Providers are inconsistent about spacing and key order, and some omit keys —
 * anything that does not parse as a non-negative number is dropped rather than
 * formatted as NaN.
 */
export function parseUserinfo(raw: string | undefined): Userinfo | null {
  if (!raw) return null;
  const out: Userinfo = {};
  let seen = false;
  for (const pair of raw.split(";")) {
    const eq = pair.indexOf("=");
    if (eq <= 0) continue;
    const key = pair.slice(0, eq).trim().toLowerCase();
    const value = Number(pair.slice(eq + 1).trim());
    if (!Number.isFinite(value) || value < 0) continue;
    if (key === "upload" || key === "download" || key === "total" || key === "expire") {
      out[key] = value;
      seen = true;
    }
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

/**
 * "used / total · until 2026-09-01" for the row, or "" when there is nothing
 * honest to say. Used is upload + download — what the subscriber has consumed
 * of the provider's total.
 */
export function formatTraffic(info: Userinfo | null): string {
  if (!info) return "";
  const used = (info.upload ?? 0) + (info.download ?? 0);
  const parts: string[] = [];
  if (info.total !== undefined && info.total > 0) {
    parts.push(`${formatBytes(used)} / ${formatBytes(info.total)}`);
  } else if (used > 0) {
    parts.push(`${formatBytes(used)} used`);
  }
  if (info.expire !== undefined && info.expire > 0) {
    // The header carries seconds; a millisecond reading would render a date in
    // the year 57000.
    parts.push(`until ${new Date(info.expire * 1000).toISOString().slice(0, 10)}`);
  }
  return parts.join(" · ");
}
