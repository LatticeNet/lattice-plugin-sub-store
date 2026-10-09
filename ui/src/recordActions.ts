import { BINDINGS, KIND_COLLECTION, KIND_FILE, KIND_SUB, type MethodBinding, type SubStoreShareRow, type SubscriptionListItem } from "./client";
import { t } from "./i18n";
import { shareStateOf } from "./shareState";

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

/**
 * What this session may do, read once from the handshake and the signed
 * manifest. The tables, the side panel, the record page and the palette all
 * gate on this one answer, so a verb cannot be enabled in one place and
 * refused in another.
 */
export function actionCapabilities(host: {
  init: { value: unknown };
  available: (binding: MethodBinding) => boolean;
}): ActionCapabilities {
  return {
    ready: !!host.init.value,
    mutate: host.available(BINDINGS.subSave) && host.available(BINDINGS.subDelete),
    fetch: host.available(BINDINGS.subProbe),
    preview: host.available(BINDINGS.subPreview),
    render: host.available(BINDINGS.subRender),
    publish: host.available(BINDINGS.subPublish),
  };
}

export interface ActionDeclaration {
  id: ActionId;
  /** Menu label. A file's nodes are a document, so two ids read differently. */
  label: (kind: RecordKind) => string;
  /**
   * What running it does, one sentence, shown as the control's title when
   * nothing blocks it. Only the blocked items used to carry a title, so
   * "Share…" beside "Publish…" had no way of saying which was which.
   */
  title: (kind: RecordKind) => string;
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

/** Read when a verdict is asked for, so the sentence is in the active locale. */
const needsHost = () => t.actions.needsHost;
const needsMutate = () => t.actions.needsMutate;

function kindOf(record: SubscriptionListItem): RecordKind {
  return (record.kind as RecordKind) || KIND_SUB;
}

const ALL_KINDS = [KIND_SUB, KIND_COLLECTION, KIND_FILE] as const;
const NODE_KINDS = [KIND_SUB, KIND_COLLECTION] as const;

export const RECORD_ACTIONS: readonly ActionDeclaration[] = [
  {
    id: "edit",
    label: () => t.actions.edit,
    title: () => t.actions.editTitle,
    icon: "pencil",
    kinds: ALL_KINDS,
    // The editor exists to change the record, and its Save is the capability
    // being tested. Opening it read-only is a product decision nobody has
    // taken, so this keeps what both screens already did.
    blocked: (caps) => (!caps.ready ? needsHost() : caps.mutate ? "" : needsMutate()),
  },
  {
    id: "refresh",
    label: () => t.actions.refresh,
    title: () => t.actions.refreshTitle,
    icon: "refresh",
    kinds: NODE_KINDS,
    // Refreshing reads the source again; it is gated on the probe method, not
    // on write access. Writing this down is what caught the two apart: the
    // draft of this registry had guessed `mutate`, and the screens had always
    // used `fetch`.
    blocked: (caps) => (!caps.ready ? needsHost() : caps.fetch ? "" : t.actions.noFetch),
  },
  {
    id: "output",
    label: (kind) => (kind === KIND_FILE ? t.actions.outputFile : t.actions.output),
    title: (kind) => (kind === KIND_FILE ? t.actions.outputFileTitle : t.actions.outputTitle),
    icon: "eye",
    kinds: ALL_KINDS,
    blocked: (caps) => (!caps.ready ? needsHost() : caps.render || caps.preview ? "" : t.actions.noRender),
  },
  {
    id: "preview",
    label: () => t.actions.preview,
    title: () => t.actions.previewTitle,
    icon: "eye",
    // A file is a document; its nodes are not the thing it serves.
    kinds: NODE_KINDS,
    blocked: (caps) => (!caps.ready ? needsHost() : caps.preview ? "" : t.actions.noPreview),
  },
  {
    // Named by its outcome. The console creates the share, so this frame can
    // only open the form there; what the operator gets is a published record,
    // which is the word the PUBLISHED column and the banner already use.
    id: "share",
    label: () => t.actions.share,
    title: () => t.actions.shareTitle,
    icon: "share",
    kinds: ALL_KINDS,
    blocked: (caps) => (caps.ready ? "" : needsHost()),
  },
  {
    // The server's `publish` renders the saved record and ships the document
    // to a destination the operator names (an upload, the way the official
    // front end syncs an artifact to a gist). It creates no share, so it is
    // not called Publish here: two menu items with that word, one of which
    // made the record reachable and one of which did not, was the vocabulary
    // bug.
    id: "publish",
    label: () => t.actions.publish,
    title: () => t.actions.publishTitle,
    icon: "upload",
    kinds: ALL_KINDS,
    blocked: (caps) => (!caps.ready ? needsHost() : !caps.publish ? t.actions.noPublish : caps.mutate ? "" : needsMutate()),
  },
  {
    id: "duplicate",
    label: () => t.actions.duplicate,
    title: () => t.actions.duplicateTitle,
    icon: "copy",
    kinds: ALL_KINDS,
    blocked: (caps) => (!caps.ready ? needsHost() : caps.mutate ? "" : needsMutate()),
  },
  {
    id: "delete",
    label: () => t.actions.delete,
    title: () => t.actions.deleteTitle,
    icon: "trash",
    kinds: ALL_KINDS,
    danger: true,
    batch: true,
    blocked: (caps) => (!caps.ready ? needsHost() : caps.mutate ? "" : needsMutate()),
  },
];

export interface ResolvedAction {
  id: ActionId;
  label: string;
  /** What running it does; the control's title when nothing blocks it. */
  title: string;
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
      title: action.title(kind),
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
      title: action.title(kind),
      icon: action.icon,
      danger: action.danger === true,
      reason,
      disabled: reason !== "",
    };
  });
}

