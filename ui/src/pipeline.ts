/**
 * pipeline.ts, the store read as one chain: sources feed combinations, both
 * feed files, and shares publish any of them.
 *
 * Every layer of the page reads this one model. The overview draws it as a
 * map, the collection tables print its "used by" and "renders" columns, the
 * side panel and the record page print a record's lineage, and the attention
 * list is a set of questions asked of it. It is built from the list rows and
 * the host's share list only, so none of that costs a `get` per record.
 *
 * Pure and DOM-free, so it runs under plain vitest.
 */
import {
  CONVERT_TARGETS,
  KIND_COLLECTION,
  KIND_FILE,
  KIND_SUB,
  SOURCE_LOCAL,
  SOURCE_REMOTE,
  SOURCE_VPN_CORE,
  SOURCE_VPN_CORE_GRAPH,
  type SubStoreShareRow,
  type SubscriptionListItem,
} from "./client";
import { compareText, formatDate, formatDays, formatList, formatPercent, t } from "./i18n";
import { formatBytes, formatRelativeTime, parseUserinfo } from "./rowStatus";
import { maskUrlsIn, refreshFailureText } from "./urlMask";

export type Stage = "source" | "combination" | "file" | "share";
export const STAGES: readonly Stage[] = ["source", "combination", "file", "share"];

/**
 * The layers of the page, in tab order. The id is what `?view=` carries.
 * Records lists every kind (design 28, S1); the three per-kind layers it
 * replaced still land through pageState's legacy views.
 */
export type ViewId = "overview" | "records" | "shares" | "settings";
export const VIEW_IDS: readonly ViewId[] = ["overview", "records", "shares", "settings"];

export function stageOfKind(kind: string | undefined): Exclude<Stage, "share"> {
  if (kind === KIND_COLLECTION) return "combination";
  if (kind === KIND_FILE) return "file";
  return "source";
}

export function recordLabel(item: Pick<SubscriptionListItem, "name" | "display_name">): string {
  return item.display_name || item.name;
}

// ── provider figures ─────────────────────────────────────────────────────────

export interface ProviderFigures {
  upload?: number;
  download?: number;
  total?: number;
  /** Unix seconds. */
  expire?: number;
}

/**
 * What the provider says about this subscription, or null when it said
 * nothing.
 *
 * A runtime that parses the header marks the row (`userinfo_parsed`), and its
 * figures are then the whole answer: a field it left out was refused, and
 * parsing the header again here would bring that field back. Only a row with
 * neither the marker nor a parsed field, from a runtime older than the parse,
 * is read here, by the same rules (parseUserinfo).
 */
export function providerFigures(item: SubscriptionListItem): ProviderFigures | null {
  const parsed: ProviderFigures = {};
  let seen = false;
  for (const key of ["upload", "download", "total", "expire"] as const) {
    const value = item[key];
    if (typeof value === "number" && Number.isFinite(value) && value >= 0) {
      parsed[key] = value;
      seen = true;
    }
  }
  if (seen) return parsed;
  if (item.userinfo_parsed) return null;
  return parseUserinfo(item.userinfo);
}

/** Upload plus download: what the subscriber has consumed. */
export function usedBytes(figures: ProviderFigures): number {
  return (figures.upload ?? 0) + (figures.download ?? 0);
}

/** Used over total, or null when there is no total to measure against. */
export function usageRatio(figures: ProviderFigures | null): number | null {
  if (!figures || figures.total === undefined || figures.total <= 0) return null;
  return usedBytes(figures) / figures.total;
}

const DAY_MS = 86_400_000;

/** Whole days from now to the expiry, negative once past; null without one. */
export function daysUntilExpiry(figures: ProviderFigures | null, now: number): number | null {
  if (!figures || figures.expire === undefined || figures.expire <= 0) return null;
  const diff = figures.expire * 1000 - now;
  // Whole days either side, counted away from now: two days and an hour ago
  // is two days ago, and anything already past is at least one day below zero
  // so a comparison with zero never mistakes it for still running.
  if (diff >= 0) return Math.floor(diff / DAY_MS);
  return -Math.max(1, Math.floor(-diff / DAY_MS));
}

