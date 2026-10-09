/**
 * recordTable.ts, what each cell of the Records table says, decided without a
 * DOM.
 *
 * One table lists sources, combinations and files (design 28, "UI and UX":
 * Records, one table with a kind filter; columns published state, nodes in,
 * nodes out, steps, expiry, traffic, last fetch). Three tables used to say
 * these things three ways. Every cell rule lives here once, with its reason
 * when a cell is empty, so the screen only draws and the rules are tested.
 *
 * The copy lives in the message table (`t.records`, `t.counts`, `t.kinds`),
 * read when a cell is decided, so a row follows the host's locale.
 */
import {
  FILE_TYPE_PLAIN,
  FILE_TYPE_SCRIPT,
  KIND_COLLECTION,
  KIND_FILE,
  KIND_SUB,
  STORE_VERSION_LEGACY,
  STORE_VERSION_SPLIT,
  type SubStoreShareRow,
  type SubscriptionListItem,
} from "./client";
import { formatCount, formatList, t } from "./i18n";
import type { NodeCountState } from "./nodeCounts";
import {
  EXPIRY_WARN_DAYS,
  clientOfFile,
  daysUntilExpiry,
  formatExpiry,
  isProviderLink,
  providerFigures,
  recordLabel,
  sourceKindLabel,
  usedBy,
  type Lineage,
  type ProviderFigures,
} from "./pipeline";
import { formatRelativeTime } from "./rowStatus";
import { publishStateFor, refreshStateFor, type PublishState, type RefreshState, type Tone } from "./shareState";


/** The kind filter's values, as the address carries them. */
export type KindFacet = "source" | "combination" | "file";
export const KIND_FACETS: readonly KindFacet[] = ["source", "combination", "file"];

export function kindFacetOf(kind: string | undefined): KindFacet {
  if (kind === KIND_COLLECTION) return "combination";
  if (kind === KIND_FILE) return "file";
  return "source";
}

/** The stored kind a facet stands for. */
export function kindOfFacet(facet: KindFacet): string {
  if (facet === "combination") return KIND_COLLECTION;
  if (facet === "file") return KIND_FILE;
  return KIND_SUB;
}

/** True when the record is the facet's kind; every record when the facet is unset. */
export function matchesKind(item: Pick<SubscriptionListItem, "kind">, facet: string): boolean {
  if (!(KIND_FACETS as readonly string[]).includes(facet)) return true;
  return kindFacetOf(item.kind) === facet;
}

export function kindCounts(items: readonly Pick<SubscriptionListItem, "kind">[]): Record<KindFacet | "all", number> {
  const out = { all: items.length, source: 0, combination: 0, file: 0 };
  for (const item of items) out[kindFacetOf(item.kind)] += 1;
  return out;
}

export function isFlagged(item: Pick<SubscriptionListItem, "flags">): boolean {
  return item.flags?.regex_incompatible === true;
}

// ── nodes in, nodes out ───────────────────────────────────────────────────────

export interface CountCells {
  in: string;
  out: string;
  title: string;
  /** For the stacked row, which prints one "in → out" item. */
  pair: string;
}

const EMPTY_COUNTS = (title: string): CountCells => ({ in: "", out: "", title, pair: "" });

export interface CountInput {
  storeVersion: number | undefined;
  /** This session's preview count, the fallback on a store without an index. */
  preview: NodeCountState | undefined;
  canPreview: boolean;
  now: number;
}

/**
 * The index's counts when the store has them: what the last fetch read, and
 * what the native chain handed on. A record with no enabled step hands on
 * everything it read. A store without the index (a runtime before S1, or a
 * legacy store not yet migrated) has no recorded counts, so the session's
 * preview count stands in, as it did before the split.
 */
export function nodeCountsOf(item: SubscriptionListItem, input: CountInput): CountCells {
  if (item.kind === KIND_FILE) return EMPTY_COUNTS(t.records.fileCounts);
  if (typeof item.nodes_in === "number") {
    const chainless = item.step_count - item.disabled_step_count <= 0;
    const out = typeof item.nodes_out === "number" ? item.nodes_out : chainless ? item.nodes_in : null;
    const when = item.last_fetch_at ? formatRelativeTime(item.last_fetch_at, input.now) : "";
    const nodesIn = formatCount(item.nodes_in);
    const nodesOut = out === null ? null : formatCount(out);
    const from = t.records.countsFrom(nodesIn, nodesOut, when);
    return {
      in: nodesIn,
      out: nodesOut ?? "",
      title: nodesOut === null ? t.common.joinSentences([from, t.records.outNotNative]) : from,
      pair: nodesOut === null ? t.counts.inOnly(nodesIn) : t.counts.pair(nodesIn, nodesOut),
    };
  }
  if (input.storeVersion === STORE_VERSION_SPLIT) return EMPTY_COUNTS(t.records.notCounted);
  const state = input.preview;
  if (!input.canPreview) return EMPTY_COUNTS(t.counts.cannotPreview);
  if (!state || state.status === "queued" || state.status === "running") {
    return { in: t.counts.counting, out: t.counts.counting, title: t.counts.running, pair: t.counts.counting };
  }
  if (state.status === "failed") {
    const when = formatRelativeTime(new Date(state.at).toISOString(), input.now) || t.time.justNow;
    return { in: t.counts.unknown, out: t.counts.unknown, title: t.counts.previewRunFailed(when, state.reason), pair: t.counts.unknown };
  }
  const when = formatRelativeTime(new Date(state.at).toISOString(), input.now) || t.time.justNow;
  const source = formatCount(state.source);
  const result = formatCount(state.result);
  return {
    in: source,
    out: result,
    title: t.counts.fromPreview(source, result, when),
    pair: t.counts.pair(source, result),
  };
}

