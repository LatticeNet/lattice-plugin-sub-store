/**
 * A row's click opens its side panel, so it has to leave clicks inside a
 * selection cell alone. PcSelectCell (plugin-bridge 0.2.0) puts the box in a
 * label that fills the cell, so a tap beside the 16px box toggles it, and
 * both the label's click and the box's bubble up to the row.
 */

/** True when the click landed in a selection cell, on the box or beside it. */
export function isSelectCell(target: EventTarget | null): boolean {
  return target instanceof Element && !!target.closest(".pc-select");
}
