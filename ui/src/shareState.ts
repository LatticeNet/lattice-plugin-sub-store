/**
 * shareState.ts, the two column verdicts the record list prints.
 *
 * Published: whether the host serves this record to anyone. The list used to
 * say, in a banner, that nothing is reachable until a share exists, and then
 * showed rows that said nothing about which ones had one. The host's share
 * list is the truth; this folds it onto a record.
 *
 * Refresh: what the last fetch did. Only a provider link is fetched; a pasted
 * list, this fleet's nodes and a converged path have no fetch to report, and
 * printing "Never refreshed" on them read as a fault on every row.
 */
import type { SubStoreShareRow, SubscriptionListItem } from "./client";
import { formatRelativeTime } from "./rowStatus";
import { maskUrlsIn } from "./urlMask";

export type Tone = "ok" | "warn" | "danger" | "neutral";

export interface PublishState {
  tone: Tone;
  /** Short cell text: the slug, or the one word that says why there is none. */
  label: string;
  title: string;
  /** Present when at least one share exists, so the cell can link to it. */
  slug?: string;
  shares: SubStoreShareRow[];
}

function expired(share: SubStoreShareRow, now: number): boolean {
  if (!share.expires_at) return false;
  const at = Date.parse(share.expires_at);
  return Number.isFinite(at) && at <= now;
}

export function publishStateFor(shares: readonly SubStoreShareRow[] | undefined, subscriptionId: string, now: number = Date.now()): PublishState {
  if (shares === undefined) {
    return { tone: "neutral", label: "—", title: "The share list has not been read yet.", shares: [] };
  }
  const mine = shares.filter((share) => share.subscription_id === subscriptionId);
  if (!mine.length) {
    return { tone: "neutral", label: "not published", title: "No share exists for this record, so no client can fetch it.", shares: [] };
  }
  const live = mine.filter((share) => share.enabled && !expired(share, now));
  const first = live[0] ?? mine[0];
  if (live.length) {
    const more = live.length > 1 ? ` and ${live.length - 1} more` : "";
    return { tone: "ok", label: `/${first.slug}`, title: `Served at ${first.path}${more}.`, slug: first.slug, shares: mine };
  }
  const why = mine.some((share) => expired(share, now)) ? "expired" : "disabled";
  return {
    tone: "warn",
    label: `/${first.slug} ${why}`,
    title: `A share exists but is ${why}; clients that fetch it get nothing.`,
    slug: first.slug,
    shares: mine,
  };
}

export interface ShareState {
  tone: Tone;
  label: "live" | "disabled" | "expired";
  title: string;
}

/** One share's own verdict, the way the Shares lens prints it. */
export function shareStateOf(share: SubStoreShareRow, now: number = Date.now()): ShareState {
  if (expired(share, now)) {
    return { tone: "warn", label: "expired", title: "Past its expiry: a client that fetches it gets nothing." };
  }
  if (!share.enabled) {
    return { tone: "warn", label: "disabled", title: "Switched off in the console: a client that fetches it gets nothing." };
  }
  return { tone: "ok", label: "live", title: `Served at ${share.path}.` };
}

export interface RefreshState {
  tone: Tone;
  label: string;
  title?: string;
}

/** True for the one source kind the plugin fetches on refresh. */
export function isFetched(item: SubscriptionListItem): boolean {
  return item.has_url;
}

export function refreshStateFor(item: SubscriptionListItem, now: number = Date.now()): RefreshState {
  if (!isFetched(item)) {
    return { tone: "neutral", label: "n/a", title: "Only a provider link is refreshed. This record's nodes are already in hand." };
  }
  if (item.last_fetch_ok === false) {
    // When it failed matters as much as that it failed: a row reading only
    // "Failed" cannot be told apart from one that broke three weeks ago, and
    // that is the row an operator is looking for.
    const when = item.last_fetch_at ? formatRelativeTime(item.last_fetch_at, now) : "";
    // The server trims the reason, and the reason quotes the link it fetched;
    // the title masks it after the host like every other read view.
    return { tone: "danger", label: when ? `Failed ${when}` : "Failed", title: maskUrlsIn(item.last_error || "The last refresh failed") };
  }
  if (!item.last_fetch_at) return { tone: "neutral", label: "Never refreshed" };
  const relative = formatRelativeTime(item.last_fetch_at, now);
  if (item.last_fetch_ok !== true) {
    // Fetched at some point, outcome not reported. Not a failure, and not a
    // success either: rendering it green was the only wrong option.
    return {
      tone: "neutral",
      label: relative ? `Fetched ${relative}, outcome not reported` : "Outcome not reported",
      title: "The server recorded a fetch for this record but not whether it succeeded.",
    };
  }
  return { tone: "ok", label: relative ? `Refreshed ${relative}` : "Refreshed" };
}
