/**
 * chassis-workaround: PcSelectCell draws a bare 16px checkbox in a 40px
 * cell, with no label around it. On a phone a tap ten pixels beside the box
 * landed on the row and opened the side panel instead of selecting it. Until
 * plugin-bridge wraps the box in a label that fills the cell, a click
 * anywhere in a selection cell, header or row, is handed to its checkbox,
 * and the row's own click handler leaves those cells alone (isSelectCell).
 */

/** True when the click landed in a selection cell, on the box or beside it. */
export function isSelectCell(target: EventTarget | null): boolean {
  return target instanceof Element && !!target.closest(".pc-select");
}

/**
 * Bound on the table's container. A click on the box itself is the native
 * toggle and is left alone; a click beside it clicks the box, once, and that
 * click reaches here again with the box as its target.
 */
export function forwardSelectCellClick(event: MouseEvent): void {
  const target = event.target;
  if (!(target instanceof Element) || target instanceof HTMLInputElement) return;
  const cell = target.closest<HTMLElement>("td.pc-select, th.pc-select");
  cell?.querySelector<HTMLInputElement>("input[type=checkbox]:not(:disabled)")?.click();
}