/** "410 GB of 500 GB · 82%", "3 GB used", or "" when nothing was reported. */
export function formatUsage(figures: ProviderFigures | null): string {
  if (!figures) return "";
  const used = usedBytes(figures);
  const ratio = usageRatio(figures);
  if (ratio !== null) return t.provider.usage(formatBytes(used), formatBytes(figures.total!), formatPercent(Math.round(ratio * 100) / 100));
  if (figures.upload !== undefined || figures.download !== undefined) return t.provider.usedOnly(formatBytes(used));
  return "";
}

/**
 * "expires in 6 days", "expires today", "expired 3 days ago", or "". Past
 * sixty days the date itself, in the active locale.
 */
export function formatExpiry(figures: ProviderFigures | null, now: number): string {
  const days = daysUntilExpiry(figures, now);
  if (days === null) return "";
  if (days < 0) {
    if (now - figures!.expire! * 1000 < DAY_MS) return t.provider.expiredToday;
    return t.provider.expiredAgo(formatDays(days));
  }
  if (days <= 60) return t.provider.expiresIn(formatDays(days));
  return t.provider.expiresOn(formatDate(figures!.expire! * 1000));
}

/** The attention thresholds the design names. */
export const EXPIRY_WARN_DAYS = 14;
export const USAGE_WARN_RATIO = 0.8;

// ── what a record is ─────────────────────────────────────────────────────────

/** Where a source's nodes come from, in the operator's words. */
export function sourceKindLabel(item: SubscriptionListItem): string {
  if (item.source === SOURCE_VPN_CORE) return t.kinds.fleet;
  if (item.source === SOURCE_VPN_CORE_GRAPH) return t.kinds.relay;
  if (item.source === SOURCE_REMOTE) return t.kinds.provider;
  if (item.source === SOURCE_LOCAL) return t.kinds.pasted;
  return item.has_url ? t.kinds.provider : t.kinds.pasted;
}

/** True for the one source kind that is fetched and reports provider figures. */
export function isProviderLink(item: SubscriptionListItem): boolean {
  if ((item.kind || KIND_SUB) !== KIND_SUB) return false;
  if (item.source === SOURCE_REMOTE) return true;
  return !item.source && item.has_url;
}

/**
 * The client a file is written for, read from its name. Files are named for a
 * person and a client (`for-openjobs-shenzhen-loon`), and the record itself
 * stores no target, so the name is the only place it is said.
 */
const CLIENT_PATTERNS: ReadonlyArray<{ id: string; pattern: RegExp }> = [
  { id: "sing-box", pattern: /sing-?box/ },
  { id: "Shadowrocket", pattern: /shadowrocket/ },
  { id: "QX", pattern: /quantumult(?:-?x)?|quanx|qx/ },
  { id: "SurgeMac", pattern: /surge-?mac/ },
  { id: "Surge", pattern: /surge/ },
  { id: "Surfboard", pattern: /surfboard/ },
  { id: "ClashMeta", pattern: /mihomo|clash-?meta/ },
  { id: "Clash", pattern: /clash/ },
  { id: "Stash", pattern: /stash/ },
  { id: "Loon", pattern: /loon/ },
  { id: "Egern", pattern: /egern/ },
  { id: "V2Ray", pattern: /v2ray/ },
];

export function clientOfFile(name: string): { id: string; label: string } | null {
  const lower = name.toLowerCase();
  for (const { id, pattern } of CLIENT_PATTERNS) {
    // Bounded by a separator or an end on both sides, so "stashed-rules" is
    // not a Stash file and "gloon" is not a Loon one.
    const bounded = new RegExp(`(?:^|[-_.\\s])(?:${pattern.source})(?:$|[-_.\\s])`);
    if (bounded.test(lower)) {
      const target = CONVERT_TARGETS.find((entry) => entry.id === id);
      return { id, label: target?.label ?? id };
    }
  }
  return null;
}

// ── prefix grouping ──────────────────────────────────────────────────────────

export type PrefixEntry<T> =
  | { kind: "group"; prefix: string; label: string; members: T[] }
  | { kind: "single"; member: T };

