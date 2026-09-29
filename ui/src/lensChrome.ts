import { inject, provide, reactive, ref, type InjectionKey, type Ref } from "vue";

import type { ViewId } from "./pipeline";

/**
 * What the shell's chrome and the layer behind it share.
 *
 * The toolbar (layer tabs, Cmd+K, the page's one primary action) belongs to
 * the shell, because it is the same row on every layer. Search and filters
 * belong to the table card. Rather than each layer drawing a toolbar of its
 * own, which is how four plugin pages came to differ in every toolbar detail,
 * the shell provides the filter state and the visible layer reads it. In the
 * other direction each layer reports the two facts that reshape the chrome:
 * whether it is inside its editor, where the list controls make no sense, and
 * how many rows are selected, so the page keeps room under its last row for
 * the selection bar.
 *
 * Navigation lives here too, because every layer needs it and only the shell
 * owns the address: opening a record in the side panel (`?open=`), on its own
 * page (`?record=`), or switching layer with a facet already applied
 * (`?view=files&published=no`).
 */
export type TabId = ViewId;
export type SortKey = "recent" | "name" | "status";

export interface LensReport {
  editing: boolean;
  selected: number;
}

/** Filters a layer reads from the address, set by whoever sent the operator there. */
export interface Facets {
  /** Files: "no" keeps the files no live share serves. */
  published: string;
  /** Every table: "migrated" keeps imported records, "local" the ones made here. */
  origin: string;
}

export interface LensChrome {
  search: Ref<string>;
  sort: Ref<SortKey>;
  facets: Facets;
  lenses: Record<TabId, LensReport>;
  /** Switch the visible layer, optionally with facets applied. No-op outside the shell. */
  openLens: (tab: TabId, facets?: Partial<Facets>) => void;
  /** Peek at a record in the side panel. */
  openRecord: (id: string) => void;
  /** The record's own page. */
  openPage: (id: string) => void;
  /** The record the side panel shows, "" when it is closed. */
  openId: Ref<string>;
}

const KEY: InjectionKey<LensChrome> = Symbol("lattice-lens-chrome");

function report(): LensReport {
  return { editing: false, selected: 0 };
}

export function createLensChrome(): LensChrome {
  return {
    search: ref(""),
    sort: ref("recent"),
    facets: reactive({ published: "", origin: "" }),
    lenses: reactive({
      overview: report(),
      sources: report(),
      combinations: report(),
      files: report(),
      shares: report(),
      settings: report(),
    }),
    openLens: () => {},
    openRecord: () => {},
    openPage: () => {},
    openId: ref(""),
  };
}

export function provideLensChrome(chrome: LensChrome): void {
  provide(KEY, chrome);
}

/** A layer rendered without the shell (a contract test) gets a chrome of its own. */
export function useLensChrome(): LensChrome {
  return inject(KEY, null) ?? createLensChrome();
}
