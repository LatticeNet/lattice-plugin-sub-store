import { inject, provide, reactive, ref, type InjectionKey, type Ref } from "vue";

/**
 * What the shell's chrome and the lens behind it share.
 *
 * The toolbar (lens tabs, the one search field, the sort, the page's primary
 * action) belongs to the shell, because it is the same row on every lens. The
 * list it filters belongs to the lens. Rather than each lens drawing a toolbar
 * of its own, which is how four plugin pages came to differ in every toolbar
 * detail, the shell provides the state and the visible lens reads it. In the
 * other direction each lens reports the two facts that reshape the chrome:
 * whether it is inside its editor, where the list controls make no sense, and
 * how many rows are selected, so the page keeps room under its last row for
 * the selection bar.
 */
export type TabId = "subscriptions" | "files" | "shares" | "settings";
export type SortKey = "recent" | "name" | "status";

export interface LensReport {
  editing: boolean;
  selected: number;
}

export interface LensChrome {
  search: Ref<string>;
  sort: Ref<SortKey>;
  lenses: Record<TabId, LensReport>;
}

const KEY: InjectionKey<LensChrome> = Symbol("lattice-lens-chrome");

export function createLensChrome(): LensChrome {
  return {
    search: ref(""),
    sort: ref("recent"),
    lenses: reactive({
      subscriptions: { editing: false, selected: 0 },
      files: { editing: false, selected: 0 },
      shares: { editing: false, selected: 0 },
      settings: { editing: false, selected: 0 },
    }),
  };
}

export function provideLensChrome(chrome: LensChrome): void {
  provide(KEY, chrome);
}

/** A lens rendered without the shell (a contract test) gets a chrome of its own. */
export function useLensChrome(): LensChrome {
  return inject(KEY, null) ?? createLensChrome();
}