const SEPARATOR = /[-_]/;

/**
 * Names that share a prefix, folded into one entry when at least `min` do.
 *
 * Files are named for a person and then a client (`for-openjobs-loon`,
 * `for-openjobs-stash`), so the prefix before the client is the person, and
 * sixteen files read as a handful of people. Each name joins the longest
 * separator-bounded prefix that at least `min` names share; a prefix left
 * with fewer than `min` members after that falls apart into single entries,
 * so two names that happen to share `for-` do not become a group of two.
 * Order follows the first appearance of each entry in `items`.
 */
export function groupByPrefix<T>(items: readonly T[], nameOf: (item: T) => string, min = 3): PrefixEntry<T>[] {
  const candidates = items.map((item) => prefixesOf(nameOf(item)));
  const counts = new Map<string, number>();
  for (const prefixes of candidates) {
    for (const prefix of new Set(prefixes)) counts.set(prefix, (counts.get(prefix) ?? 0) + 1);
  }
  const chosen = candidates.map((prefixes) => {
    let best = "";
    for (const prefix of prefixes) {
      if ((counts.get(prefix) ?? 0) >= min && prefix.length > best.length) best = prefix;
    }
    return best;
  });
  const members = new Map<string, T[]>();
  chosen.forEach((prefix, index) => {
    if (!prefix) return;
    const list = members.get(prefix) ?? [];
    list.push(items[index]!);
    members.set(prefix, list);
  });
  const out: PrefixEntry<T>[] = [];
  const emitted = new Set<string>();
  items.forEach((item, index) => {
    const prefix = chosen[index]!;
    const group = prefix ? members.get(prefix) : undefined;
    if (!group || group.length < min) {
      out.push({ kind: "single", member: item });
      return;
    }
    if (emitted.has(prefix)) return;
    emitted.add(prefix);
    const name = nameOf(group[0]!);
    const separator = name.charAt(prefix.length) || "-";
    out.push({ kind: "group", prefix, label: `${prefix}${separator}*`, members: group });
  });
  return out;
}

/** Every separator-bounded prefix of a name, shortest first, never the whole name. */
function prefixesOf(name: string): string[] {
  const out: string[] = [];
  for (let index = 1; index < name.length; index += 1) {
    if (SEPARATOR.test(name.charAt(index)) && !SEPARATOR.test(name.charAt(index - 1))) {
      out.push(name.slice(0, index));
    }
  }
  return out;
}

// ── lineage ──────────────────────────────────────────────────────────────────

export interface LineageNode {
  /** The record id, or for a share the id in `Lineage.shareNodes`. */
  id: string;
  stage: Stage;
  label: string;
  item?: SubscriptionListItem;
  share?: SubStoreShareRow;
}

export type EdgeVia = "member" | "tag" | "node-source" | "share";

export interface LineageEdge {
  from: string;
  to: string;
  via: EdgeVia;
  /** The tag that made this member, for a tag edge. */
  tag?: string;
}

/** A reference a record declares that nothing in the store answers. */
export interface BrokenRef {
  owner: string;
  ref: string;
  via: "member" | "node-source" | "share";
  /** Why it is broken, in a clause: "no longer exists", "is a file". */
  reason: string;
}

export interface Lineage {
  nodes: Map<string, LineageNode>;
  /** Node ids per stage, in the order the map draws them. */
  columns: Record<Stage, string[]>;
  edges: LineageEdge[];
  broken: BrokenRef[];
  /** Direct neighbours, by node id. */
  upstream: Map<string, string[]>;
  downstream: Map<string, string[]>;
  /** Each share's node id, by share id. */
  shareNodes: Map<string, string>;
}

/**
 * A share's node id: `share:<share_id>`, with the prefix repeated until no
 * node already holds it. Record ids are free text, so a record may well be
 * called `share:sh-1`; without this a share would take that record's place on
 * the map, and the record's edges would lead to the share.
 */
export function shareNodeId(share: SubStoreShareRow, taken: { has(id: string): boolean } = new Set<string>()): string {
  let id = `share:${share.share_id}`;
  while (taken.has(id)) id = `share:${id}`;
  return id;
}

