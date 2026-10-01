/**
 * Where the keyboard goes when rows leave the table.
 *
 * A confirmed delete removes the row whose menu opened the dialog, so the
 * dialog has nothing to give focus back to and it fell to <body>, the top of
 * the frame. vpn-core moves it to the row that now sits where the deleted one
 * was, beside the outcome; this is the same rule for Sub-Store's tables.
 */

/**
 * The row that takes focus after `deleted` leave a table listed in `order`:
 * the nearest surviving row above the first deleted one, else the first
 * surviving row, else "" when the table is empty.
 */
export function anchorAfterDelete(order: readonly string[], deleted: readonly string[]): string {
  const gone = new Set(deleted);
  const first = order.findIndex((id) => gone.has(id));
  if (first === -1) return "";
  for (let at = first - 1; at >= 0; at -= 1) {
    if (!gone.has(order[at]!)) return order[at]!;
  }
  return order.find((id) => !gone.has(id)) ?? "";
}

/**
 * Focus the anchor row's open button, or the first row in `scope`, or
 * `scope` itself. Only when nothing else holds focus: a failed delete leaves
 * the dialog's opener in place, and that is where the keyboard should stay.
 */
export function focusRowAfterDelete(scope: HTMLElement | null, anchor: string): void {
  if (typeof document === "undefined") return;
  const active = document.activeElement;
  if (active && active !== document.body) return;
  const escaped = anchor ? CSS.escape(anchor) : "";
  const target =
    (escaped ? document.querySelector<HTMLElement>(`[data-record-open="${escaped}"]`) : null) ??
    scope?.querySelector<HTMLElement>("[data-record-open]") ??
    scope;
  target?.focus();
}