/** Whether this row's counts come from the session's previews rather than the index. */
export function countsNeedPreview(item: SubscriptionListItem, storeVersion: number | undefined): boolean {
  return item.kind !== KIND_FILE && typeof item.nodes_in !== "number" && storeVersion !== STORE_VERSION_SPLIT;
}

// ── published ────────────────────────────────────────────────────────────────

export interface PublishedInput {
  shares: readonly SubStoreShareRow[] | undefined;
  /** Whether this session may read the share list at all. */
  available: boolean;
  error: string;
  now: number;
}

/**
 * The share list's verdict on a record, or "unknown" when the list cannot be
 * known: a session without the scopes `shares.list` needs, or a list read
 * that failed. Unknown is not "not published", which would be a claim.
 */
export function publishedOf(item: Pick<SubscriptionListItem, "id">, input: PublishedInput): PublishState & { unknown: boolean } {
  if (!input.available) {
    return { tone: "neutral", label: t.records.publishedUnknown, title: t.records.publishedUnknownTitle, shares: [], unknown: true };
  }
  if (input.shares === undefined && input.error) {
    return { tone: "neutral", label: t.records.publishedUnknown, title: t.records.publishedUnreadTitle(input.error), shares: [], unknown: true };
  }
  return { ...publishStateFor(input.shares, item.id, input.now), unknown: false };
}

// ── expiry and traffic ───────────────────────────────────────────────────────

export type ExpiryState = "none" | "unreported" | "ok" | "soon" | "expired";

export interface ExpiryCell {
  state: ExpiryState;
  figures: ProviderFigures | null;
  /** "expires in 6 days", "expired 2 days ago", or "" without an expiry. */
  text: string;
  title: string;
  tone: Tone;
}

export function expiryOf(item: SubscriptionListItem, now: number): ExpiryCell {
  if (!isProviderLink(item)) return { state: "none", figures: null, text: "", title: t.records.notAProvider, tone: "neutral" };
  const figures = providerFigures(item);
  if (!figures) return { state: "unreported", figures: null, text: t.records.notReported, title: t.records.notReportedTitle, tone: "neutral" };
  const days = daysUntilExpiry(figures, now);
  const text = formatExpiry(figures, now);
  if (days === null) return { state: "ok", figures, text, title: t.provider.noExpiryTitle, tone: "neutral" };
  if (days < 0) return { state: "expired", figures, text, title: t.provider.expiredTitle(text), tone: "danger" };
  if (days <= EXPIRY_WARN_DAYS) return { state: "soon", figures, text, title: t.provider.saysTitle(text), tone: "warn" };
  return { state: "ok", figures, text, title: t.provider.saysTitle(text), tone: "neutral" };
}

// ── last fetch ───────────────────────────────────────────────────────────────

/**
 * What the last fetch did, for any record the store has fetch bookkeeping
 * for: a provider link always, and since the split any record core fetched on
 * a schedule. Null, with NOT_FETCHED as the reason, for one that is never
 * fetched.
 */
export function lastFetchOf(item: SubscriptionListItem, now: number): RefreshState | null {
  if (item.has_url) return refreshStateFor(item, now);
  if (item.last_fetch_at) return refreshStateFor({ ...item, has_url: true }, now);
  return null;
}

// ── kind ─────────────────────────────────────────────────────────────────────

export interface KindCell {
  /** What the record is, specifically: "Provider link", "Combination", "Script file". */
  label: string;
  /**
   * What it is connected to: its members, its node source, what uses it. A
   * file names its source before its client, because the name in the row
   * usually says the client already and a narrow cell cuts the end.
   */
  detail: string;
  title: string;
  /** References that answer nothing, drawn as an error dot in either density. */
  missing: number;
  missingLabel: string;
}

export function fileTypeLabel(fileType: string | undefined): string {
  if (fileType === FILE_TYPE_SCRIPT) return t.kinds.scriptFile;
  if (fileType === FILE_TYPE_PLAIN) return t.kinds.plainFile;
  return t.kinds.configFile;
}