/**
 * The chain the records declare, with every reference resolved or reported.
 *
 * A combination's members are explicit ids plus every source carrying one of
 * its member tags, which is how the engine gathers them (subscription_kinds.go
 * `collectionMembers`), so the map draws what renders. A file draws from its
 * node source. A share publishes one record. A reference to a record that is
 * gone, or to one of the wrong kind, becomes a broken ref instead of an edge.
 */
export function buildLineage(
  items: readonly SubscriptionListItem[],
  shares: readonly SubStoreShareRow[] | undefined,
): Lineage {
  const nodes = new Map<string, LineageNode>();
  const byId = new Map<string, SubscriptionListItem>();
  for (const item of items) {
    byId.set(item.id, item);
    nodes.set(item.id, { id: item.id, stage: stageOfKind(item.kind), label: recordLabel(item), item });
  }
  const edges: LineageEdge[] = [];
  const broken: BrokenRef[] = [];
  const seenEdge = new Set<string>();
  function link(edge: LineageEdge): void {
    const key = `${edge.from}\u0000${edge.to}`;
    if (seenEdge.has(key)) return;
    seenEdge.add(key);
    edges.push(edge);
  }

  const subs = items.filter((item) => (item.kind || KIND_SUB) === KIND_SUB);
  for (const item of items) {
    const kind = item.kind || KIND_SUB;
    if (kind === KIND_COLLECTION) {
      for (const ref of item.members ?? []) {
        const member = byId.get(ref);
        if (!member) broken.push({ owner: item.id, ref, via: "member", reason: t.lineage.reasonGone });
        else if ((member.kind || KIND_SUB) !== KIND_SUB) {
          broken.push({ owner: item.id, ref, via: "member", reason: notASubscription(member.kind) });
        } else link({ from: ref, to: item.id, via: "member" });
      }
      for (const tag of item.member_tags ?? []) {
        const wanted = tag.trim();
        if (!wanted) continue;
        for (const sub of subs) {
          if ((sub.tags ?? []).includes(wanted)) link({ from: sub.id, to: item.id, via: "tag", tag: wanted });
        }
      }
    } else if (kind === KIND_FILE) {
      const ref = (item.node_source ?? "").trim();
      if (!ref) continue;
      const source = byId.get(ref);
      if (!source) broken.push({ owner: item.id, ref, via: "node-source", reason: t.lineage.reasonGone });
      else if (source.kind === KIND_FILE) broken.push({ owner: item.id, ref, via: "node-source", reason: t.lineage.reasonFileSource });
      else link({ from: ref, to: item.id, via: "node-source" });
    }
  }
  const shareNodes = new Map<string, string>();
  for (const share of shares ?? []) {
    const id = shareNodeId(share, nodes);
    shareNodes.set(share.share_id, id);
    nodes.set(id, { id, stage: "share", label: `/${share.slug}`, share });
    if (byId.has(share.subscription_id)) link({ from: share.subscription_id, to: id, via: "share" });
    else broken.push({ owner: id, ref: share.subscription_id, via: "share", reason: t.lineage.reasonGone });
  }

  const upstream = new Map<string, string[]>();
  const downstream = new Map<string, string[]>();
  for (const edge of edges) {
    (downstream.get(edge.from) ?? downstream.set(edge.from, []).get(edge.from)!).push(edge.to);
    (upstream.get(edge.to) ?? upstream.set(edge.to, []).get(edge.to)!).push(edge.from);
  }

  return { nodes, columns: orderColumns(nodes, upstream, downstream), edges, broken, upstream, downstream, shareNodes };
}

/** Why a combination's member is the wrong kind, as a clause after its name. */
function notASubscription(kind: string | undefined): string {
  if (kind === KIND_COLLECTION) return t.lineage.reasonIsCombination;
  if (kind === KIND_FILE) return t.lineage.reasonIsFile;
  return t.lineage.reasonWrongKind;
}

/**
 * The order the map draws each column in, chosen so edges cross as little as
 * a simple rule allows: combinations by name, sources by the first
 * combination they feed, files by where their node source sits, shares by
 * where their record sits. Ties fall back to the name.
 */
