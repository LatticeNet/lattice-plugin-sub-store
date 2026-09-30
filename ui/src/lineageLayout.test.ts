import { describe, expect, it } from "vitest";

import type { SubscriptionListItem } from "./client";
import { failingFixture, largeFixture, productionFixture, type Fixture, type StoredRecord } from "../dev/fixtures";
import { COLUMN_CAP, DENSE_EDGES, drawnEdges, layoutLineage, moreKey, paintedEdges, type LayoutOptions } from "./lineageLayout";
import { buildLineage, pathOf, type Lineage } from "./pipeline";

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
    const edges = drawnEdges(lineage, layout.anchor, null, new Set());
    const painted = paintedEdges(edges, false);
    expect(painted.dense).toBe(false);
    expect(painted.edges).toHaveLength(edges.length);
    expect(edges.length).toBeLessThanOrEqual(DENSE_EDGES);
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
    expect(more).toMatchObject({ key: moreKey("share"), kind: "more", label: `${layout.hidden.share} more` });
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
    const provider = "src-01";
    const attention = pathOf(lineage, provider);
    const edges = drawnEdges(lineage, layout.anchor, null, attention);
    expect(edges.length).toBeGreaterThan(DENSE_EDGES);
    const atRest = paintedEdges(edges, false);
    expect(atRest.dense).toBe(true);
    expect(atRest.edges.length).toBeGreaterThan(0);
    expect(atRest.edges.every((edge) => edge.attention)).toBe(true);
    const lit = pathOf(lineage, "combo-05");
    const selected = paintedEdges(drawnEdges(lineage, layout.anchor, lit, attention), true);
    expect(selected.edges.length).toBeGreaterThan(0);
    expect(selected.edges.every((edge) => edge.on)).toBe(true);
  });
});
