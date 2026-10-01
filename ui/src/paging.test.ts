import { describe, expect, it } from "vitest";

import { pageHolding, pageRows } from "./paging";

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
