/**
 * lineageLayout.ts, what the overview's map draws when the store is large.
 *
 * Production holds 23 records and the map draws every one of them with every
 * dependency, which reads well. The 256-record budget does not: 40 sources in
 * one column made a 2,180 px page and the edges between them a knot. Three
 * rules keep the map one picture at any size, and leave production as it was:
 *
 * 1. Records fold by name prefix, three or more to a group. Files always do,
 *    as they did (`for-openjobs-*`): a file is named for a person and a client.
 *    Sources and combinations fold only in a column longer than the cap,
 *    because folding three combinations into `merge-*` hides which one is
 *    broken. Shares never fold: each is one URL, named for one client.
 * 2. Each column shows at most `COLUMN_CAP` entries and folds the rest into
 *    one "N more" item that opens the column in place. The selected record's
 *    path and the records the attention list names are never folded away.
 * 3. A map with more dependencies than `DENSE_EDGES` stops drawing all of
 *    them. At rest it draws the paths of the records that need attention;
 *    with a record selected, that record's path. Below the threshold it draws
 *    everything, as before, and dims what is off the selected path.
 */

import { groupByPrefix, STAGES, type Lineage, type Stage } from "./pipeline";

export const COLUMN_CAP = 8;
/** Above this many drawn dependencies the map draws paths, not everything. */
export const DENSE_EDGES = 30;

export type MapItemKind = "node" | "group" | "more";

export interface MapItem {
  key: string;
  kind: MapItemKind;
  stage: Stage;
  /** Node ids the item stands for: one, a group's members, or what "more" hides. */
  ids: string[];
  label: string;
  /** A group drawn open, or a column opened past its cap. */
  open?: boolean;
  /** A member drawn under its open group. */
  member?: boolean;
}

export interface MapLayout {
  columns: Record<Stage, MapItem[]>;
  /** Where each node is drawn: itself, its folded group, or its column's "more". */
  anchor: Map<string, string>;
  /** The group each grouped node belongs to, folded or not. */
  groupOf: Map<string, string>;
  /** Records each column folds under "more", for its label and the summary. */
  hidden: Record<Stage, number>;
}

export interface LayoutOptions {
  openGroups: ReadonlySet<string>;
  expanded: ReadonlySet<Stage>;
  /** Node ids that stay drawn whatever the cap. */
  pinned: ReadonlySet<string>;
  cap?: number;
}

/** Whether a column folds by prefix: files always, sources and combinations past the cap. */
function folds(stage: Stage, count: number, cap: number): boolean {
  if (stage === "file") return true;
  if (stage === "share") return false;
  return count > cap;
}

export function groupKey(stage: Stage, prefix: string): string {
  return `group:${stage}:${prefix}`;
}

export function moreKey(stage: Stage): string {
  return `more:${stage}`;
}

interface Entry {
  key: string;
  kind: "node" | "group";
  ids: string[];
  label: string;
}

