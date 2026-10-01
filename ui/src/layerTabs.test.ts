import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

import { revealKeyOf, revealSelectedTab, vRevealSelected, type TabRow } from "./layerTabs";

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

describe("the Sub-Store layer row", () => {
  // The order of the shell's tabs: overview, sources, combinations, files, shares, settings.
  const unread = [null, null, null, null, null, null];
  const read = [null, 12, 3, 180, 40, null];

  it("reveals again when the tab counts are first read, not when a count changes its number", () => {
    // The counts land after a reload has applied ?view= and widen every tab.
    expect(revealKeyOf("files", unread)).not.toBe(revealKeyOf("files", read));
    expect(revealKeyOf("files", read)).toBe(revealKeyOf("files", [null, 13, 3, 179, 41, null]));
    // The share list lands on its own; that widens Shares, so it counts too.
    expect(revealKeyOf("files", [null, 12, 3, 180, null, null])).not.toBe(revealKeyOf("files", read));
    // A zero is a read count, not an unread one.
    expect(revealKeyOf("files", [null, 0, 0, 0, 0, null])).toBe(revealKeyOf("files", read));
  });

  it("reveals when the layer changes", () => {
    expect(revealKeyOf("files", read)).not.toBe(revealKeyOf("shares", read));
  });

  it("drives the directive: a re-render with the same key leaves a swiped row where it is", () => {
    const el = row(337, 421) as unknown as HTMLElement & { scrollLeft: number };
    const updated = vRevealSelected.updated as (el: HTMLElement, binding: { value: string; oldValue: string }) => void;
    el.scrollLeft = 0;
    updated(el, { value: revealKeyOf("files", read), oldValue: revealKeyOf("files", [null, 13, 3, 179, 41, null]) });
    expect(el.scrollLeft).toBe(0);
    updated(el, { value: revealKeyOf("files", read), oldValue: revealKeyOf("files", unread) });
    expect(el.scrollLeft).toBeGreaterThan(0);
  });

  it("is what the shell binds the row to", () => {
    const shell = readFileSync(new URL("./Shell.vue", import.meta.url), "utf8");
    expect(shell).toContain('v-reveal-selected="revealKey"');
    expect(shell).toContain("revealKeyOf(activeTab.value, tabs.map((tab) => tabCounts.value[tab.id]))");
  });
});