function orderColumns(
  nodes: Map<string, LineageNode>,
  upstream: Map<string, string[]>,
  downstream: Map<string, string[]>,
): Record<Stage, string[]> {
  const of = (stage: Stage) => [...nodes.values()].filter((node) => node.stage === stage);
  const byName = (a: LineageNode, b: LineageNode) => compareText(a.label, b.label) || compareText(a.id, b.id);
  const combos = of("combination").sort(byName).map((node) => node.id);
  const comboIndex = new Map(combos.map((id, index) => [id, index]));
  const firstCombo = (id: string) => {
    const hits = (downstream.get(id) ?? []).map((to) => comboIndex.get(to)).filter((n): n is number => n !== undefined);
    return hits.length ? Math.min(...hits) : Number.POSITIVE_INFINITY;
  };
  const sources = of("source")
    .sort((a, b) => firstCombo(a.id) - firstCombo(b.id) || byName(a, b))
    .map((node) => node.id);
  const sourceIndex = new Map(sources.map((id, index) => [id, index]));
  const rank = (ref: string | undefined): number => {
    if (!ref) return Number.POSITIVE_INFINITY;
    if (comboIndex.has(ref)) return comboIndex.get(ref)!;
    if (sourceIndex.has(ref)) return combos.length + sourceIndex.get(ref)!;
    return Number.POSITIVE_INFINITY;
  };
  const files = of("file")
    .sort((a, b) => rank(upstream.get(a.id)?.[0]) - rank(upstream.get(b.id)?.[0]) || byName(a, b))
    .map((node) => node.id);
  const fileIndex = new Map(files.map((id, index) => [id, index]));
  const place = (ref: string | undefined): number => {
    if (!ref) return Number.POSITIVE_INFINITY;
    if (sourceIndex.has(ref)) return sourceIndex.get(ref)!;
    if (comboIndex.has(ref)) return 1000 + comboIndex.get(ref)!;
    if (fileIndex.has(ref)) return 2000 + fileIndex.get(ref)!;
    return Number.POSITIVE_INFINITY;
  };
  const shares = of("share")
    .sort((a, b) => place(upstream.get(a.id)?.[0]) - place(upstream.get(b.id)?.[0]) || byName(a, b))
    .map((node) => node.id);
  return { source: sources, combination: combos, file: files, share: shares };
}

/**
 * The node and everything upstream and downstream of it: what feeds it, all
 * the way back, and what it feeds, all the way out. Siblings that share a
 * parent are not on the path, which is what makes the highlight readable.
 */
export function pathOf(lineage: Lineage, id: string): Set<string> {
  const out = new Set<string>([id]);
  const walk = (start: string, next: Map<string, string[]>) => {
    const queue = [start];
    while (queue.length) {
      const current = queue.shift()!;
      for (const neighbour of next.get(current) ?? []) {
        if (out.has(neighbour)) continue;
        out.add(neighbour);
        queue.push(neighbour);
      }
    }
  };
  walk(id, lineage.upstream);
  walk(id, lineage.downstream);
  return out;
}

/**
 * The shares a record reaches through what it feeds, each with the records in
 * between, nearest first. A source that no share names directly can still be
 * what a client receives: openjobs-host feeds merge-cd-openjobs, which
 * for-cdcd-loon renders, which /cdcd serves. Saying such a record is "not
 * published" would contradict the lit path on the map.
 */
export function reachingShares(lineage: Lineage, id: string): Array<{ share: string; via: string[] }> {
  const previous = new Map<string, string>();
  const seen = new Set<string>([id]);
  const queue = [id];
  const found: string[] = [];
  while (queue.length) {
    const current = queue.shift()!;
    for (const next of lineage.downstream.get(current) ?? []) {
      if (seen.has(next)) continue;
      seen.add(next);
      previous.set(next, current);
      if (lineage.nodes.get(next)?.stage === "share") found.push(next);
      else queue.push(next);
    }
  }
  return found.map((share) => {
    const via: string[] = [];
    for (let step = previous.get(share); step && step !== id; step = previous.get(step)) via.unshift(step);
    return { share, via };
  });
}