export function layoutLineage(lineage: Lineage, options: LayoutOptions): MapLayout {
  const cap = Math.max(2, options.cap ?? COLUMN_CAP);
  const anchor = new Map<string, string>();
  const groupOf = new Map<string, string>();
  const columns = {} as Record<Stage, MapItem[]>;
  const hiddenCount = {} as Record<Stage, number>;
  const labelOf = (id: string) => lineage.nodes.get(id)?.label ?? id;
  const nameOf = (id: string) => lineage.nodes.get(id)?.item?.name ?? labelOf(id);

  for (const stage of STAGES) {
    const ids = lineage.columns[stage];
    const entries: Entry[] = folds(stage, ids.length, cap)
      ? groupByPrefix(ids, nameOf).map((entry) =>
          entry.kind === "group"
            ? { key: groupKey(stage, entry.prefix), kind: "group", ids: entry.members, label: entry.label }
            : { key: entry.member, kind: "node", ids: [entry.member], label: labelOf(entry.member) },
        )
      : ids.map((id) => ({ key: id, kind: "node", ids: [id], label: labelOf(id) }));
    for (const entry of entries) {
      if (entry.kind === "group") for (const id of entry.ids) groupOf.set(id, entry.key);
    }

    const expanded = options.expanded.has(stage);
    const over = entries.length > cap;
    let visible = entries;
    let folded: Entry[] = [];
    if (over && !expanded) {
      // One slot goes to "more". Pinned entries (the selection, the records
      // the attention list names) take their slots first, wherever they sit,
      // and the first of the rest fill what is left, so a column stays at the
      // cap unless more than that is pinned. Order is kept.
      const pinned = entries.filter((entry) => entry.ids.some((id) => options.pinned.has(id)));
      const budget = Math.max(0, cap - 1 - pinned.length);
      const keep = new Set([...pinned, ...entries.filter((entry) => !pinned.includes(entry)).slice(0, budget)]);
      visible = entries.filter((entry) => keep.has(entry));
      folded = entries.filter((entry) => !keep.has(entry));
    }

    const items: MapItem[] = [];
    for (const entry of visible) {
      if (entry.kind === "node") {
        items.push({ key: entry.key, kind: "node", stage, ids: entry.ids, label: entry.label });
        anchor.set(entry.key, entry.key);
        continue;
      }
      const open = options.openGroups.has(entry.key);
      items.push({ key: entry.key, kind: "group", stage, ids: entry.ids, label: entry.label, open });
      for (const id of entry.ids) {
        anchor.set(id, open ? id : entry.key);
        if (open) items.push({ key: id, kind: "node", stage, ids: [id], label: labelOf(id), member: true });
      }
    }
    const hiddenIds = folded.flatMap((entry) => entry.ids);
    hiddenCount[stage] = hiddenIds.length;
    if (hiddenIds.length) {
      items.push({ key: moreKey(stage), kind: "more", stage, ids: hiddenIds, label: `${hiddenIds.length} more`, open: false });
      for (const id of hiddenIds) anchor.set(id, moreKey(stage));
    } else if (over && expanded) {
      items.push({ key: moreKey(stage), kind: "more", stage, ids: [], label: "Show fewer", open: true });
    }
    columns[stage] = items;
  }
  return { columns, anchor, groupOf, hidden: hiddenCount };
}

export interface DrawnEdge {
  from: string;
  to: string;
  /** Every dependency behind it came from a member tag. */
  tag: boolean;
  /** On the selected path. */
  on: boolean;
  /** On the path of a record the attention list names. */
  attention: boolean;
}

/** One edge per pair of drawn items, whatever folded into them. */
export function drawnEdges(lineage: Lineage, anchor: ReadonlyMap<string, string>, lit: ReadonlySet<string> | null, attention: ReadonlySet<string>): DrawnEdge[] {
  const seen = new Map<string, DrawnEdge>();
  for (const edge of lineage.edges) {
    const from = anchor.get(edge.from) ?? edge.from;
    const to = anchor.get(edge.to) ?? edge.to;
    if (from === to) continue;
    const key = `${from}\u0000${to}`;
    const on = !!lit && lit.has(edge.from) && lit.has(edge.to);
    const warm = attention.has(edge.from) && attention.has(edge.to);
    const existing = seen.get(key);
    if (existing) {
      existing.on ||= on;
      existing.attention ||= warm;
      existing.tag &&= edge.via === "tag";
      continue;
    }
    seen.set(key, { from, to, tag: edge.via === "tag", on, attention: warm });
  }
  return [...seen.values()];
}

/**
 * The edges the map paints. A sparse map paints all of them; a dense one
 * paints the selected path, or at rest the attention paths.
 */
export function paintedEdges(edges: readonly DrawnEdge[], lit: boolean): { edges: DrawnEdge[]; dense: boolean } {
  const dense = edges.length > DENSE_EDGES;
  if (!dense) return { edges: [...edges], dense };
  return { edges: edges.filter((edge) => (lit ? edge.on : edge.attention)), dense };
}
