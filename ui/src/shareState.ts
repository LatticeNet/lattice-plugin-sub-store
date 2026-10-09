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
import { t } from "./i18n";
import { formatRelativeTime } from "./rowStatus";
import { refreshFailureText } from "./urlMask";

export type Tone = "ok" | "warn" | "danger" | "neutral";

/** The chassis's name for a tone, for the state dot and pill it draws. */
export function stateTone(tone: Tone): "healthy" | "warning" | "error" | "neutral" {
  if (tone === "ok") return "healthy";
  if (tone === "warn") return "warning";
  if (tone === "danger") return "error";
  return "neutral";
}

/**
 * The link a client fetches for a share: the absolute URL when the console
 * sent one, the path otherwise. Every copy-link control reads it here, so the
 * table, the Shares layer, the side panel and the record page copy the same
 * string.
 */
export function shareLinkOf(share: SubStoreShareRow): string {
  return share.url || share.path || "";
}

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
    return { tone: "neutral", label: t.publish.unread, title: t.publish.unreadTitle, shares: [] };
  }
  const mine = shares.filter((share) => share.subscription_id === subscriptionId);
  if (!mine.length) {
    return { tone: "neutral", label: t.publish.none, title: t.publish.noneTitle, shares: [] };
  }
  const live = mine.filter((share) => share.enabled && !expired(share, now));
  const first = live[0] ?? mine[0];
  if (live.length) {
    // By slug: the path carries the share's token, and a hover title is no place for it.
    return { tone: "ok", label: `/${first.slug}`, title: t.publish.servedAt(`/${first.slug}`, live.length - 1), slug: first.slug, shares: mine };
  }
  const isExpired = mine.some((share) => expired(share, now));
  return {
    tone: "warn",
    label: isExpired ? t.publish.expiredLabel(`/${first.slug}`) : t.publish.disabledLabel(`/${first.slug}`),
    title: isExpired ? t.publish.expiredTitle : t.publish.disabledTitle,
    slug: first.slug,
    shares: mine,
  };
}

/** What a share does for a client that fetches it, worst first; `orphan` serves nothing. */
export type ShareStateId = "live" | "disabled" | "expired" | "orphan";

export interface ShareState {
  tone: Tone;
  state: ShareStateId;
  /** The state in the active locale's words. */
  label: string;
  title: string;
}

/**
 * One share's own verdict, the way the Shares lens prints it.
 *
 * `recordKnown` is false when the record catalogue has been read and the
 * share's record is not in it. Such a share is still enabled in the console,
 * and it used to read "live" in green and count towards "1 share live" right
 * after its file was deleted, while a client fetching it got nothing. It is
 * the worst state a share can be in, so it is checked first.
 */
export function shareStateOf(share: SubStoreShareRow, now: number = Date.now(), recordKnown = true): ShareState {
  if (!recordKnown) {
    return { tone: "danger", state: "orphan", label: t.shareState.orphan, title: t.shareState.orphanTitle };
  }
  if (expired(share, now)) {
    return { tone: "warn", state: "expired", label: t.shareState.expired, title: t.shareState.expiredTitle };
  }
  if (!share.enabled) {
    return { tone: "warn", state: "disabled", label: t.shareState.disabled, title: t.shareState.disabledTitle };
  }
  return { tone: "ok", state: "live", label: t.shareState.live, title: t.publish.servedAt(`/${share.slug}`, 0) };
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
    return { tone: "neutral", label: t.refresh.notApplicable, title: t.refresh.notFetched };
  }
  if (item.last_fetch_ok === false) {
    // When it failed matters as much as that it failed: a row reading only
    // "Failed" cannot be told apart from one that broke three weeks ago, and
    // that is the row an operator is looking for.
    const when = item.last_fetch_at ? formatRelativeTime(item.last_fetch_at, now) : "";
    // The server trims the reason, and the reason quotes the link it fetched;
    // the title keeps only the link's host, as the attention list does.
    return { tone: "danger", label: when ? t.refresh.failedAt(when) : t.refresh.failed, title: refreshFailureText(item.last_error) || t.refresh.failedTitle };
  }
  if (!item.last_fetch_at) return { tone: "neutral", label: t.refresh.never };
  const relative = formatRelativeTime(item.last_fetch_at, now);
  if (item.last_fetch_ok !== true) {
    // Fetched at some point, outcome not reported. Not a failure, and not a
    // success either: rendering it green was the only wrong option.
    return {
      tone: "neutral",
      label: relative ? t.refresh.unreportedAt(relative) : t.refresh.unreported,
      title: t.refresh.unreportedTitle,
    };
  }
  return { tone: "ok", label: relative ? t.refresh.refreshedAt(relative) : t.refresh.refreshed };
}