/** Everything downstream of a record, split the way a sentence names them. */
export function usedBy(lineage: Lineage, id: string): { combinations: string[]; files: string[]; shares: string[] } {
  const out = { combinations: [] as string[], files: [] as string[], shares: [] as string[] };
  for (const to of lineage.downstream.get(id) ?? []) {
    const stage = lineage.nodes.get(to)?.stage;
    if (stage === "combination") out.combinations.push(to);
    else if (stage === "file") out.files.push(to);
    else if (stage === "share") out.shares.push(to);
  }
  return out;
}

/** "feeds 2 combinations and 5 files", "published at /cdcd", "" when nothing. */
export function usedBySentence(lineage: Lineage, id: string): string {
  const used = usedBy(lineage, id);
  const parts: string[] = [];
  if (used.combinations.length) parts.push(t.nouns.combinations(used.combinations.length));
  if (used.files.length) parts.push(t.nouns.files(used.files.length));
  const feeds = parts.length ? t.lineage.feeds(formatList(parts)) : "";
  const shares = used.shares.map((share) => lineage.nodes.get(share)?.label ?? share);
  const published = shares.length ? t.lineage.publishedAt(shares.join(", ")) : "";
  return t.common.joinFacts([feeds, published].filter(Boolean));
}

// ── attention ────────────────────────────────────────────────────────────────

export type AttentionTone = "danger" | "warning" | "neutral";

export interface AttentionItem {
  /** Stable key for rendering. */
  key: string;
  tone: AttentionTone;
  /** The claim, one sentence, naming the record. */
  claim: string;
  /** The record that proves it, opened in the side panel. */
  recordId?: string;
  /** That record's name, for anything that points at it; ids stay out of copy. */
  recordName?: string;
  /**
   * The action that clears it. `publish` names a file whose fix is the
   * console's share form, opened on it: the one click that publishes it.
   * `search` narrows the layer it opens to the thing the claim names (one
   * share, by its slug).
   */
  action: { label: string; recordId?: string; view?: ViewId; facet?: Record<string, string>; publish?: string; search?: string };
}

export interface AttentionInput {
  items: readonly SubscriptionListItem[];
  /** `undefined` while the host's share list is unread, or when it failed. */
  shares: readonly SubStoreShareRow[] | undefined;
  sharesError?: string;
  lineage: Lineage;
  now: number;
}

const TONE_ORDER: Record<AttentionTone, number> = { danger: 0, warning: 1, neutral: 2 };

/**
 * What needs a hand, worst first. Each item is a claim about one record (or,
 * for publishing, about the files as a set), the record that proves it, and
 * the one action that clears it. An empty list means the page shows no
 * attention block at all.
 */
