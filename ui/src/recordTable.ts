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
 * The copy the table adds is collected in TEXT, so the locale lane can move it
 * into the message table in one pass.
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
import type { NodeCountState } from "./nodeCounts";
import {
  EXPIRY_WARN_DAYS,
  clientOfFile,
  daysUntilExpiry,
  formatExpiry,
  isProviderLink,
  plural,
  providerFigures,
  recordLabel,
  sourceKindLabel,
  usedBy,
  type Lineage,
  type ProviderFigures,
} from "./pipeline";
import { formatRelativeTime } from "./rowStatus";
import { publishStateFor, refreshStateFor, type PublishState, type RefreshState, type Tone } from "./shareState";

export const TEXT = {
  layer: "Records",
  kindLegend: "Kind",
  kindAll: "All",
  kindPlural: { source: "Sources", combination: "Combinations", file: "Files" },
  kindNoun: { source: "source", combination: "combination", file: "file" },
  density: "Compact rows",
  densityTitle: "Compact rows hide the icon and the remark, so more records fit on a screen",
  sortManual: "Manual order",
  orderColumn: "Order",
  publishedUnknown: "unknown",
  publishedUnknownTitle:
    "This session cannot read the share list (it needs substore:admin and proxy:admin), so whether a client can fetch this record is unknown.",
  publishedUnreadTitle: (error: string) => `The share list could not be read (${error}), so whether a client can fetch this record is unknown.`,
  fileCounts: "A file renders from its node source; the node counts are that record's.",
  notCounted: "Not counted yet. The next fetch records how many nodes came in.",
  outNotNative: "Counted after the chain only when the chain runs natively. Preview the record to see what it hands on.",
  countsFrom: (nodesIn: number, nodesOut: number | null, when: string) =>
    `${nodesIn} in${nodesOut === null ? "" : `, ${nodesOut} out after the chain`}, at the fetch ${when || "on record"}.`,
  notAProvider: "Only a provider link reports traffic and expiry. This record's nodes are already in hand.",
  notReported: "not reported",
  notReportedTitle: "The provider has not sent traffic figures, or the link has not been refreshed yet.",
  notFetched: "Only a provider link is refreshed. This record's nodes are already in hand.",
  flagged: "regex rewrite",
  flaggedTitle:
    "A pattern in this record's chain uses lookaround or a backreference, which the native engine cannot run. It keeps rendering on the fallback path; editing the step offers a rewrite.",
  steps: (count: number, off: number) => `${plural(count, "step")}${off ? `, ${off} turned off` : ""}.`,
  stepsOff: (off: number) => `${off} off`,
  stepsTarget: (target: string) => `Always rendered for ${target}.`,
  fileServedAsWritten: "served as written",
  fileFrom: (name: string) => `from ${name}`,
  fileFor: (client: string) => `for ${client}`,
  fileGone: "source gone",
  fileGoneTitle: (id: string) => `${id} is no longer in the store, so this file cannot render.`,
  usedByNothing: (noun: string) => `Nothing uses this ${noun}: no combination, file or share draws from it.`,
  membersTags: (tags: string) => `and every source tagged ${tags}`,
  missing: (count: number) => `${count} missing`,
  reorderReadOnly: "This session can read records but not change them, so the order is read-only.",
  reorderUnsigned: "The signed plugin does not offer reorder yet, so the order shown is the store's and cannot be changed here.",
  reorderLegacy: "This store still keeps every record in one document, in id order. Migrate it to arrange records by hand.",
  reorderSorted: "Sorted, so the rows are not in the store's order. Choose Manual order to rearrange them.",
  gripLabel: (name: string, position: number, total: number) => `Reorder ${name}, position ${position} of ${total}`,
  gripHelp: "Arrow Up and Arrow Down move the record; with the pointer, drag it. Escape puts a dragged row back.",
  moved: (name: string, position: number, total: number) => `Moved ${name} to position ${position} of ${total}.`,
  moveAtEdge: (name: string, edge: "top" | "bottom") => `${name} is already at the ${edge} of the rows shown.`,
  reorderFailed: (reason: string) => `The new order was not saved (${reason}). The table shows the stored order again.`,
  migrateTitle: "This store still keeps every record in one document",
  migrateBody:
    "Saves, deletes and reordering are refused until it is split into one document per record. Reading, previews and scheduled refreshes keep working. The legacy document is kept until the split verifies.",
  migrateAction: "Migrate store",
  migrateUnsigned: "The signed plugin does not offer migrate_store yet; it arrives with the S1 manifest.",
  migrateReadOnly: "An operator with substore:admin has to run the migration.",
  migrateProgress: (migrated: number, remaining: number) => `Migrated ${migrated} so far, ${remaining} to go.`,
  migrateDone: (count: number) => `The store is split: ${plural(count, "record")} moved, verified, and every write works again.`,
  migrateFailed: (reason: string) => `The migration stopped (${reason}). Records already moved stay moved; run it again to continue.`,
  readOnlyNote: "This session can read records but not create, change or reorder them.",
  noKind: (noun: string) => `No ${noun}s yet`,
} as const;

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
  if (item.kind === KIND_FILE) return EMPTY_COUNTS(TEXT.fileCounts);
  if (typeof item.nodes_in === "number") {
    const chainless = item.step_count - item.disabled_step_count <= 0;
    const out = typeof item.nodes_out === "number" ? item.nodes_out : chainless ? item.nodes_in : null;
    const when = item.last_fetch_at ? formatRelativeTime(item.last_fetch_at, input.now) : "";
    const title = TEXT.countsFrom(item.nodes_in, out, when) + (out === null ? ` ${TEXT.outNotNative}` : "");
    return {
      in: String(item.nodes_in),
      out: out === null ? "" : String(out),
      title,
      pair: out === null ? `${item.nodes_in} in` : `${item.nodes_in} → ${out}`,
    };
  }
  if (input.storeVersion === STORE_VERSION_SPLIT) return EMPTY_COUNTS(TEXT.notCounted);
  const state = input.preview;
  if (!input.canPreview) return EMPTY_COUNTS("This session cannot run a preview, so the node count is unknown.");
  if (!state || state.status === "queued" || state.status === "running") {
    return { in: "counting", out: "counting", title: "Counting: a preview is running for this record.", pair: "counting" };
  }
  if (state.status === "failed") {
    const when = formatRelativeTime(new Date(state.at).toISOString(), input.now) || "just now";
    return { in: "unknown", out: "unknown", title: `The preview run ${when} failed: ${state.reason}`, pair: "unknown" };
  }
  const when = formatRelativeTime(new Date(state.at).toISOString(), input.now) || "just now";
  return {
    in: String(state.source),
    out: String(state.result),
    title: `${state.source} in, ${state.result} out, from a preview run ${when}.`,
    pair: `${state.source} → ${state.result}`,
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
    return { tone: "neutral", label: TEXT.publishedUnknown, title: TEXT.publishedUnknownTitle, shares: [], unknown: true };
  }
  if (input.shares === undefined && input.error) {
    return { tone: "neutral", label: TEXT.publishedUnknown, title: TEXT.publishedUnreadTitle(input.error), shares: [], unknown: true };
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
  if (!isProviderLink(item)) return { state: "none", figures: null, text: "", title: TEXT.notAProvider, tone: "neutral" };
  const figures = providerFigures(item);
  if (!figures) return { state: "unreported", figures: null, text: TEXT.notReported, title: TEXT.notReportedTitle, tone: "neutral" };
  const days = daysUntilExpiry(figures, now);
  const text = formatExpiry(figures, now);
  if (days === null) return { state: "ok", figures, text, title: "The provider reported traffic and no expiry.", tone: "neutral" };
  if (days < 0) return { state: "expired", figures, text, title: `The provider says this subscription ${text}; it may serve nothing now.`, tone: "danger" };
  if (days <= EXPIRY_WARN_DAYS) return { state: "soon", figures, text, title: `The provider says this subscription ${text}.`, tone: "warn" };
  return { state: "ok", figures, text, title: `The provider says this subscription ${text}.`, tone: "neutral" };
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
  if (fileType === FILE_TYPE_SCRIPT) return "Script file";
  if (fileType === FILE_TYPE_PLAIN) return "Plain text file";
  return "Configuration file";
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
    const forClient = client ? TEXT.fileFor(client.label) : "";
    const clientTitle = client ? ` The name says it is for ${client.label}.` : "";
    const source = (item.node_source ?? "").trim();
    if (!source) {
      return {
        label,
        detail: [TEXT.fileServedAsWritten, forClient].filter(Boolean).join(" · "),
        title: `Nothing fills it: the document is served as written.${clientTitle}`,
        missing: 0,
        missingLabel: "",
      };
    }
    if (!items.some((entry) => entry.id === source)) {
      return { label, detail: forClient, title: TEXT.fileGoneTitle(source) + clientTitle, missing: 1, missingLabel: TEXT.fileGone };
    }
    return {
      label,
      detail: [TEXT.fileFrom(name(source)), forClient].filter(Boolean).join(" · "),
      title: `Its proxy list is filled from ${name(source)}.${clientTitle}`,
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
      ...missing.map((ref) => `${ref.ref} (${ref.reason})`),
      ...(item.member_tags?.length ? [TEXT.membersTags(item.member_tags.join(", "))] : []),
    ].join(", ");
    return { label: "Combination", detail, title: title || "No members.", missing: missing.length, missingLabel: missing.length ? TEXT.missing(missing.length) : "" };
  }
  const used = usedBy(lineage, item.id);
  const parts: string[] = [];
  if (used.combinations.length) parts.push(plural(used.combinations.length, "combination"));
  if (used.files.length) parts.push(plural(used.files.length, "file"));
  const users = [...used.combinations, ...used.files].map(name);
  return {
    label: sourceKindLabel(item),
    detail: parts.length ? `feeds ${parts.join(", ")}` : "",
    title: users.length ? `Feeds ${users.join(", ")}` : TEXT.usedByNothing("source"),
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
    count: String(item.step_count),
    off: off ? TEXT.stepsOff(off) : "",
    title: [TEXT.steps(item.step_count, off), item.target ? TEXT.stepsTarget(item.target) : ""].filter(Boolean).join(" "),
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
  if (!input.canMutate) return TEXT.reorderReadOnly;
  if (!input.available || input.storeVersion === undefined) return TEXT.reorderUnsigned;
  if (input.storeVersion === STORE_VERSION_LEGACY) return TEXT.reorderLegacy;
  if (input.sort !== "manual") return TEXT.reorderSorted;
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