/**
 * The row menu: the verbs that act on a record without opening it. The same
 * list wherever a record is shown (a table row, the side panel, the record
 * page), so a record opened from a pasted link can still be refreshed,
 * copied or deleted without going back to its table. The kind filters it: a
 * file has nothing to refresh.
 */
export const ROW_MENU_ACTIONS: readonly ActionId[] = ["output", "refresh", "duplicate", "delete"];
/**
 * A file's menu leads with Publish…, the verb a file exists for: the
 * Published column states whether it is published, and the menu is where
 * the operator acts on that. The ellipsis says it opens the console's form.
 */
export const FILE_ROW_MENU_ACTIONS: readonly ActionId[] = ["share", ...ROW_MENU_ACTIONS];

export function rowMenuFor(record: SubscriptionListItem, caps: ActionCapabilities): ResolvedAction[] {
  if (kindOf(record) !== KIND_FILE) return actionsFor(record, caps, ROW_MENU_ACTIONS);
  // The registry's order, with Publish… moved to the front.
  const actions = actionsFor(record, caps, FILE_ROW_MENU_ACTIONS);
  return [...actions.filter((action) => action.id === "share"), ...actions.filter((action) => action.id !== "share")];
}

export interface DeletePrompt {
  title: string;
  /** What is being deleted; the operator types their count to arm a batch. */
  names: string[];
  /** Records that break as a consequence. Listed, not counted. */
  consequences: string[];
  /**
   * Live shares the delete changes, by path: what breaks outside Lattice,
   * for clients that fetch them (design 23, 3.8). Empty when none does or
   * when the share list is unread.
   */
  served: string[];
  /** What the operator types to arm a one-record delete that changes a live share; "" when nothing needs typing. */
  confirmText: string;
}

/**
 * The live shares a delete reaches, each as one line. A share on a deleted
 * record, or on a file that loses its node source, stops serving. A share on
 * a file that draws from a combination losing a member keeps serving, with
 * fewer nodes. Downstream is followed to the end: a combination that loses a
 * member feeds its change to the files that draw from it.
 */
