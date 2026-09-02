import { KIND_COLLECTION, KIND_FILE, KIND_SUB, type SubscriptionListItem } from "./client";

/**
 * What can be done to a record, declared once.
 *
 * This knowledge was inline in every surface that offered it: two row menus,
 * two batch bars, and it was about to be copied a fifth time into the command
 * palette. Each copy carried its own `:disabled` expression, so "why is this
 * greyed out" had a different answer depending on where you clicked, and a new
 * capability meant finding every copy.
 *
 * The split is deliberate: this module says WHAT exists and WHEN it is allowed,
 * and each screen supplies HOW to run it. The what/when half is pure, so it can
 * be tested without mounting a 1700 line screen; the how half genuinely differs
 * (one screen opens a drawer, the other a sheet) and is not worth pretending is
 * shared.
 */

export type RecordKind = typeof KIND_SUB | typeof KIND_COLLECTION | typeof KIND_FILE;

export type ActionId =
  | "edit"
  | "refresh"
  | "output"
  | "preview"
  | "share"
  | "publish"
  | "duplicate"
  | "delete";

/** What the signed bundle and this session's token allow. */
export interface ActionCapabilities {
  /** The host handshake has landed; before that nothing is known. */
  ready: boolean;
  mutate: boolean;
  /** Reading a source again. Its own method, not a write. */
  fetch: boolean;
  preview: boolean;
  render: boolean;
  publish: boolean;
}

export interface ActionDeclaration {
  id: ActionId;
  /** Menu label. A file's nodes are a document, so two ids read differently. */
  label: (kind: RecordKind) => string;
  /** Icon name; each screen maps it to its own imported component. */
  icon: string;
  kinds: readonly RecordKind[];
  /** Destructive: rendered apart and in the danger tone. */
  danger?: boolean;
  /** May run over a selection rather than one record. */
  batch?: boolean;
  /**
   * Why this cannot run now, or "" when it can. A sentence, not a boolean: the
   * reason is the whole value of a disabled control, and it was previously
   * buried in a `title` that no touch device and no screen reader ever showed.
   */
  blocked: (caps: ActionCapabilities, record: SubscriptionListItem) => string;
}

const NEEDS_HOST = "The console has not finished handing this panel a session yet.";
const NEEDS_MUTATE =
  "This session cannot change records here. Either the installed bundle does not declare that method, or your token lacks the scope.";

function kindOf(record: SubscriptionListItem): RecordKind {
  return (record.kind as RecordKind) || KIND_SUB;
}

const ALL_KINDS = [KIND_SUB, KIND_COLLECTION, KIND_FILE] as const;
const NODE_KINDS = [KIND_SUB, KIND_COLLECTION] as const;

export const RECORD_ACTIONS: readonly ActionDeclaration[] = [
  {
    id: "edit",
    label: () => "Edit",
    icon: "pencil",
    kinds: ALL_KINDS,
    // The editor exists to change the record, and its Save is the capability
    // being tested. Opening it read-only is a product decision nobody has
    // taken, so this keeps what both screens already did.
    blocked: (caps) => (!caps.ready ? NEEDS_HOST : caps.mutate ? "" : NEEDS_MUTATE),
  },
  {
    id: "refresh",
    label: () => "Refresh",
    icon: "refresh",
    kinds: NODE_KINDS,
    // Refreshing reads the source again; it is gated on the probe method, not
    // on write access. Writing this down is what caught the two apart: the
    // draft of this registry had guessed `mutate`, and the screens had always
    // used `fetch`.
    blocked: (caps) => (!caps.ready ? NEEDS_HOST : caps.fetch ? "" : "The installed bundle does not declare a fetch method."),
  },
  {
    id: "output",
    label: (kind) => (kind === KIND_FILE ? "Show document" : "Client output…"),
    icon: "eye",
    kinds: ALL_KINDS,
    blocked: (caps) =>
      !caps.ready
        ? NEEDS_HOST
        : caps.render || caps.preview
          ? ""
          : "The installed bundle does not declare a render method.",
  },
  {
    id: "preview",
    label: () => "Preview nodes",
    icon: "eye",
    // A file is a document; its nodes are not the thing it serves.
    kinds: NODE_KINDS,
    blocked: (caps) =>
      !caps.ready ? NEEDS_HOST : caps.preview ? "" : "The installed bundle does not declare a preview method.",
  },
  {
    id: "share",
    label: () => "Share…",
    icon: "share",
    kinds: ALL_KINDS,
    blocked: (caps) => (caps.ready ? "" : NEEDS_HOST),
  },
  {
    id: "publish",
    label: () => "Publish…",
    icon: "send",
    kinds: ALL_KINDS,
    blocked: (caps) =>
      !caps.ready
        ? NEEDS_HOST
        : !caps.publish
          ? "The installed bundle does not declare a publish method."
          : caps.mutate
            ? ""
            : NEEDS_MUTATE,
  },
  {
    id: "duplicate",
    label: () => "Duplicate",
    icon: "copy",
    kinds: ALL_KINDS,
    blocked: (caps) => (!caps.ready ? NEEDS_HOST : caps.mutate ? "" : NEEDS_MUTATE),
  },
  {
    id: "delete",
    label: () => "Delete",
    icon: "trash",
    kinds: ALL_KINDS,
    danger: true,
    batch: true,
    blocked: (caps) => (!caps.ready ? NEEDS_HOST : caps.mutate ? "" : NEEDS_MUTATE),
  },
];

export interface ResolvedAction {
  id: ActionId;
  label: string;
  icon: string;
  danger: boolean;
  /** "" when the action can run; otherwise the sentence to show. */
  reason: string;
  disabled: boolean;
}

/** Every action this record offers, in menu order, each with its verdict. */
export function actionsFor(
  record: SubscriptionListItem,
  caps: ActionCapabilities,
  only?: readonly ActionId[],
): ResolvedAction[] {
  const kind = kindOf(record);
  return RECORD_ACTIONS.filter(
    (action) => action.kinds.includes(kind) && (!only || only.includes(action.id)),
  ).map((action) => {
    const reason = action.blocked(caps, record);
    return {
      id: action.id,
      label: action.label(kind),
      icon: action.icon,
      danger: action.danger === true,
      reason,
      disabled: reason !== "",
    };
  });
}

/** The actions that may run over a selection rather than one record. */
export function batchActionsFor(
  records: readonly SubscriptionListItem[],
  caps: ActionCapabilities,
): ResolvedAction[] {
  if (records.length === 0) return [];
  // yagni: judged for the set as a whole, because every batch action today
  // applies to every kind and no rule depends on the record itself. Ceiling:
  // the moment an action is batchable for some kinds only, or a rule starts
  // reading the record (a published record refusing deletion, say), this has
  // to filter by every record's kind and refuse the set if any record refuses.
  // Both were written that way first and deleted again: with one all-kinds,
  // caps-only action in the registry neither branch could be reached, so no
  // test could hold them up and they were decoration.
  const kind = kindOf(records[0]!);
  return RECORD_ACTIONS.filter((action) => action.batch).map((action) => {
    const reason = action.blocked(caps, records[0]!);
    return {
      id: action.id,
      label: action.label(kind),
      icon: action.icon,
      danger: action.danger === true,
      reason,
      disabled: reason !== "",
    };
  });
}
