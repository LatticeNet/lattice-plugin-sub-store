/**
 * What changed under an edit, in the operator's words.
 *
 * The backend refuses a stale save and hands back the record as it now stands.
 * It cannot say what changed, because it has no memory of the copy the operator
 * opened; the UI does (`lastRead`). So the diff belongs here.
 *
 * The point is not completeness, it is a decision. An operator looking at
 * "someone else changed this" needs to know whether the other edit was the same
 * field they were editing (in which case one of the two has to lose) or an
 * unrelated one (in which case reopening and re-applying is cheap and safe).
 * A list of field names answers that; a JSON blob does not.
 */

import type { SubscriptionRecord } from "./client";

export interface FieldChange {
  /** The field, named the way the editor labels it rather than the way it is stored. */
  label: string;
  /** What it was when the operator opened it, rendered for reading. */
  before: string;
  /** What it is now. */
  after: string;
  /** True when the operator's own draft also touched this field. */
  contested: boolean;
}

/** Storage name to the label the editor puts on it. Order is the reading order. */
const FIELDS: Array<{ key: keyof SubscriptionRecord; label: string }> = [
  { key: "name", label: "Name" },
  { key: "display_name", label: "Display name" },
  { key: "remark", label: "Remark" },
  { key: "tags", label: "Tags" },
  { key: "url", label: "Provider URL" },
  { key: "content", label: "Content" },
  { key: "source", label: "Source" },
  { key: "vpn_identity", label: "VPN identity" },
  { key: "entry_roots", label: "Entry roots" },
  { key: "ua", label: "User agent" },
  { key: "target", label: "Client target" },
  { key: "members", label: "Members" },
  { key: "member_tags", label: "Member tags" },
  { key: "failure_mode", label: "Failure mode" },
  { key: "file_type", label: "File type" },
  { key: "node_source", label: "Node source" },
  { key: "download", label: "Download" },
  { key: "query_params", label: "Query parameters" },
  { key: "arguments", label: "Arguments" },
  { key: "process", label: "Operations" },
  { key: "script_digest", label: "Program" },
];

/** How long a value may be before it is summarised rather than printed. */
const MAX_VALUE = 60;

/**
 * Render one stored value for a human. Long text and structured values become a
 * shape rather than their content: the operator is deciding whose edit wins,
 * not reading a diff of a 40 KB config.
 */
export function describeValue(value: unknown): string {
  if (value === undefined || value === null || value === "") return "empty";
  if (typeof value === "boolean") return value ? "on" : "off";
  if (Array.isArray(value)) {
    if (value.length === 0) return "empty";
    if (value.every((item) => typeof item === "string")) {
      const joined = (value as string[]).join(", ");
      return joined.length <= MAX_VALUE ? joined : `${value.length} entries`;
    }
    return `${value.length} ${value.length === 1 ? "entry" : "entries"}`;
  }
  if (typeof value === "object") {
    const keys = Object.keys(value as Record<string, unknown>);
    return keys.length ? `${keys.length} ${keys.length === 1 ? "key" : "keys"}` : "empty";
  }
  const text = String(value);
  if (text.length <= MAX_VALUE) return text;
  return `${text.length} characters`;
}

/**
 * Whether a stored value is one the operator would read as "nothing set".
 *
 * The wire and the editor disagree about how to spell empty: the backend omits
 * an empty list entirely, a draft sends `[]`, and a cleared text field is `""`.
 * All three mean the same thing to the person reading the panel, and treating
 * them as different produced rows saying a field changed from "empty" to
 * "empty", which is exactly the kind of noise that teaches an operator to stop
 * reading the list.
 */
function isBlank(value: unknown): boolean {
  if (value === undefined || value === null || value === "") return true;
  if (Array.isArray(value)) return value.length === 0;
  if (typeof value === "object") return Object.keys(value as Record<string, unknown>).length === 0;
  return false;
}

/** Structural equality, deep enough for the shapes a record holds. */
function same(a: unknown, b: unknown): boolean {
  if (a === b) return true;
  const aBlank = isBlank(a);
  const bBlank = isBlank(b);
  if (aBlank && bBlank) return true;
  if (aBlank !== bBlank) return false;
  try {
    return JSON.stringify(a) === JSON.stringify(b);
  } catch {
    return false;
  }
}

/**
 * The fields that moved between the copy the operator opened and the copy in
 * storage now, marking the ones their own draft also touched.
 *
 * `mine` is optional because the draft is not always reconstructible as a
 * record (a create that raced a create, for instance). Without it nothing is
 * contested and the list is purely "what changed underneath you".
 */
export function conflictChanges(
  opened: SubscriptionRecord | null,
  current: SubscriptionRecord | null,
  mine?: SubscriptionRecord | null,
): FieldChange[] {
  if (!opened || !current) return [];
  const changes: FieldChange[] = [];
  for (const field of FIELDS) {
    const before = opened[field.key];
    const after = current[field.key];
    if (same(before, after)) continue;
    changes.push({
      label: field.label,
      before: describeValue(before),
      after: describeValue(after),
      // Contested only when the operator changed the same field away from what
      // they opened. Editing it back to its original value is not a contest.
      contested: mine ? !same(before, mine[field.key]) : false,
    });
  }
  return changes;
}

/**
 * One sentence for the top of the conflict panel. It has to be true when the
 * diff is empty, which happens when the change was in a field this list does
 * not name: saying "nothing changed" there would be a lie that invites the
 * operator to overwrite.
 */
export function conflictSummary(changes: FieldChange[]): string {
  const contested = changes.filter((change) => change.contested).length;
  if (!changes.length) {
    return "This record was changed while you had it open. The change is not in a field shown here, so compare before you decide.";
  }
  const changed = changes.length === 1 ? "1 field" : `${changes.length} fields`;
  if (!contested) {
    return `This record was changed while you had it open: ${changed}, none of them fields you edited. Reopening keeps both changes.`;
  }
  const clash = contested === 1 ? "1 of them is a field you also edited" : `${contested} of them are fields you also edited`;
  return `This record was changed while you had it open: ${changed}, and ${clash}. Saving anyway replaces their version with yours.`;
}