function servedBy(doomed: ReadonlySet<string>, items: readonly SubscriptionListItem[], shares: readonly SubStoreShareRow[], now: number): string[] {
  const byId = new Map(items.map((item) => [item.id, item]));
  const label = (id: string) => {
    const item = byId.get(id);
    return item ? item.display_name || item.name : id;
  };
  const broken = new Set(doomed);
  const changed = new Set<string>();
  for (let grew = true; grew; ) {
    grew = false;
    for (const item of items) {
      if (broken.has(item.id)) continue;
      const source = item.node_source ?? "";
      if (item.kind === KIND_FILE && source && broken.has(source)) {
        broken.add(item.id);
        grew = true;
      } else if (!changed.has(item.id) && ((source && changed.has(source)) || (item.members ?? []).some((member) => broken.has(member) || changed.has(member)))) {
        changed.add(item.id);
        grew = true;
      }
    }
  }
  const lines: string[] = [];
  for (const share of shares) {
    if (shareStateOf(share, now).state !== "live") continue;
    const id = share.subscription_id;
    // By slug, as the Published column names it: the path carries the
    // share's token, and a dialog is no place to print a credential.
    const path = `/${share.slug}`;
    if (doomed.has(id)) lines.push(t.deletion.servedDoomed(path, label(id)));
    else if (broken.has(id)) lines.push(t.deletion.servedBroken(path, label(id)));
    else if (changed.has(id)) lines.push(t.deletion.servedChanged(path, label(id)));
  }
  return lines;
}

/**
 * The confirm dialog's words for deleting `ids`, the same from every surface.
 *
 * The records that break are found rather than described: a combination
 * names its parts in `members`, and a file draws its nodes from
 * `node_source`, so the warning lists the actual names, and a record with no
 * dependents carries no warning about dependents. Files have none (nothing
 * may draw from a file), so a set of files gets the shorter sentence.
 *
 * With the share list read, the live shares the delete changes are named by
 * path, and a one-record delete that changes one asks for the record's name
 * to be typed: it breaks something outside Lattice. A batch already asks for
 * its count. Unread, the dialog says it cannot tell.
 */
export function deletePrompt(
  ids: readonly string[],
  items: readonly SubscriptionListItem[],
  shares?: readonly SubStoreShareRow[],
  now: number = Date.now(),
): DeletePrompt {
  const byId = new Map(items.map((item) => [item.id, item]));
  const label = (item: SubscriptionListItem) => item.display_name || item.name;
  const names = ids.map((id) => {
    const item = byId.get(id);
    return item ? label(item) : id;
  });
  const count = ids.length;
  const one = count === 1;
  const doomed = new Set(ids);
  const served = shares ? servedBy(doomed, items, shares, now) : [];
  const confirmText = one && served.length ? names[0]! : "";
  const sharesNote = !shares
    ? t.deletion.sharesUnread(count)
    : served.length
      ? t.deletion.sharesChange(served.length)
      : t.deletion.sharesNone(count);
  if (count > 0 && ids.every((id) => byId.get(id)?.kind === KIND_FILE)) {
    return { title: t.deletion.filesTitle(count, sharesNote), names, consequences: [], served, confirmText };
  }
  const consequences: string[] = [];
  for (const item of items) {
    if (doomed.has(item.id)) continue;
    if ((item.members ?? []).some((member) => doomed.has(member))) {
      consequences.push(t.deletion.consequenceMember(label(item)));
    } else if (item.node_source && doomed.has(item.node_source)) {
      consequences.push(t.deletion.consequenceSource(label(item)));
    }
  }
  if (!consequences.length) return { title: t.deletion.noDependents(count, sharesNote), names, consequences, served, confirmText };
  return { title: t.deletion.withDependents(count, consequences.length, sharesNote), names, consequences, served, confirmText };
}

/**
 * The live shares that publish `id` itself, as paths ("/cdcd"); undefined
 * when the share list is unread. These are the shares a delete leaves
 * serving nothing.
 */
export function ownLiveShares(id: string, shares: readonly SubStoreShareRow[] | undefined, now: number = Date.now()): string[] | undefined {
  if (!shares) return undefined;
  return shares.filter((share) => share.subscription_id === id && shareStateOf(share, now).state === "live").map((share) => `/${share.slug}`);
}

/**
 * What a finished delete says. A share is not removed with its record: it
 * stays in the console and serves nothing, so the notice names it and says
 * where to deal with it. With the share list unread it cannot name one and
 * says so; with none, it says only what was deleted.
 */
export function deletedNotice(label: string, ownShares: readonly string[] | undefined): string {
  if (!ownShares) return t.deletion.deletedUnread(label);
  if (!ownShares.length) return t.deletion.deleted(label);
  return t.deletion.deletedServing(label, ownShares.join(", "), ownShares.length);
}
