import { computed, nextTick, ref, shallowRef } from "vue";
import { describe, expect, it } from "vitest";

import type { SubscriptionListItem } from "./client";
import { failingFixture, largeFixture, productionFixture, type Fixture, type StoredRecord } from "../dev/fixtures";
import { COLUMN_CAP, DENSE_EDGES, drawnEdges, isDense, layoutLineage, moreKey, moreLabel, openSelectedGroup, paintedEdges, type LayoutOptions } from "./lineageLayout";
import { buildLineage, pathOf, STAGES, type Lineage } from "./pipeline";

/** What the plugin's `list` answers for a stored record, as the pipeline tests build it. */
function row(rec: StoredRecord): SubscriptionListItem {
  return {
    id: rec.id,
    kind: rec.kind || "sub",
    name: rec.name,
    tags: rec.tags,
    source: rec.source,
    has_url: Boolean(rec.url),
    has_inline_content: Boolean(rec.content),
    members: rec.members,
    member_tags: rec.member_tags,
    file_type: rec.file_type,
    node_source: rec.node_source,
    step_count: 0,
    disabled_step_count: 0,
    imported: Boolean(rec.origin),
  };
}

function lineageOf(fixture: Fixture): Lineage {
  return buildLineage(fixture.records.map(row), fixture.shares);
}

/** Every group key a lineage folds, from a layout that folds nothing. */
function STAGE_GROUPS(lineage: Lineage): string[] {
  const all = layoutLineage(lineage, { openGroups: new Set(), expanded: new Set(["source", "combination", "file", "share"]), pinned: new Set() });
  return Object.values(all.columns).flat().filter((item) => item.kind === "group").map((item) => item.key);
}

const rest = (over: Partial<LayoutOptions> = {}): LayoutOptions => ({ openGroups: new Set(), expanded: new Set(), pinned: new Set(), ...over });

describe("production keeps the map it had", () => {
  const lineage = lineageOf(productionFixture());
  const layout = layoutLineage(lineage, rest());

  it("folds no source or combination, folds files by person, and hides nothing", () => {
    expect(layout.columns.source.map((item) => item.kind)).toEqual(["node", "node", "node", "node", "node"]);
    expect(layout.columns.combination).toHaveLength(2);
    expect(layout.columns.file.filter((item) => item.kind === "group").map((item) => item.label)).toEqual(["for-cdcd-*", "for-openjobs-*"]);
    expect(Object.values(layout.hidden)).toEqual([0, 0, 0, 0]);
  });

  it("is sparse, so every dependency is painted at rest", () => {
    expect(lineage.edges).toHaveLength(17);
    expect(isDense(lineage)).toBe(false);
    const edges = drawnEdges(lineage, layout.anchor, null, new Set());
    expect(paintedEdges(edges, false, isDense(lineage))).toHaveLength(edges.length);
  });
});

describe("a short column is never folded", () => {
  it("keeps the failing store's three combinations apart, so the broken one shows", () => {
    const layout = layoutLineage(lineageOf(failingFixture()), rest());
    expect(layout.columns.combination.map((item) => item.kind)).toEqual(["node", "node", "node"]);
    expect(layout.columns.combination.map((item) => item.label)).toContain("merge-openjobs");
  });
});