export function kindOf(item: SubscriptionListItem, items: readonly SubscriptionListItem[], lineage: Lineage): KindCell {
  const name = (id: string) => {
    const found = items.find((entry) => entry.id === id);
    return found ? recordLabel(found) : id;
  };
  if (item.kind === KIND_FILE) {
    const label = fileTypeLabel(item.file_type);
    // The record stores no target, so the name is where a file says its client.
    const client = clientOfFile(item.name);
    const forClient = client ? t.records.fileFor(client.label) : "";
    const titled = (sentence: string) => t.common.joinSentences([sentence, client ? t.records.fileNamedFor(client.label) : ""].filter(Boolean));
    const source = (item.node_source ?? "").trim();
    if (!source) {
      return {
        label,
        detail: [t.records.fileServedAsWritten, forClient].filter(Boolean).join(" · "),
        title: titled(t.records.fileServedTitle),
        missing: 0,
        missingLabel: "",
      };
    }
    if (!items.some((entry) => entry.id === source)) {
      return { label, detail: forClient, title: titled(t.records.fileGoneTitle(source)), missing: 1, missingLabel: t.records.fileGone };
    }
    return {
      label,
      detail: [t.records.fileFrom(name(source)), forClient].filter(Boolean).join(" · "),
      title: titled(t.records.fileFilledFrom(name(source))),
      missing: 0,
      missingLabel: "",
    };
  }
  if (item.kind === KIND_COLLECTION) {
    const members = (lineage.upstream.get(item.id) ?? []).map(name);
    const missing = lineage.broken.filter((ref) => ref.owner === item.id);
    const detail = members.slice(0, 2).join(", ") + (members.length > 2 ? ` +${members.length - 2}` : "");
    const title = [
      ...members,
      ...missing.map((ref) => t.records.memberMissing(ref.ref, ref.reason)),
      ...(item.member_tags?.length ? [t.records.membersTags(item.member_tags.join(", "))] : []),
    ].join(", ");
    return {
      label: t.kinds.combination,
      detail,
      title: title || t.records.noMembers,
      missing: missing.length,
      missingLabel: missing.length ? t.records.missing(missing.length) : "",
    };
  }
  const used = usedBy(lineage, item.id);
  const parts: string[] = [];
  if (used.combinations.length) parts.push(t.nouns.combinations(used.combinations.length));
  if (used.files.length) parts.push(t.nouns.files(used.files.length));
  const users = [...used.combinations, ...used.files].map(name);
  return {
    label: sourceKindLabel(item),
    detail: parts.length ? t.lineage.feeds(formatList(parts)) : "",
    title: users.length ? t.lineage.feedsTitle(users.join(", ")) : t.records.usedByNothing,
    missing: 0,
    missingLabel: "",
  };
}

// ── steps ────────────────────────────────────────────────────────────────────

export interface StepsCell {
  count: string;
  /** "1 off" under the count, or "" when every step runs. */
  off: string;
  title: string;
}

/**
 * The chain's length, and how many of its steps are turned off on a line of
 * their own, so the column stays narrow; the title says both in words.
 */
export function stepsOf(item: Pick<SubscriptionListItem, "step_count" | "disabled_step_count" | "target">): StepsCell {
  const off = item.disabled_step_count || 0;
  return {
    count: formatCount(item.step_count),
    off: off ? t.records.stepsOff(off) : "",
    title: t.common.joinSentences([t.records.steps(item.step_count, off), item.target ? t.records.stepsTarget(item.target) : ""].filter(Boolean)),
  };
}

// ── order and attention ──────────────────────────────────────────────────────

export interface ReorderInput {
  canMutate: boolean;
  /** Whether the host declares `reorder` for this frame. */
  available: boolean;
  storeVersion: number | undefined;
  sort: string;
}

/** Why the order cannot be changed here, or "" when it can. */
export function reorderBlock(input: ReorderInput): string {
  if (!input.canMutate) return t.records.reorderReadOnly;
  if (!input.available || input.storeVersion === undefined) return t.records.reorderUnsigned;
  if (input.storeVersion === STORE_VERSION_LEGACY) return t.records.reorderLegacy;
  if (input.sort !== "manual") return t.records.reorderSorted;
  return "";
}

/** Rank for the Needs attention sort: failures and expiry first, then flags and soon-to-expire. */
export function attentionWeight(item: SubscriptionListItem, now: number): number {
  const fetch = lastFetchOf(item, now);
  const expiry = expiryOf(item, now);
  if (fetch?.tone === "danger" || expiry.state === "expired") return 0;
  if (fetch?.tone === "warn" || expiry.state === "soon" || isFlagged(item)) return 1;
  if (fetch?.tone === "neutral") return 2;
  return 3;
}
