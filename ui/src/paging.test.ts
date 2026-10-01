import { nextTick, ref } from "vue";
import { describe, expect, it } from "vitest";

import { pageHolding, pageRows, toggleShown, usePages } from "./paging";

const rows = Array.from({ length: 160 }, (_, index) => index);

describe("a table in pages of fifty", () => {
  it("cuts the rows and says which ones a page holds", () => {
    expect(pageRows(rows, 1, 50)).toMatchObject({ page: 1, pages: 4, from: 1, to: 50, total: 160 });
    const last = pageRows(rows, 4, 50);
    expect(last).toMatchObject({ page: 4, from: 151, to: 160 });
    expect(last.rows).toEqual(rows.slice(150));
  });

  it("clamps a page that no longer exists, as after a filter or a delete", () => {
    expect(pageRows(rows.slice(0, 60), 4, 50)).toMatchObject({ page: 2, pages: 2, from: 51, to: 60 });
    expect(pageRows(rows, 0, 50).page).toBe(1);
    expect(pageRows(rows, Number.NaN, 50).page).toBe(1);
  });

  it("is one page with no rows when there is nothing to show", () => {
    expect(pageRows([], 3, 50)).toEqual({ rows: [], page: 1, pages: 1, from: 0, to: 0, total: 0 });
  });

  it("finds the page that holds a row", () => {
    expect(pageHolding(0, 50)).toBe(1);
    expect(pageHolding(49, 50)).toBe(1);
    expect(pageHolding(50, 50)).toBe(2);
    expect(pageHolding(159, 50)).toBe(4);
    expect(pageHolding(-1, 50)).toBe(0);
  });
});

describe("a screen's page number", () => {
  it("keeps the clamped page after rows are deleted, so rows added later do not jump the view back", async () => {
    const list = ref(Array.from({ length: 120 }, (_, index) => index));
    const { page, table } = usePages(() => list.value, 50);
    page.value = 3;
    expect(table.value).toMatchObject({ page: 3, from: 101, to: 120 });
    list.value = list.value.slice(0, 100);
    expect(table.value.page).toBe(2);
    await nextTick();
    expect(page.value).toBe(2);
    list.value = Array.from({ length: 160 }, (_, index) => index);
    await nextTick();
    expect(table.value.page).toBe(2);
  });

  it("turns back to page 1 when a filter changes", async () => {
    const rowsRef = ref(Array.from({ length: 160 }, (_, index) => index));
    const query = ref("");
    const { page, table } = usePages(() => rowsRef.value, 50, () => query.value);
    page.value = 4;
    await nextTick();
    expect(table.value.page).toBe(4);
    query.value = "alice";
    await nextTick();
    expect(page.value).toBe(1);
  });

  it("leaves a page alone while the rows still reach it", async () => {
    const rowsRef = ref(Array.from({ length: 160 }, (_, index) => index));
    const { page } = usePages(() => rowsRef.value, 50);
    page.value = 4;
    rowsRef.value = rowsRef.value.slice(0, 151);
    await nextTick();
    expect(page.value).toBe(4);
  });
});

describe("select all on a page", () => {
  it("adds the rows shown, and keeps what was selected on other pages", () => {
    const fromPageOne = new Set(["a", "b"]);
    expect([...toggleShown(fromPageOne, ["c", "d"])].sort()).toEqual(["a", "b", "c", "d"]);
    // Part of the page selected: select all completes it rather than clearing it.
    expect([...toggleShown(new Set(["a", "c"]), ["c", "d"])].sort()).toEqual(["a", "c", "d"]);
  });

  it("clears only the rows shown when all of them are selected", () => {
    expect([...toggleShown(new Set(["a", "b", "c", "d"]), ["c", "d"])].sort()).toEqual(["a", "b"]);
  });

  it("does nothing on an empty page, and never changes the set it was given", () => {
    const selected = new Set(["a"]);
    expect([...toggleShown(selected, [])]).toEqual(["a"]);
    toggleShown(selected, ["b"]);
    expect([...selected]).toEqual(["a"]);
  });
});
