import { pageFromState } from "./paging";
import { VIEW_IDS, type ViewId } from "./pipeline";
import type { Density, SortKey } from "./lensChrome";

/**
 * pageState.ts, the page's own state carried in the console's address.
 *
 * The frame's URL cannot carry it: the console builds a content-addressed
 * frame URL with no query and rebuilds it on every reload, so a layer, an open
 * record or a filter written into the frame's own address was gone the moment
 * the operator pressed reload. The console's address survives a reload and is
 * what an operator pastes to a colleague, so the bridge moves the state there
 * and back (wave CONTRACTS, "Plugin page state in the console address"):
 *
 *   host to plugin  `lattice.host.init` carries `pageState`, the query of the
 *                   console's plugin route, filtered by the rules below, and
 *                   the bridge client hands it over as HostInit.pageState;
 *   plugin to host  BridgeClient.sendState posts `lattice.plugin.state` with
 *                   the full state, debounced here, and the console replaces
 *                   its query with it.
 *
 * Both sides apply the same rules, and anything outside them drops the whole
 * message rather than part of it. The bridge client (plugin-bridge 0.2.0)
 * applies them on the wire; the copies below serve the shell's own encoding
 * and the dev harness's console address.
 */

export const MAX_STATE_KEYS = 16;
export const MAX_STATE_VALUE = 256;
export const STATE_DEBOUNCE_MS = 250;
const KEY_PATTERN = /^[a-z][a-z0-9_]{0,23}$/;

export type PageState = Record<string, string>;

/**
 * Keys the console keeps for itself (sign-in redirects, SSO and MFA). They
 * never cross the bridge in either direction, and no key of this page's state
 * may be one of them.
 */
export const RESERVED_STATE_KEYS: ReadonlySet<string> = new Set([
  "redirect", "next", "code", "state", "token", "sso_error", "totp_challenge", "mfa",
]);

/** A state without the reserved keys, whichever side it came from. */
export function withoutReserved(state: PageState): PageState {
  return Object.fromEntries(Object.entries(state).filter(([key]) => !RESERVED_STATE_KEYS.has(key)));
}

/** The contract's rules, whole or nothing: null when any entry breaks one. */
export function validPageState(value: unknown): PageState | null {
  if (!value || typeof value !== "object" || Array.isArray(value)) return null;
  const entries = Object.entries(value as Record<string, unknown>);
  if (entries.length > MAX_STATE_KEYS) return null;
  const out: PageState = {};
  for (const [key, entry] of entries) {
    if (!KEY_PATTERN.test(key) || typeof entry !== "string" || entry.length > MAX_STATE_VALUE) return null;
    out[key] = entry;
  }
  return out;
}

/** One string per state, independent of key order, for "has it changed". */
export function canonicalState(state: PageState): string {
  return new URLSearchParams(Object.entries(state).sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0))).toString();
}

/**
 * Everything the shell restores after a reload.
 *
 * `from` is the layer a record page goes back to; "" means the page was the
 * landing, and back then goes to Records. The facets are the filters the
 * tables keep: `kind`, `published`, `origin`, `type` (files only) and
 * `density` on Records, `link` on Shares, `q` and `sort` in the toolbar.
 */
export interface ShellState {
  view: ViewId;
  record: string;
  from: string;
  open: string;
  q: string;
  sort: SortKey;
  kind: string;
  density: Density;
  published: string;
  origin: string;
  type: string;
  link: string;
  /** The Records table's page; 1 is left out of the address. */
  page: number;
}

type FacetKey = "q" | "sort" | "kind" | "density" | "published" | "origin" | "type" | "link";

/** The values each facet can take; anything else reads as unset. */
const FACET_VALUES: Record<Exclude<FacetKey, "q">, readonly string[]> = {
  sort: ["manual", "recent", "name", "status"],
  kind: ["source", "combination", "file"],
  density: ["expanded", "compact"],
  published: ["yes", "no"],
  origin: ["migrated", "local"],
  type: ["config", "script", "plain"],
  link: ["live", "dead"],
};

/** The value each facet takes when the address leaves it out. */
const FACET_DEFAULTS: Partial<Record<FacetKey, string>> = { sort: "manual", density: "expanded" };

/**
 * Which facets a layer reads. Only those go into the address, so a link to
 * Sources does not carry a Files filter the operator set an hour ago and
 * cannot see from where they stand.
 */
const VIEW_FACETS: Record<ViewId, readonly FacetKey[]> = {
  overview: [],
  records: ["q", "sort", "kind", "density", "published", "origin", "type"],
  shares: ["q", "link"],
  settings: [],
};

