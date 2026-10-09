/**
 * Whether the store can take a new record, and where the create action shows,
 * decided without a DOM.
 *
 * Three surfaces offer create from the shell: the header's primary action
 * (one per layer), the overview's add menu, and the command palette. They
 * must agree, so the answer lives here once and Shell.vue only renders it.
 * Two rules from design 23 section 3.7 drive it: a read and empty layer's
 * empty state carries create, so the header does not repeat it; and nothing
 * offers create as if the record budget and the names in use were known
 * while the catalogue is unread.
 */
import { KIND_COLLECTION, KIND_FILE, KIND_SUB, MAX_SUBSCRIPTION_RECORDS } from "./client";
import type { ViewId } from "./pipeline";
import type { LoadState } from "./useSubscriptions";

export type CreateCommand = "new-subscription" | "new-collection" | "new-file";

/** The record catalogue as the create rules read it. */
export interface CatalogueView {
  state: LoadState;
  /**
   * The last read that finished failed, and none has succeeded since. It
   * stays true while a retry is in flight (state "loading"), so the header
   * keeps its disabled action in place instead of dropping it and adding it
   * back a moment later.
   */
  failed: boolean;
  /** The records as read. Only looked at once the state is "ready". */
  records: readonly { kind?: string }[];
}

export const UNREAD_REASON =
  "The record catalogue could not be read, so the record budget and the names in use are unknown. Refresh first";
export const READING_REASON =
  "The record catalogue is still being read, so the record budget and the names in use are not known yet";
export const LIMIT_REASON = `The store holds ${MAX_SUBSCRIPTION_RECORDS} records; delete one to add another`;
export const NO_SOURCE_REASON = "Create a source first. There is nothing to combine";

/** Why the store cannot take any new record right now; empty when it can. */
export function storeBlock(catalogue: CatalogueView, limit = MAX_SUBSCRIPTION_RECORDS): string {
  if (catalogue.state === "error") return UNREAD_REASON;
  if (catalogue.state !== "ready") return READING_REASON;
  return catalogue.records.length >= limit ? LIMIT_REASON : "";
}

function kindOf(record: { kind?: string }): string {
  return record.kind || KIND_SUB;
}

/**
 * Why each create command is blocked, empty where it is not. A combination
 * also needs a subscription to combine; that reason comes after an unread
 * catalogue (nothing is known then) and before the record limit (which
 * deleting a record would lift, while an empty source list would not).
 */
export function createBlocks(catalogue: CatalogueView, limit = MAX_SUBSCRIPTION_RECORDS): Record<CreateCommand, string> {
  const store = storeBlock(catalogue, limit);
  const unread = catalogue.state !== "ready";
  const noSource = !unread && !catalogue.records.some((record) => kindOf(record) === KIND_SUB);
  return {
    "new-subscription": store,
    "new-collection": unread ? store : noSource ? NO_SOURCE_REASON : store,
    "new-file": store,
  };
}

interface LayerCreate {
  command: CreateCommand;
  label: string;
  /** What the action makes, as its title when nothing blocks it. */
  hint: string;
}

const SUBSCRIPTION: LayerCreate = {
  command: "new-subscription",
  label: "New source",
  hint: "One source of nodes, processed and served",
};

/** What each kind of record is created with. */
const KIND_CREATE: Record<string, LayerCreate> = {
  [KIND_SUB]: SUBSCRIPTION,
  [KIND_COLLECTION]: {
    command: "new-collection",
    label: "New combination",
    hint: "Merge several sources and process the result as one",
  },
  [KIND_FILE]: {
    command: "new-file",
    label: "New file",
    hint: "A document served as it is, with its proxy list kept in step",
  },
};

/** The kind the Records layer's kind filter stands for, "" for every kind. */
const FACET_KIND: Record<string, string> = { source: KIND_SUB, combination: KIND_COLLECTION, file: KIND_FILE };

/**
 * The layer's create, and the kind it is narrowed to: Records creates the
 * kind its filter shows, a source with every kind showing. Shares and
 * Settings have no create of their own.
 */
function layerCreate(tab: ViewId, kindFacet: string): { create: LayerCreate; kind: string } | null {
  if (tab === "overview") return { create: SUBSCRIPTION, kind: "" };
  if (tab !== "records") return null;
  const kind = FACET_KIND[kindFacet] ?? "";
  return { create: KIND_CREATE[kind || KIND_SUB]!, kind };
}

/** The layer's list was read and holds nothing of the kind it shows. */
function layerEmpty(kind: string, records: readonly { kind?: string }[]): boolean {
  if (!kind) return records.length === 0;
  return !records.some((record) => kindOf(record) === kind);
}

export interface HeaderCreateInput {
  tab: ViewId;
  /** The Records layer's kind filter (`source`, `combination`, `file`), "" for all. */
  kind?: string;
  catalogue: CatalogueView;
  caps: { ready: boolean; mutate: boolean };
  /** A record page or an editor is up; neither carries the layer's create. */
  covered: boolean;
}

export interface HeaderCreate extends LayerCreate {
  disabled: boolean;
  /** The control's title: why it is disabled, or what it makes. */
  title: string;
  /** The split button, with the other kinds behind a chevron (Overview, and Records showing every kind). */
  menu: boolean;
  /**
   * The chevron is disabled while the catalogue is unread. At the record
   * limit it still opens, and its items say why they are disabled.
   */
  menuDisabled: boolean;
}

/**
 * The header's create action for the layer on screen, or null where none
 * shows.
 *
 * A session that may not create gets no action at all (the verb is absent,
 * not disabled). While the first read is in flight nothing shows, because
 * the layer may turn out empty and its empty state then carries create. A
 * failed read, and a retry after one, show the action disabled with the
 * reason. A read and empty layer leaves create to its empty state.
 */
export function headerCreate(input: HeaderCreateInput): HeaderCreate | null {
  const layer = layerCreate(input.tab, input.kind ?? "");
  if (!layer || input.covered || !input.caps.ready || !input.caps.mutate) return null;
  const { state, failed, records } = input.catalogue;
  if (state !== "ready" && state !== "error" && !failed) return null;
  if (state === "ready" && layerEmpty(layer.kind, records)) return null;
  const reason = createBlocks(input.catalogue)[layer.create.command];
  return {
    ...layer.create,
    disabled: reason !== "",
    title: reason || layer.create.hint,
    menu: !layer.kind,
    menuDisabled: state !== "ready",
  };
}
