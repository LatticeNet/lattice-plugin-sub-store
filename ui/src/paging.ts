/**
 * Pages of a table, as vpn-core pages its identities (vpnModel.ts pageRows):
 * a fixed number of rows a page, the requested page clamped into range, and
 * the ordinals of the first and last row for the footer ("Files 51 to 100 of
 * 160"). Pure, so the rules are tested without a page.
 */

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