export function attentionItems(input: AttentionInput): AttentionItem[] {
  const { items, shares, lineage, now } = input;
  const byId = new Map(items.map((item) => [item.id, item]));
  const out: AttentionItem[] = [];
  const name = (id: string) => {
    const item = byId.get(id);
    return item ? recordLabel(item) : id;
  };

  for (const item of items) {
    if ((item.kind || KIND_SUB) !== KIND_SUB) continue;
    if (item.last_fetch_ok === false) {
      const when = item.last_fetch_at ? formatRelativeTime(item.last_fetch_at, now) : "";
      const reason = refreshFailureText(item.last_error);
      out.push({
        key: `fetch:${item.id}`,
        tone: "danger",
        claim: t.attention.fetchFailed(recordLabel(item), when, reason),
        recordId: item.id,
        action: { label: t.attention.open, recordId: item.id },
      });
    }
    const figures = providerFigures(item);
    const days = daysUntilExpiry(figures, now);
    const ratio = usageRatio(figures);
    const expiring = days !== null && days <= EXPIRY_WARN_DAYS;
    const heavy = ratio !== null && ratio > USAGE_WARN_RATIO;
    if (expiring || heavy) {
      const facts: string[] = [];
      if (expiring) facts.push(t.attention.providerExpiry(formatExpiry(figures, now)));
      if (heavy) facts.push(t.attention.providerTraffic(formatPercent(Math.round(ratio! * 100) / 100)));
      const dead = (days !== null && days < 0) || (ratio !== null && ratio >= 1);
      out.push({
        key: `provider:${item.id}`,
        tone: dead ? "danger" : "warning",
        claim: t.attention.providerClaim(recordLabel(item), t.common.joinFacts(facts)),
        recordId: item.id,
        action: { label: t.attention.open, recordId: item.id },
      });
    }
  }

  const brokenByOwner = new Map<string, BrokenRef[]>();
  for (const ref of lineage.broken) {
    const list = brokenByOwner.get(ref.owner) ?? [];
    list.push(ref);
    brokenByOwner.set(ref.owner, list);
  }
  for (const [owner, refs] of brokenByOwner) {
    const node = lineage.nodes.get(owner);
    if (!node) continue;
    if (node.stage === "combination") {
      const files = usedBy(lineage, owner).files.length;
      const missing = refs.map((ref) => ref.ref).join(", ");
      out.push({
        key: `members:${owner}`,
        tone: "danger",
        claim: t.attention.membersBroken(node.label, refs.length, missing, files),
        recordId: owner,
        action: { label: t.attention.open, recordId: owner },
      });
    } else if (node.stage === "file") {
      const ref = refs[0]!;
      out.push({
        key: `source:${owner}`,
        tone: "danger",
        claim: t.attention.sourceBroken(node.label, ref.ref, ref.reason),
        recordId: owner,
        action: { label: t.attention.open, recordId: owner },
      });
    } else if (node.stage === "share") {
      out.push({
        key: `orphan:${owner}`,
        tone: "danger",
        claim: t.attention.shareOrphan(node.label, ref0(refs)),
        action: { label: t.attention.review, view: "shares", search: node.share?.slug },
      });
    }
  }

  for (const item of items) {
    if (item.kind !== KIND_COLLECTION) continue;
    if ((lineage.downstream.get(item.id) ?? []).length) continue;
    out.push({
      key: `unused:${item.id}`,
      tone: "neutral",
      claim: t.attention.unused(recordLabel(item)),
      recordId: item.id,
      action: { label: t.attention.open, recordId: item.id },
    });
  }

  if (shares === undefined) {
    if (input.sharesError) {
      out.push({
        key: "shares:unread",
        tone: "warning",
        claim: t.attention.sharesUnread(input.sharesError),
        action: { label: t.attention.review, view: "shares" },
      });
    }
  } else {
    const files = items.filter((item) => item.kind === KIND_FILE);
    const live = new Set(shares.filter((share) => shareLive(share, now)).map((share) => share.subscription_id));
    const unpublished = files.filter((file) => !live.has(file.id));
    if (unpublished.length) {
      const all = unpublished.length === files.length;
      const one = unpublished.length === 1 ? unpublished[0]! : undefined;
      out.push({
        key: "files:unpublished",
        tone: "warning",
        claim: one
          ? t.attention.fileUnpublished(recordLabel(one))
          : all
            ? t.attention.noFilePublished
            : t.attention.filesUnpublished(unpublished.length),
        // One file: Publish opens the share form on it. Several: the Records
        // table narrowed to them, where each row carries its own Publish.
        recordId: one?.id,
        action: one
          ? { label: t.attention.publish, publish: one.id }
          : { label: t.attention.showThem, view: "records", facet: { kind: "file", published: "no" } },
      });
    }
    for (const share of shares) {
      if (!share.enabled || !share.expires_at) continue;
      const at = Date.parse(share.expires_at);
      if (!Number.isFinite(at)) continue;
      const days = Math.floor((at - now) / DAY_MS);
      if (days > EXPIRY_WARN_DAYS) continue;
      const record = name(share.subscription_id);
      const when = formatExpiry({ expire: Math.floor(at / 1000) }, now);
      out.push({
        key: `share:${share.share_id}`,
        tone: at <= now ? "danger" : "warning",
        claim: at <= now ? t.attention.shareExpired(`/${share.slug}`, record, when) : t.attention.shareExpiring(`/${share.slug}`, record, when),
        action: { label: t.attention.review, view: "shares", search: share.slug },
      });
    }
  }

  return out
    .map((item, index) => ({ item, index }))
    .sort((a, b) => TONE_ORDER[a.item.tone] - TONE_ORDER[b.item.tone] || a.index - b.index)
    .map(({ item }) => (item.recordId ? { ...item, recordName: name(item.recordId) } : item));
}

