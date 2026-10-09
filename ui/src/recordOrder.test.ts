import { describe, expect, it } from "vitest";

import { EDGE_SPEED, EDGE_ZONE, dropMove, edgeScrollSpeed, gapAt, inStoreOrder, namedMove, placeAfter, placeBefore, stepMove, withOrder } from "./recordOrder";

const ORDER = ["a", "b", "c", "d", "e"];

describe("the store's order", () => {
  it("follows each record's order where the list sent one, and the list's own sequence otherwise", () => {
    const rows = [{ id: "c", order: 2 }, { id: "a", order: 0 }, { id: "b", order: 1 }];
    expect(inStoreOrder(rows).map((row) => row.id)).toEqual(["a", "b", "c"]);
    // A runtime before the split sends no order: its sequence is the store's.
    expect(inStoreOrder([{ id: "z" }, { id: "y" }]).map((row) => row.id)).toEqual(["z", "y"]);
  });

  it("rewrites every position after a move, dense from zero", () => {
    const rows = ORDER.map((id, order) => ({ id, order, name: id.toUpperCase() }));
    const moved = withOrder(rows, ["c", "a", "b", "d", "e"]);
    expect(moved.map((row) => [row.id, row.order])).toEqual([["c", 0], ["a", 1], ["b", 2], ["d", 3], ["e", 4]]);
    expect(moved[0]!.name).toBe("C");
    // A record the order left out stays in the table, after the rest.
    expect(withOrder(rows, ["e", "d"]).map((row) => row.id)).toEqual(["e", "d", "a", "b", "c"]);
  });
});

describe("one move, against the rows on screen", () => {
  it("places a record before or after another, and reports a move that changes nothing as none", () => {
    expect(placeBefore(ORDER, "d", "b")).toEqual(["a", "d", "b", "c", "e"]);
    expect(placeAfter(ORDER, "a", "c")).toEqual(["b", "c", "a", "d", "e"]);
    expect(placeBefore(ORDER, "b", "c")).toBeNull();
    expect(placeAfter(ORDER, "c", "b")).toBeNull();
    expect(placeBefore(ORDER, "b", "b")).toBeNull();
    expect(placeBefore(ORDER, "x", "b")).toBeNull();
  });

  it("steps past the neighbour shown, whatever is hidden between them", () => {
    // Files only: b and d are sources the filter hides.
    const shown = ["a", "c", "e"];
    expect(stepMove(ORDER, shown, "e", -1)).toEqual(["a", "b", "e", "c", "d"]);
    expect(stepMove(ORDER, shown, "a", 1)).toEqual(["b", "c", "a", "d", "e"]);
    // At either end of what is shown, nothing moves.
    expect(stepMove(ORDER, shown, "a", -1)).toBeNull();
    expect(stepMove(ORDER, shown, "e", 1)).toBeNull();
    expect(stepMove(ORDER, shown, "b", 1)).toBeNull();
  });

  it("drops into a gap between the rows shown, or after the last of them", () => {
    const shown = ["b", "c", "d"];
    expect(dropMove(ORDER, shown, "d", 0)).toEqual(["a", "d", "b", "c", "e"]);
    expect(dropMove(ORDER, shown, "b", 3)).toEqual(["a", "c", "d", "b", "e"]);
    // The row's own gaps, before and after it, move nothing.
    expect(dropMove(ORDER, shown, "c", 1)).toBeNull();
    expect(dropMove(ORDER, shown, "c", 2)).toBeNull();
    // A gap past the end is clamped to it.
    expect(dropMove(ORDER, shown, "b", 99)).toEqual(["a", "c", "d", "b", "e"]);
  });

  it("finds the gap under the pointer from the rows' middles", () => {
    const middles = [20, 60, 100];
    expect(gapAt(middles, 5)).toBe(0);
    expect(gapAt(middles, 59)).toBe(1);
    expect(gapAt(middles, 61)).toBe(2);
    expect(gapAt(middles, 140)).toBe(3);
  });
});

describe("a move named from the row menu", () => {
  const shown = ["b", "d"];

  it("steps past the neighbour shown, or goes to either end of the rows shown", () => {
    expect(namedMove(ORDER, ORDER, "c", "up")).toEqual(["a", "c", "b", "d", "e"]);
    expect(namedMove(ORDER, ORDER, "c", "down")).toEqual(["a", "b", "d", "c", "e"]);
    expect(namedMove(ORDER, ORDER, "c", "top")).toEqual(["c", "a", "b", "d", "e"]);
    expect(namedMove(ORDER, ORDER, "c", "bottom")).toEqual(["a", "b", "d", "e", "c"]);
  });

  it("means the ends of what a filter shows, leaving the hidden rows where they are", () => {
    expect(namedMove(ORDER, shown, "d", "top")).toEqual(["a", "d", "b", "c", "e"]);
    expect(namedMove(ORDER, shown, "b", "bottom")).toEqual(["a", "c", "d", "b", "e"]);
  });

  it("is null for a row already at that end", () => {
    expect(namedMove(ORDER, ORDER, "a", "top")).toBeNull();
    expect(namedMove(ORDER, ORDER, "a", "up")).toBeNull();
    expect(namedMove(ORDER, ORDER, "e", "bottom")).toBeNull();
    expect(namedMove(ORDER, ORDER, "e", "down")).toBeNull();
  });
});

describe("scrolling while a row is held near the edge", () => {
  const height = 812;

  it("does nothing away from the edges", () => {
    expect(edgeScrollSpeed(EDGE_ZONE, height)).toBe(0);
    expect(edgeScrollSpeed(400, height)).toBe(0);
    expect(edgeScrollSpeed(height - EDGE_ZONE, height)).toBe(0);
  });

  it("goes faster the deeper the pointer is into a zone, up at the top and down at the bottom", () => {
    expect(edgeScrollSpeed(height - 1, height)).toBeGreaterThan(edgeScrollSpeed(height - EDGE_ZONE + 4, height));
    expect(edgeScrollSpeed(height - EDGE_ZONE + 4, height)).toBeGreaterThan(0);
    expect(edgeScrollSpeed(2, height)).toBeLessThan(edgeScrollSpeed(EDGE_ZONE - 4, height));
    expect(edgeScrollSpeed(EDGE_ZONE - 4, height)).toBeLessThan(0);
  });

  it("holds the top speed for a pointer past the edge", () => {
    expect(edgeScrollSpeed(height + 300, height)).toBe(EDGE_SPEED);
    expect(edgeScrollSpeed(-300, height)).toBe(-EDGE_SPEED);
  });
});
