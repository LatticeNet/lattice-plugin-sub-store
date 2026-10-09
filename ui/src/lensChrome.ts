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
 * holds the page state the console keeps in its address: opening a record in
 * the side panel (`open`), on its own page (`record`), or switching layer with
 * a facet already applied (`view=records&kind=file&published=no`).
 */
export type TabId = ViewId;
/** "manual" is the store's own order (s1-plan section 3.3), and the default. */
export type SortKey = "manual" | "recent" | "name" | "status";
/** Expanded rows carry the kind icon and the remark; compact rows only the line. */
export type Density = "expanded" | "compact";

export interface LensReport {
  editing: boolean;
  selected: number;
}

/** Filters a layer reads from the address, set by whoever sent the operator there. */
export interface Facets {
  /** Records: "source", "combination" or "file"; every kind when unset. */
  kind: string;
  /** Records: "no" keeps the records no live share serves, "yes" the others. */
  published: string;
  /** Every table: "migrated" keeps imported records, "local" the ones made here. */
  origin: string;
  /** Records narrowed to files: "config", "script" or "plain". */
  type: string;
  /** Shares: "live" keeps the links a client gets something from, "dead" the rest. */
  link: string;
}

export interface LensOpenOptions {
  search?: string;
  focus?: boolean;
}

export interface LensChrome {
  search: Ref<string>;
  sort: Ref<SortKey>;
  density: Ref<Density>;
  facets: Facets;
  lenses: Record<TabId, LensReport>;
  /**
   * Switch the visible layer, optionally with facets applied. No-op outside
   * the shell. `search` fills the layer's filter field (an attention item
   * naming one share narrows Shares to it); `focus` moves the keyboard to the
   * layer that opened, for a control that leaves the screen it was on.
   */
  openLens: (tab: TabId, facets?: Partial<Facets>, options?: LensOpenOptions) => void;
  /** Peek at a record in the side panel. */
  openRecord: (id: string) => void;
  /** The record's own page. */
  openPage: (id: string) => void;
  /** The record the side panel shows, "" when it is closed. */
  openId: Ref<string>;
  /** The Records table's page; carried in the address so a reload lands on it. */
  page: Ref<number>;
}

const KEY: InjectionKey<LensChrome> = Symbol("lattice-lens-chrome");

function report(): LensReport {
  return { editing: false, selected: 0 };
}

export function createLensChrome(): LensChrome {
  return {
    search: ref(""),
    sort: ref("manual"),
    density: ref("expanded"),
    facets: reactive({ kind: "", published: "", origin: "", type: "", link: "" }),
    lenses: reactive({
      overview: report(),
      records: report(),
      shares: report(),
      settings: report(),
    }),
    openLens: () => {},
    openRecord: () => {},
    openPage: () => {},
    openId: ref(""),
    page: ref(1),
  };
}

export function provideLensChrome(chrome: LensChrome): void {
  provide(KEY, chrome);
}

/** A layer rendered without the shell (a contract test) gets a chrome of its own. */
export function useLensChrome(): LensChrome {
  return inject(KEY, null) ?? createLensChrome();
}
