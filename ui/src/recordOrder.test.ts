import { describe, expect, it } from "vitest";

import { dropMove, gapAt, inStoreOrder, placeAfter, placeBefore, stepMove, withOrder } from "./recordOrder";

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