function ref0(refs: BrokenRef[]): string {
  return refs[0]?.ref ?? t.attention.aRecord;
}

/** Enabled and not past its expiry: a client fetching it gets a document. */
export function shareLive(share: SubStoreShareRow, now: number): boolean {
  if (!share.enabled) return false;
  if (!share.expires_at) return true;
  const at = Date.parse(share.expires_at);
  return !Number.isFinite(at) || at > now;
}

// ── state of one record ──────────────────────────────────────────────────────

export type HealthTone = "healthy" | "warning" | "error" | "neutral";

export interface RecordHealth {
  tone: HealthTone;
  /** A word or two for the dot. */
  label: string;
  /** The sentence behind it. */
  title: string;
}

/**
 * The one state a chip's dot and a row's state cell show, worst fact first.
 * Neutral is the honest answer when nothing is known yet, never healthy.
 */
export function recordHealth(
  item: SubscriptionListItem,
  lineage: Lineage,
  shares: readonly SubStoreShareRow[] | undefined,
  now: number,
  /** How this session's preview of the record went, when one has run. */
  preview?: { ok: boolean; reason?: string },
): RecordHealth {
  const kind = item.kind || KIND_SUB;
  const broken = lineage.broken.filter((ref) => ref.owner === item.id);
  if (broken.length) {
    const ref = broken[0]!;
    return { tone: "error", label: t.health.broken, title: t.health.brokenTitle(ref.ref, ref.reason) };
  }
  if (kind === KIND_SUB) {
    if (item.last_fetch_ok === false) {
      return { tone: "error", label: t.health.refreshFailed, title: refreshFailureText(item.last_error) || t.health.refreshFailedTitle };
    }
    const figures = providerFigures(item);
    const days = daysUntilExpiry(figures, now);
    const ratio = usageRatio(figures);
    if ((days !== null && days < 0) || (ratio !== null && ratio >= 1)) {
      return { tone: "error", label: days !== null && days < 0 ? t.health.expired : t.health.quotaSpent, title: t.common.joinFacts([formatExpiry(figures, now), formatUsage(figures)].filter(Boolean)) };
    }
    if ((days !== null && days <= EXPIRY_WARN_DAYS) || (ratio !== null && ratio > USAGE_WARN_RATIO)) {
      return { tone: "warning", label: days !== null && days <= EXPIRY_WARN_DAYS ? formatExpiry(figures, now) : t.health.quotaLow, title: t.common.joinFacts([formatExpiry(figures, now), formatUsage(figures)].filter(Boolean)) };
    }
  }
  if (kind === KIND_COLLECTION) {
    const failing = (lineage.upstream.get(item.id) ?? []).filter((id) => lineage.nodes.get(id)?.item?.last_fetch_ok === false);
    if (failing.length) {
      return { tone: "warning", label: t.health.memberFailing, title: t.health.memberFailingTitle(failing.length, failing.join(", ")) };
    }
  }
  const published = shares?.some((share) => share.subscription_id === item.id && shareLive(share, now));
  if (kind === KIND_FILE) {
    if (shares === undefined) return { tone: "neutral", label: t.health.unknown, title: t.health.unreadTitle };
    return published
      ? { tone: "healthy", label: t.health.published, title: t.health.publishedTitle }
      : { tone: "neutral", label: t.health.notPublished, title: t.health.notPublishedTitle };
  }
  if (preview && !preview.ok) {
    return { tone: "warning", label: t.health.previewFailed, title: maskUrlsIn(t.health.previewFailedTitle(preview.reason || t.health.noReason)) };
  }
  if (!preview) return { tone: "neutral", label: t.health.notCounted, title: t.health.notCountedTitle };
  return { tone: "healthy", label: t.health.ok, title: published ? t.health.okPublished : t.health.okTitle };
}