/**
 * Addresses this page wrote before Records still land. The per-kind layers
 * (`?view=sources`, `combinations`, `files`) open Records filtered to their
 * kind, and the address before the layers (`?lens=`) does the same.
 */
const LEGACY_VIEWS: Record<string, { view: ViewId; kind?: string }> = {
  sources: { view: "records", kind: "source" },
  combinations: { view: "records", kind: "combination" },
  files: { view: "records", kind: "file" },
};
const LEGACY_LENS: Record<string, { view: ViewId; kind?: string }> = {
  subscriptions: { view: "records", kind: "source" },
  files: { view: "records", kind: "file" },
  shares: { view: "shares" },
  settings: { view: "settings" },
};
const VIEWS = new Set<string>(VIEW_IDS);

export function defaultShellState(): ShellState {
  return {
    view: "overview", record: "", from: "", open: "", q: "", sort: "manual", kind: "", density: "expanded",
    published: "", origin: "", type: "", link: "", page: 1,
  };
}

/** The shell's state as the wire carries it: unset and default values are left out. */
export function encodeShellState(state: ShellState): PageState {
  const out: PageState = {};
  const put = (key: string, value: string) => {
    const clipped = value.slice(0, MAX_STATE_VALUE);
    if (clipped) out[key] = clipped;
  };
  let table: ViewId | "" = state.view;
  if (state.record) {
    table = VIEWS.has(state.from) ? (state.from as ViewId) : "";
    if (table) out.view = table;
    put("record", state.record);
  } else {
    if (state.view !== "overview") out.view = state.view;
    put("open", state.open);
  }
  for (const key of table ? VIEW_FACETS[table] : []) {
    if (FACET_DEFAULTS[key] === state[key]) continue;
    // The file type narrows files only; with another kind on screen it filters nothing.
    if (key === "type" && state.kind !== "file") continue;
    put(key, state[key]);
  }
  // The page of the one paged table, while that table is what is on screen.
  if (table === "records" && !state.record && state.page > 1) put("page", String(state.page));
  return out;
}

/** Back from the wire, forgiving: an unknown view or facet value reads as unset. */
export function decodeShellState(state: PageState): ShellState {
  const out = defaultShellState();
  const asked = state.view ?? "";
  const legacy = state.lens ?? "";
  const landing = VIEWS.has(asked)
    ? { view: asked as ViewId }
    : Object.hasOwn(LEGACY_VIEWS, asked)
      ? LEGACY_VIEWS[asked]
      : Object.hasOwn(LEGACY_LENS, legacy)
        ? LEGACY_LENS[legacy]
        : undefined;
  const view = landing?.view;
  if (view) out.view = view;
  out.record = state.record ?? "";
  if (out.record) out.from = view ?? "";
  else out.open = state.open ?? "";
  out.q = state.q ?? "";
  out.page = pageFromState(state.page);
  for (const key of Object.keys(FACET_VALUES) as (keyof typeof FACET_VALUES)[]) {
    const value = state[key] ?? "";
    if (FACET_VALUES[key].includes(value)) (out as unknown as Record<string, string>)[key] = value;
  }
  // An old per-kind layer names its kind; an explicit `kind` beside it wins.
  if (!out.kind && landing?.kind) out.kind = landing.kind;
  return out;
}

export interface StateSender {
  /** What the console's address already says; an equal state is not sent back. */
  seed: (state: PageState) => void;
  /** The page's full state now; sent once it has been still for the delay. */
  push: (state: PageState) => void;
  flush: () => void;
  dispose: () => void;
}

/**
 * The debounce in front of the wire. Typing a search sends once, after the
 * operator stops, and the console's 60-per-minute budget per frame is never
 * spent on keystrokes or on echoing back what the console just sent.
 */
export function createStateSender(send: (state: PageState) => void, delayMs = STATE_DEBOUNCE_MS): StateSender {
  let last: string | null = null;
  let pending: PageState | null = null;
  let timer: ReturnType<typeof setTimeout> | undefined;
  const flush = () => {
    if (timer !== undefined) clearTimeout(timer);
    timer = undefined;
    if (!pending) return;
    const key = canonicalState(pending);
    const state = pending;
    pending = null;
    if (key === last) return;
    last = key;
    send(state);
  };
  return {
    seed: (state) => {
      last = canonicalState(state);
    },
    push: (state) => {
      pending = state;
      if (timer !== undefined) clearTimeout(timer);
      timer = setTimeout(flush, delayMs);
    },
    flush,
    dispose: () => {
      if (timer !== undefined) clearTimeout(timer);
      timer = undefined;
      pending = null;
    },
  };
}
