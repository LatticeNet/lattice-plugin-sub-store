/**
 * Keeps the selected layer tab in view.
 *
 * Below 620px the layer row is a segmented control that scrolls sideways and
 * is wider than a phone. A link or a reload onto a later layer (Files,
 * Shares, Settings) left its tab cut off or off screen, so the page did not say
 * which layer it was on. The row scrolls itself to show the selected tab on
 * mount and whenever the selection changes, and at no other time: a re-render
 * for anything else (a count arriving, the proof line's age) must not snap
 * back a row the operator is swiping through.
 *
 * Only the row scrolls. scrollIntoView would also scroll the page and, in a
 * frame, the console around it.
 *
 * revealSelectedTab and the directive are the same as
 * lattice-plugin-vpn-core/ui/src/layerTabs.ts; revealKeyOf is Sub-Store's.
 */
import type { ObjectDirective } from "vue";

/** A tab row as this needs it; duck-typed so the tests run without a DOM. */
export interface TabRow {
  scrollLeft: number;
  getBoundingClientRect(): { left: number; right: number };
  querySelector(selector: string): { getBoundingClientRect(): { left: number; right: number } } | null;
}

/** Room left beside the tab, so its edge does not sit on the row's. */
const EDGE = 4;

export function revealSelectedTab(row: TabRow | null | undefined): void {
  const tab = row?.querySelector('[aria-selected="true"]');
  if (!row || !tab) return;
  const box = row.getBoundingClientRect();
  const at = tab.getBoundingClientRect();
  if (at.left < box.left + EDGE) row.scrollLeft -= box.left + EDGE - at.left;
  else if (at.right > box.right - EDGE) row.scrollLeft += at.right - (box.right - EDGE);
}

/** `v-reveal-selected="layer"` on the tab row: reveals on mount and when `layer` changes. */
export const vRevealSelected: ObjectDirective<HTMLElement, string> = {
  mounted: (el) => revealSelectedTab(el),
  updated: (el, binding) => {
    if (binding.value !== binding.oldValue) revealSelectedTab(el);
  },
};

/**
 * What the Sub-Store row passes to `v-reveal-selected`: the selected layer,
 * plus which tabs have a count yet. The counts land about half a second after
 * a reload has applied ?view=, and widening every tab at once pushed Files
 * (and Shares) back past a phone's edge, so the row reveals once more when a
 * count goes from unread to read. A count that only changes its number keeps
 * the key, and does not move a row the operator is swiping through.
 */
export function revealKeyOf(active: string, counts: readonly (number | null | undefined)[]): string {
  return `${active}:${counts.map((count) => (count === null || count === undefined ? "-" : "#")).join("")}`;
}
