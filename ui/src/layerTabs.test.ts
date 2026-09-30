import { describe, expect, it } from "vitest";

import { revealSelectedTab, vRevealSelected, type TabRow } from "./layerTabs";

/** A 341px row starting at x 16 whose selected tab sits at [left, right] on screen. */
function row(left: number, right: number, selected = true): TabRow & { scrollLeft: number } {
  const tab = { getBoundingClientRect: () => ({ left, right }) };
  return {
    scrollLeft: 100,
    getBoundingClientRect: () => ({ left: 16, right: 357 }),
    querySelector: (selector: string) => (selected && selector === '[aria-selected="true"]' ? tab : null),
  };
}

describe("the selected layer tab", () => {
  it("scrolls a tab cut off on the right fully into view, with a little room", () => {
    const cut = row(337, 421);
    revealSelectedTab(cut);
    expect(cut.scrollLeft).toBe(100 + (421 - (357 - 4)));
  });

  it("scrolls back to a tab off the left edge", () => {
    const behind = row(-60, 10);
    revealSelectedTab(behind);
    expect(behind.scrollLeft).toBe(100 - (16 + 4 + 60));
  });

  it("leaves a row alone when the tab is in view or nothing is selected", () => {
    const shown = row(40, 120);
    revealSelectedTab(shown);
    expect(shown.scrollLeft).toBe(100);
    const none = row(337, 421, false);
    revealSelectedTab(none);
    expect(none.scrollLeft).toBe(100);
    expect(() => revealSelectedTab(null)).not.toThrow();
  });

  it("reveals on mount and when the layer changes, not on every re-render", () => {
    const el = row(337, 421) as unknown as HTMLElement & { scrollLeft: number };
    const mounted = vRevealSelected.mounted as (el: HTMLElement) => void;
    const updated = vRevealSelected.updated as (el: HTMLElement, binding: { value: string; oldValue: string }) => void;
    mounted(el);
    const afterMount = el.scrollLeft;
    expect(afterMount).toBeGreaterThan(100);
    el.scrollLeft = 0;
    updated(el, { value: "attention", oldValue: "attention" });
    expect(el.scrollLeft).toBe(0);
    updated(el, { value: "attention", oldValue: "lines" });
    expect(el.scrollLeft).toBeGreaterThan(0);
  });
});
