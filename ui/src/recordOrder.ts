/**
 * recordOrder.ts, the store's manual order and how one move changes it.
 *
 * `reorder` takes every live id exactly once (s1-plan section 3.3), so every
 * move here produces the whole order, however few rows the operator can see.
 * A move is stated against the rows on screen, which may be one kind of
 * record, one page or one search: "before this row", "after that row". Placed
 * that way it means the same thing in the full order whatever is hidden
 * between the two, which is what lets a filtered table be rearranged at all.
 */

interface Ordered {
  id: string;
  order?: number;
}

/**
 * The records in the order the store keeps: by `order` where the list sent
 * one, and in the list's own sequence otherwise (a runtime before the split
 * sends neither, and its sequence is the store's id order).
 */
export function inStoreOrder<T extends Ordered>(items: readonly T[]): T[] {
  return items
    .map((item, index) => ({ item, index }))
    .sort((a, b) => {
      const left = typeof a.item.order === "number" ? a.item.order : a.index;
      const right = typeof b.item.order === "number" ? b.item.order : b.index;
      return left - right || a.index - b.index;
    })
    .map(({ item }) => item);
}

/** `id` taken out and put back immediately before `anchor`; null when nothing moves. */
export function placeBefore(order: readonly string[], id: string, anchor: string): string[] | null {
  if (id === anchor || !order.includes(id) || !order.includes(anchor)) return null;
  const rest = order.filter((entry) => entry !== id);
  rest.splice(rest.indexOf(anchor), 0, id);
  return sameOrder(rest, order) ? null : rest;
}

/** `id` taken out and put back immediately after `anchor`; null when nothing moves. */
export function placeAfter(order: readonly string[], id: string, anchor: string): string[] | null {
  if (id === anchor || !order.includes(id) || !order.includes(anchor)) return null;
  const rest = order.filter((entry) => entry !== id);
  rest.splice(rest.indexOf(anchor) + 1, 0, id);
  return sameOrder(rest, order) ? null : rest;
}

/**
 * One step up or down among the rows the operator can see (`shown`, in the
 * order they are drawn): past the neighbour on that side. Null at either end
 * and for an id that is not shown.
 */
export function stepMove(order: readonly string[], shown: readonly string[], id: string, direction: -1 | 1): string[] | null {
  const at = shown.indexOf(id);
  if (at < 0) return null;
  const neighbour = shown[at + direction];
  if (neighbour === undefined) return null;
  return direction < 0 ? placeBefore(order, id, neighbour) : placeAfter(order, id, neighbour);
}

/**
 * A drop into the gap before `shown[gap]`, or after the last shown row when
 * `gap` is past the end. Null when the row lands where it already was.
 */
export function dropMove(order: readonly string[], shown: readonly string[], id: string, gap: number): string[] | null {
  if (!shown.length || !shown.includes(id)) return null;
  const clamped = Math.max(0, Math.min(gap, shown.length));
  if (clamped >= shown.length) return placeAfter(order, id, shown[shown.length - 1]!);
  return placeBefore(order, id, shown[clamped]!);
}

/**
 * Which gap a pointer at `y` is over, given the vertical middle of every shown
 * row in drawing order: the first row whose middle lies below the pointer, or
 * the end.
 */
export function gapAt(middles: readonly number[], y: number): number {
  const index = middles.findIndex((middle) => y < middle);
  return index < 0 ? middles.length : index;
}

/** The records rewritten into `order`, each carrying its new position. */
export function withOrder<T extends Ordered>(items: readonly T[], order: readonly string[]): T[] {
  const byId = new Map(items.map((item) => [item.id, item]));
  const out: T[] = [];
  for (const id of order) {
    const item = byId.get(id);
    if (item) out.push({ ...item, order: out.length });
  }
  // Anything the order did not name keeps its place after the rest rather than
  // vanishing from the table.
  for (const item of items) if (!order.includes(item.id)) out.push({ ...item, order: out.length });
  return out;
}

function sameOrder(a: readonly string[], b: readonly string[]): boolean {
  return a.length === b.length && a.every((id, index) => id === b[index]);
}
