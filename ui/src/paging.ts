/**
 * Pages of a table, as vpn-core pages its identities (vpnModel.ts pageRows):
 * a fixed number of rows a page, the requested page clamped into range, and
 * the ordinals of the first and last row for the footer ("Files 51 to 100 of
 * 160"). Pure, so the rules are tested without a page; usePages holds the
 * page number for a screen.
 */
import { computed, ref, watch, type ComputedRef, type Ref } from "vue";

export interface Page<T> {
  rows: T[];
  page: number;
  pages: number;
  /** Ordinal of the first and last row on the page; 0 when it shows none. */
  from: number;
  to: number;
  total: number;
}

export function pageRows<T>(rows: readonly T[], requestedPage: number, pageSize: number): Page<T> {
  const size = Math.max(1, Math.trunc(pageSize) || 1);
  const pages = Math.max(1, Math.ceil(rows.length / size));
  const page = Math.min(Math.max(1, Math.trunc(requestedPage) || 1), pages);
  const start = (page - 1) * size;
  const slice = rows.slice(start, start + size);
  return { rows: slice, page, pages, from: slice.length ? start + 1 : 0, to: start + slice.length, total: rows.length };
}

/** The page that holds the row at `index`, so a panel opened from a link has its row on screen; 0 when there is no such row. */
export function pageHolding(index: number, pageSize: number): number {
  if (index < 0) return 0;
  return Math.floor(index / Math.max(1, Math.trunc(pageSize) || 1)) + 1;
}

export interface PagesOptions {
  /**
   * The page number, when something else keeps it too: the shell carries the
   * Files page in the console's address, so a reload lands on the same page.
   */
  page?: Ref<number>;
  /**
   * Whether the rows have been read. Until then neither rule below runs: the
   * table is empty because nothing has arrived, not because the rows went,
   * and the filters a reload restores are not the operator changing them.
   * Clamping or restarting then threw away the page the address asked for.
   */
  ready?: () => boolean;
}

/**
 * A screen's page of `rows`. A change in `restartOn` (a search, a filter)
 * turns back to page 1. When the rows shrink under the page on screen (rows
 * deleted, a reload with fewer), the clamped page is kept as the page number,
 * so rows added later do not jump the view back to the page that had gone.
 */
export function usePages<T>(
  rows: () => readonly T[],
  pageSize: number,
  restartOn?: () => unknown,
  options: PagesOptions = {},
): { page: Ref<number>; table: ComputedRef<Page<T>> } {
  const page = options.page ?? ref(1);
  const ready = options.ready ?? (() => true);
  const table = computed(() => pageRows(rows(), page.value, pageSize));
  if (restartOn) watch(restartOn, () => { if (ready()) page.value = 1; });
  watch([() => table.value.page, ready], ([clamped, isReady]) => {
    if (isReady && clamped !== page.value) page.value = clamped;
  });
  return { page, table };
}

/** A page number off the wire: a positive whole number, or 1. */
export function pageFromState(value: string | undefined): number {
  return value && /^[1-9][0-9]{0,4}$/.test(value) ? Number(value) : 1;
}

/**
 * Select all on a page: select every row shown, or, when all of them are
 * selected already, clear just those. Rows selected on other pages stay as
 * they were; the batch bar acts only on rows on screen, and a row paged away
 * comes back checked.
 */
export function toggleShown(selected: ReadonlySet<string>, shown: readonly string[]): Set<string> {
  const next = new Set(selected);
  const all = shown.length > 0 && shown.every((id) => next.has(id));
  for (const id of shown) {
    if (all) next.delete(id);
    else next.add(id);
  }
  return next;
}
