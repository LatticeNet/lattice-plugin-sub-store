import type { SubscriptionListItem } from "./client";
import { KIND_COLLECTION, KIND_FILE, KIND_SUB } from "./client";
import { matchesQuery, normalizeQuery } from "./recordSearch";
import { actionsFor, type ActionCapabilities, type ActionId } from "./recordActions";

/**
 * What the palette shows, decided without a DOM.
 *
 * The palette is a third reader of two things this plugin already knows: which
 * records exist, and what may be done to them. It adds no rules of its own —
 * it matches with the list screens' predicate (recordSearch.ts) and asks the
 * registry what each record offers (recordActions.ts). If it had its own copy
 * of either, the palette would answer "can I delete this" differently from the
 * row menu next to it, which is the failure this whole layer exists to avoid.
 *
 * Two levels, not one: a flat list of 256 records times six actions is not a
 * list anybody reads. Level one finds the thing; level two says what to do
 * with it.
 */

export type PaletteTab = "subscriptions" | "files";

/** A record, or a screen-level command like "New file". */
export type PaletteEntryKind = "record" | "command";

export interface PaletteEntry {
  kind: PaletteEntryKind;
  /** Stable identity for keyed rendering and for tests. */
  key: string;
  label: string;
  /** Shown after the label, quietly: the id, or what the command does. */
  hint: string;
  /** Which screen owns it; selecting it switches there first. */
  tab: PaletteTab;
  /** Set for records, so level two can resolve the actions. */
  record?: SubscriptionListItem;
  /** Set for commands. */
  command?: PaletteCommandId;
  disabled: boolean;
  reason: string;
}

export type PaletteCommandId = "new-subscription" | "new-collection" | "new-file";

export interface PaletteCommand {
  id: PaletteCommandId;
  label: string;
  hint: string;
  tab: PaletteTab;
  blocked: (caps: ActionCapabilities) => string;
}

const NEEDS_MUTATE =
  "This session cannot create records here. Either the installed bundle does not declare that method, or your token lacks the scope.";

export const PALETTE_COMMANDS: readonly PaletteCommand[] = [
  {
    id: "new-subscription",
    label: "New subscription",
    hint: "One source of nodes",
    tab: "subscriptions",
    blocked: (caps) => (caps.mutate ? "" : NEEDS_MUTATE),
  },
  {
    id: "new-collection",
    label: "New combination",
    hint: "Several subscriptions served as one",
    tab: "subscriptions",
    blocked: (caps) => (caps.mutate ? "" : NEEDS_MUTATE),
  },
  {
    id: "new-file",
    label: "New file",
    hint: "A configuration served as it is",
    tab: "files",
    blocked: (caps) => (caps.mutate ? "" : NEEDS_MUTATE),
  },
];

function tabFor(record: SubscriptionListItem): PaletteTab {
  return record.kind === KIND_FILE ? "files" : "subscriptions";
}

function describe(record: SubscriptionListItem): string {
  if (record.kind === KIND_FILE) return "File";
  if (record.kind === KIND_COLLECTION) return "Combination";
  return "Subscription";
}

/**
 * Level one. Records first, because the palette is opened to reach a record far
 * more often than to create one; commands follow, and only those the query
 * actually names once something has been typed.
 */
export function paletteEntries(
  query: string,
  records: readonly SubscriptionListItem[],
  caps: ActionCapabilities,
  limit = 20,
): PaletteEntry[] {
  const q = normalizeQuery(query);
  const matched = records
    .filter((record) => matchesQuery(record, q))
    .map<PaletteEntry>((record) => ({
      kind: "record",
      key: "record:" + record.id,
      label: record.display_name?.trim() || record.name,
      hint: `${describe(record)} · ${record.id}`,
      tab: tabFor(record),
      record,
      disabled: false,
      reason: "",
    }));

  const commands = PALETTE_COMMANDS.filter(
    (command) => !q || command.label.toLowerCase().includes(q) || command.hint.toLowerCase().includes(q),
  ).map<PaletteEntry>((command) => {
    const reason = command.blocked(caps);
    return {
      kind: "command",
      key: "command:" + command.id,
      label: command.label,
      hint: command.hint,
      tab: command.tab,
      command: command.id,
      disabled: reason !== "",
      reason,
    };
  });

  return [...matched.slice(0, limit), ...commands];
}

export interface PaletteAction {
  id: ActionId;
  label: string;
  hint: string;
  danger: boolean;
  disabled: boolean;
  reason: string;
}

/**
 * Level two: what to do with the record just chosen. Straight from the
 * registry, including the reasons — a palette that silently omits the actions
 * this session cannot run teaches the operator that the feature is missing
 * rather than that their token is.
 */
export function paletteActionsFor(
  record: SubscriptionListItem,
  caps: ActionCapabilities,
  offered: readonly ActionId[],
): PaletteAction[] {
  return actionsFor(record, caps, offered).map((action) => ({
    id: action.id,
    label: action.label,
    hint: record.display_name?.trim() || record.name,
    danger: action.danger,
    disabled: action.disabled,
    reason: action.reason,
  }));
}

/** Where the arrow keys land next, skipping nothing and wrapping at both ends. */
export function moveSelection(current: number, delta: number, length: number): number {
  if (length === 0) return 0;
  return (current + delta + length) % length;
}

export const KINDS_ON_TAB: Record<PaletteTab, readonly string[]> = {
  subscriptions: [KIND_SUB, KIND_COLLECTION],
  files: [KIND_FILE],
};
