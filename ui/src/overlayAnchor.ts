/**
 * Where an overlay has to appear when the document is not a viewport.
 *
 * This plugin renders inside an iframe the host sizes to its content: the frame
 * is as tall as the page it holds, and the browser viewport scrolls the parent
 * document instead. Two consequences drive everything here.
 *
 * `position: fixed` resolves against the frame, not against what the operator
 * can see. A sheet centred with `inset: 0` on a 1600px frame lands at the top of
 * that frame, which, when the operator clicked a row near the bottom, is 700px
 * above the fold. The click reads as doing nothing.
 *
 * `position: sticky` never activates for the same reason: sticky needs a
 * scrollport and the frame has none.
 *
 * So overlays are positioned in DOCUMENT coordinates, anchored to whatever the
 * operator just clicked. The frame does not scroll, so an element that was
 * visible enough to click is still visible, and the overlay opens beside it.
 */

/** How far above the anchor an overlay starts, so the trigger stays visible. */
const ANCHOR_OFFSET = 8;
/** Never open flush against the document top; there is a tab bar up there. */
const MIN_TOP = 12;

/**
 * The document-space Y an overlay opened from `event` should use.
 *
 * Falls back to the current scroll offset when there is no event. A keyboard
 * shortcut, or an overlay opened programmatically, which is the top of what
 * the operator can see in every case except a frame taller than the viewport
 * that the host has scrolled past, and that case has no better answer from
 * inside the sandbox.
 */
export function anchorTopFrom(event?: Event | null): number {
  const scrollY = typeof window === "undefined" ? 0 : window.scrollY;
  // Duck-typed rather than `instanceof Element` so this stays a plain function
  // the tests can call without a DOM.
  const target = (event?.currentTarget ?? event?.target) as { getBoundingClientRect?: () => { top: number } } | null;
  if (target && typeof target.getBoundingClientRect === "function") {
    return Math.max(MIN_TOP, target.getBoundingClientRect().top + scrollY - ANCHOR_OFFSET);
  }
  return Math.max(MIN_TOP, scrollY + ANCHOR_OFFSET);
}

/**
 * Clamp an overlay so it cannot start below the content it would need to be
 * read against. `documentHeight` is the frame height the host already knows.
 */
export function clampAnchorTop(
  top: number,
  overlayHeight = 0,
  documentHeight = typeof document === "undefined" ? 0 : document.documentElement.scrollHeight,
): number {
  const maxTop = Math.max(MIN_TOP, documentHeight - overlayHeight - MIN_TOP);
  return Math.min(Math.max(MIN_TOP, top), maxTop);
}