describe("the 256-record budget stays one picture", () => {
  const lineage = lineageOf(largeFixture());
  const layout = layoutLineage(lineage, rest());

  it("folds sources and combinations by prefix and caps every column", () => {
    for (const stage of ["source", "combination", "file", "share"] as const) {
      expect(layout.columns[stage].length, stage).toBeLessThanOrEqual(COLUMN_CAP);
    }
    expect(layout.columns.source.some((item) => item.kind === "group")).toBe(true);
    expect(layout.columns.combination).toEqual([expect.objectContaining({ kind: "group", label: "merge-team-*", ids: expect.any(Array) })]);
    const more = layout.columns.share.at(-1)!;
    expect(more).toMatchObject({ key: moreKey("share"), kind: "more", label: `${layout.hidden.share} more shares` });
    // Every record is drawn somewhere: itself, its group, or its column's "more".
    for (const id of lineage.nodes.keys()) expect(layout.anchor.has(id), id).toBe(true);
  });

  it("never folds away a record the attention list names or the selected path", () => {
    const deep = lineage.columns.share.at(-1)!;
    const pinned = layoutLineage(lineage, rest({ pinned: new Set([deep]) }));
    expect(pinned.anchor.get(deep)).toBe(deep);
    expect(pinned.columns.share.some((item) => item.key === deep)).toBe(true);
  });

  it("opens a column past its cap and offers to fold it again", () => {
    const open = layoutLineage(lineage, rest({ expanded: new Set(["share"]) }));
    expect(open.columns.share.filter((item) => item.kind === "node")).toHaveLength(lineage.columns.share.length);
    expect(open.columns.share.at(-1)).toMatchObject({ kind: "more", label: "Show fewer", open: true });
  });

  it("is dense, so at rest it paints only the attention paths and, selected, only that path", () => {
    expect(lineage.edges.length).toBeGreaterThan(DENSE_EDGES);
    expect(isDense(lineage)).toBe(true);
    const attention = pathOf(lineage, "src-01");
    const atRest = paintedEdges(drawnEdges(lineage, layout.anchor, null, attention), false, true);
    expect(atRest.length).toBeGreaterThan(0);
    expect(atRest.every((edge) => edge.attention)).toBe(true);
    const lit = pathOf(lineage, "combo-05");
    const selected = paintedEdges(drawnEdges(lineage, layout.anchor, lit, attention), true, true);
    expect(selected.length).toBeGreaterThan(0);
    expect(selected.every((edge) => edge.on)).toBe(true);
  });

  it("stays as dense or sparse as the store is, however much is opened", () => {
    // Density is counted on the store before anything folds, so the layout
    // cannot move it: every group open, every column expanded, same answer.
    const everything = layoutLineage(lineage, rest({
      openGroups: new Set(STAGE_GROUPS(lineage)),
      expanded: new Set(["source", "combination", "file", "share"]),
    }));
    expect(drawnEdges(lineage, everything.anchor, null, new Set()).length).toBeGreaterThan(drawnEdges(lineage, layout.anchor, null, new Set()).length);
    expect(isDense(lineage)).toBe(true);
    const small = lineageOf(productionFixture());
    const opened = layoutLineage(small, rest({ openGroups: new Set(STAGE_GROUPS(small)), expanded: new Set(["file"]) }));
    expect(drawnEdges(small, opened.anchor, null, new Set()).length).toBeGreaterThan(0);
    expect(isDense(small)).toBe(false);
  });

  it("draws a record the attention list names inside a folded group, and leaves the group folded", () => {
    const pinned = layoutLineage(lineage, rest({ pinned: new Set(["src-01"]) }));
    const group = pinned.columns.source.find((item) => item.kind === "group" && item.ids.includes("src-01"))!;
    expect(group.open).toBe(false);
    expect(pinned.columns.source.find((item) => item.key === "src-01")).toMatchObject({ kind: "node", member: true });
    expect(pinned.anchor.get("src-01")).toBe("src-01");
    const neighbour = group.ids.find((id) => id !== "src-01")!;
    expect(pinned.anchor.get(neighbour)).toBe(group.key);
  });

  it("counts each drawn member as a row, so six pinned providers still leave the column at the cap", () => {
    const six = new Set(["src-01", "src-10", "src-16", "src-31", "src-34", "src-40"]);
    const layout = layoutLineage(lineage, rest({ pinned: six }));
    expect(layout.columns.source).toHaveLength(COLUMN_CAP);
    for (const id of six) expect(layout.anchor.get(id)).toBe(id);
    expect(layout.columns.source.at(-1)).toMatchObject({ kind: "more" });
    // Opening a group draws all of it and folds nothing else away.
    const group = layout.columns.source.find((item) => item.kind === "group")!;
    const opened = layoutLineage(lineage, rest({ pinned: six, openGroups: new Set([group.key]) }));
    expect(opened.hidden.source).toBe(layout.hidden.source);
  });
});

describe("the group a selection sits in", () => {
  const nothingOpen = (lineage: Lineage) => layoutLineage(lineage, { openGroups: new Set(), expanded: new Set(STAGES), pinned: new Set() }).groupOf;

  it("opens once the store that holds the record arrives after the selection", async () => {
    const lineage = shallowRef<Lineage>(buildLineage([], []));
    const selected = ref("src-06");
    const openGroups = ref(new Set<string>());
    const stop = openSelectedGroup(() => selected.value, () => nothingOpen(lineage.value), openGroups);
    expect([...openGroups.value]).toEqual([]);
    lineage.value = lineageOf(largeFixture());
    await nextTick();
    expect([...openGroups.value]).toEqual(["group:source:pasted-uk"]);
    stop();
  });

  it("opens for a selection made after the store, and leaves other open groups open", async () => {
    const lineage = shallowRef<Lineage>(lineageOf(largeFixture()));
    const groupOf = computed(() => nothingOpen(lineage.value));
    const selected = ref("");
    const openGroups = ref(new Set(["group:source:provider"]));
    const stop = openSelectedGroup(() => selected.value, () => groupOf.value, openGroups);
    selected.value = "src-06";
    await nextTick();
    expect([...openGroups.value].sort()).toEqual(["group:source:pasted-uk", "group:source:provider"]);
    stop();
  });
});

describe("what the folded rows say", () => {
  it("counts families and records with a noun, never a bare number", () => {
    expect(moreLabel("file", 75, 5, 0)).toBe("5 more families, 75 files");
    expect(moreLabel("file", 47, 3, 2)).toBe("3 more families and 2 files, 47 files");
    expect(moreLabel("source", 26, 0, 26)).toBe("26 more sources");
    expect(moreLabel("share", 1, 0, 1)).toBe("1 more share");
  });

  it("says how many of a folded group's members are drawn under it", () => {
    const lineage = lineageOf(largeFixture());
    const six = new Set(["src-01", "src-10", "src-16", "src-31", "src-34", "src-40"]);
    const layout = layoutLineage(lineage, rest({ pinned: six }));
    const group = layout.columns.source.find((item) => item.kind === "group" && item.ids.includes("src-01"))!;
    expect(group).toMatchObject({ open: false, shown: 6 });
    expect(group.ids).toHaveLength(14);
    const opened = layoutLineage(lineage, rest({ pinned: six, openGroups: new Set([group.key]) }));
    expect(opened.columns.source.find((item) => item.key === group.key)!.shown).toBeUndefined();
    const more = layout.columns.file.at(-1)!;
    expect(more.label).toMatch(/^\d+ more famil(y|ies), \d+ files$/);
  });
});
