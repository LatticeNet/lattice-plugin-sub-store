/**
 * planFlow.ts, the rules behind the staged state, the plans and the migration
 * of design 28 S2 (s2-plan sections 2.6, 2.7, 6 and 11), decided without a
 * DOM.
 *
 * A fleet-bound record's edit does not go live when it is saved: it waits in
 * staged-v2 under its own revision until core promotes it, through
 * plans.publish when no live share in the record's fleet closure names an
 * identity, or through a plan an operator approves otherwise. The Records
 * table, the Fleet view and the migration flow all reason over the same
 * facts: what the list says about a record's live and staged revisions, which
 * records read it, which shares serve it and whether they name an identity,
 * and what core said about a plan. Those rules live here once, so the three
 * surfaces cannot disagree and the rules are tested apart from the screens.
 */
import {
  KIND_COLLECTION,
  KIND_FILE,
  KIND_SUB,
  SOURCE_FLEET,
  SOURCE_VPN_CORE,
  SOURCE_VPN_CORE_GRAPH,
  type SubStoreShareRow,
  type SubscriptionListItem,
} from "./client";
import type { BindLine, BindPreviewReply, Plan, PlanLine, PlanLineChange, ShareRow } from "./core-types";
import { REASON_NOT_BOUND, planState, type PlanState, type PlanStatus } from "./fleet";

/**
 * A share as `shares.list` answers it. Core's row carries the share's
 * identity, display name and archive time (core-types ShareRow); the UI's
 * older row type predates them, so they are optional here.
 */
export type PlanShareRow = SubStoreShareRow & Partial<Pick<ShareRow, "identity_id" | "display_name" | "archived_at">>;

// ── what a record is ─────────────────────────────────────────────────────────

export function isLegacySource(source: string | undefined): boolean {
  return source === SOURCE_VPN_CORE || source === SOURCE_VPN_CORE_GRAPH;
}

/** A legacy vpn-core or vpn-core-graph source record: the one kind migrate_record converts. */
export function isLegacyRecord(item: Pick<SubscriptionListItem, "kind" | "source">): boolean {
  return (item.kind || KIND_SUB) === KIND_SUB && isLegacySource(item.source);
}

/**
 * Whether the record is fleet-bound live: its own source is the fleet, or the
 * index's closure says a live member is. A staged-only record (a new fleet
 * record waiting for its first publish) is not: it serves no one yet.
 */
export function isFleetBound(item: Pick<SubscriptionListItem, "flags" | "source" | "revision">): boolean {
  if (item.flags?.fleet_bound === true) return true;
  return item.source === SOURCE_FLEET && !!item.revision;
}

/** Whether the record still serves line owner credentials (the index's live flag, or a legacy source). */
export function servesOwnerCredentials(item: Pick<SubscriptionListItem, "flags" | "kind" | "source">): boolean {
  return item.flags?.owner_credentials === true || isLegacyRecord(item);
}

export function recordName(item: Pick<SubscriptionListItem, "display_name" | "name" | "id"> | undefined, id = ""): string {
  if (!item) return id;
  return item.display_name || item.name || item.id;
}

// ── staged revisions ─────────────────────────────────────────────────────────

export interface StagedFacts {
  staged: boolean;
  /** The staged revision, "" when nothing is staged. */
  revision: string;
  /** The live revision, "" for a record that was never live. */
  live: string;
  /** A staged new record, import or restore: nothing of it is live yet. */
  neverLive: boolean;
  /** migrate_record wrote the staged revision. */
  migrated: boolean;
  /** restore wrote it: an archived fleet-bound record waiting to come back. */
  restored: boolean;
  at: string;
}

export function stagedFacts(item: SubscriptionListItem): StagedFacts {
  const revision = item.staged_revision ?? "";
  const live = item.revision ?? "";
  return {
    staged: revision !== "",
    revision,
    live,
    neverLive: revision !== "" && live === "",
    migrated: item.staged_migrated === true,
    restored: !!item.staged_restored_at,
    at: item.staged_at ?? "",
  };
}

/** A revision as the tables print it: its first characters and an ellipsis, whole when short. */
export function shortRevision(revision: string | undefined, length = 10): string {
  const value = (revision ?? "").trim();
  if (value.length <= length + 2) return value;
  return `${value.slice(0, length)}…`;
}

// ── who reads a record ───────────────────────────────────────────────────────

function membersOf(item: SubscriptionListItem): string[] {
  return [...new Set([...(item.members ?? []), ...(item.staged_members ?? [])])];
}

function memberTagsOf(item: SubscriptionListItem): string[] {
  return [...new Set([...(item.member_tags ?? []), ...(item.staged_member_tags ?? [])])];
}

function tagsOf(item: SubscriptionListItem): string[] {
  return [...new Set([...(item.tags ?? []), ...(item.staged_tags ?? [])])];
}

function isCollection(item: SubscriptionListItem): boolean {
  return item.kind === KIND_COLLECTION || item.staged_kind === KIND_COLLECTION;
}

/** Whether `collection` gathers `member`, by id or by one of its tags, live or staged. */
export function gathers(collection: SubscriptionListItem, member: SubscriptionListItem): boolean {
  if (!isCollection(collection) || collection.id === member.id) return false;
  if (membersOf(collection).includes(member.id)) return true;
  const tags = new Set(tagsOf(member));
  return memberTagsOf(collection).some((tag) => tags.has(tag));
}

/**
 * The record and every collection that reads it, transitively, over live and
 * staged edges: what core's closure of a plan over the record covers (files
 * cannot be fleet-bound in S2, so they never join). The record comes first.
 */
export function closureOf(id: string, items: readonly SubscriptionListItem[]): string[] {
  const out = [id];
  for (let at = 0; at < out.length; at += 1) {
    const current = items.find((item) => item.id === out[at]);
    if (!current) continue;
    for (const candidate of items) {
      if (!out.includes(candidate.id) && gathers(candidate, current)) out.push(candidate.id);
    }
  }
  return out;
}

/**
 * The fleet-bound collections that gather the record directly, by id or by
 * a tag, live or staged (the plugin's fleetGatherers, S2-SR-1). A write to
 * such a record changes what those collections' identity holders receive,
 * so the plugin stages it and refuses its plain delete
 * (fleet_delete_requires_plan), although the record's own flags say it is
 * not fleet-bound. The list's fleet_bound flag is live state, so a
 * collection that becomes fleet-bound only through a staged revision is not
 * named here; the plugin's reply still says staged, and the surfaces read it.
 */
export function fleetGatherers(item: SubscriptionListItem, items: readonly SubscriptionListItem[]): SubscriptionListItem[] {
  return items.filter((candidate) => candidate.id !== item.id && isFleetBound(candidate) && gathers(candidate, item));
}

/** Whether a record's delete goes through a plan: it is fleet-bound, or a fleet-bound collection gathers it. */
export function deleteNeedsPlan(item: SubscriptionListItem, items: readonly SubscriptionListItem[]): boolean {
  return isFleetBound(item) || fleetGatherers(item, items).length > 0;
}

/** Collections that name `id` in their members, live or staged: a delete plan refuses while any does. */
export function explicitDependents(id: string, items: readonly SubscriptionListItem[]): SubscriptionListItem[] {
  return items.filter((item) => item.id !== id && isCollection(item) && membersOf(item).includes(id));
}

/** Collections that gather `id` by a tag only: they lose the member when it is deleted, and need no step. */
export function tagDependents(id: string, items: readonly SubscriptionListItem[]): SubscriptionListItem[] {
  const record = items.find((item) => item.id === id);
  if (!record) return [];
  return items.filter((item) => item.id !== id && gathers(item, record) && !membersOf(item).includes(id));
}

/**
 * Files whose node source is the record or a collection that reads it. A
 * fleet-bound file cannot exist before S3, so migrate_record refuses a record
 * any of these read (fleet_file_unavailable).
 */
export function fileDependents(id: string, items: readonly SubscriptionListItem[]): SubscriptionListItem[] {
  const readers = new Set(closureOf(id, items));
  return items.filter((item) => item.kind === KIND_FILE && readers.has((item.node_source ?? "").trim()));
}

/** A collection's live members that still serve owner credentials. */
export function legacyMembers(collection: SubscriptionListItem, items: readonly SubscriptionListItem[]): SubscriptionListItem[] {
  if (collection.kind !== KIND_COLLECTION) return [];
  return items.filter((item) => {
    if (!isLegacyRecord(item)) return false;
    if ((collection.members ?? []).includes(item.id)) return true;
    const tags = new Set(item.tags ?? []);
    return (collection.member_tags ?? []).some((tag) => tags.has(tag));
  });
}

/**
 * A collection that reads a fleet member and a legacy member at once, which
 * is what one promotion of a multi-record migration leaves: core refuses to
 * serve it (fleet_mixed_owner_credentials) until the legacy members migrate.
 * Their names, or null when the collection is not in that state.
 */
export function mixedBlock(item: SubscriptionListItem, items: readonly SubscriptionListItem[]): SubscriptionListItem[] | null {
  if (item.kind !== KIND_COLLECTION || item.flags?.fleet_bound !== true || item.flags?.owner_credentials !== true) return null;
  return legacyMembers(item, items);
}

// ── shares ───────────────────────────────────────────────────────────────────

/** Unarchived shares on any of the records, in the order the records are given. */
export function liveSharesOn(ids: readonly string[], shares: readonly PlanShareRow[] | undefined): PlanShareRow[] {
  if (!shares) return [];
  const out: PlanShareRow[] = [];
  for (const id of ids) out.push(...shares.filter((share) => share.subscription_id === id && !share.archived_at));
  return out;
}

/**
 * Shares that answer the decoy now: unarchived shares naming no identity on a
 * fleet-bound record. Core serves a fleet-bound record only through an
 * identity, so such a share gives its holder an empty document; the fix is an
 * assignment-only plan (s2-plan section 2.6).
 */
export function decoyShares(item: SubscriptionListItem, shares: readonly PlanShareRow[] | undefined): PlanShareRow[] {
  if (!isFleetBound(item)) return [];
  return liveSharesOn([item.id], shares).filter((share) => !share.identity_id);
}

/**
 * Shares that name an identity on a record that is not fleet-bound. Core
 * answers such a share with the decoy (section 2.1, decision 11): the
 * identity buys its holder nothing, and clearing it restores the document.
 */
export function darkenedShares(item: SubscriptionListItem, shares: readonly PlanShareRow[] | undefined): PlanShareRow[] {
  if (isFleetBound(item)) return [];
  return liveSharesOn([item.id], shares).filter((share) => !!share.identity_id);
}

export function shareLabel(share: Pick<PlanShareRow, "display_name" | "slug" | "share_id">): string {
  return share.display_name || `/${share.slug || share.share_id}`;
}

// ── migration ────────────────────────────────────────────────────────────────

/** What migrate_record answers (s2-plan section 2.7). */
export interface MigrateRecordReply {
  subscription_id: string;
  live_revision: string;
  staged_revision: string;
  selection: { catalogue_version?: string; count: number; line_uuids?: string[]; every_bound_line?: boolean };
  legacy: { source: string; vpn_identity?: string; entry_roots?: string[]; graph_options_version?: string; migrated_at?: string };
  dependents: { collections: string[]; co_migrate: string[] };
  warnings: string[];
}

/**
 * The identity a migrated share starts with. A graph record composed for one
 * VpnUser, so its shares default to that identity when vpn-core still holds
 * it. An export record has none: an empty identity meant every identity, and
 * a non-empty one is a ProxyUser id, not a VpnUser (s2-plan section 6), so
 * the operator picks.
 */
export function defaultIdentity(
  legacy: Pick<MigrateRecordReply["legacy"], "source" | "vpn_identity"> | undefined,
  identities: readonly { id: string }[],
): string {
  if (!legacy || legacy.source !== SOURCE_VPN_CORE_GRAPH) return "";
  const wanted = (legacy.vpn_identity ?? "").trim();
  return wanted && identities.some((identity) => identity.id === wanted) ? wanted : "";
}

/**
 * Whether an identity holds none of the selection's lines: core would serve
 * it no fleet entry, and at least one selected line is excluded only because
 * it is not bound to the identity. Approving a plan that gives a share this
 * identity turns the share into the decoy.
 */
export function holdsNoLine(preview: Pick<BindPreviewReply, "entries" | "excluded"> | null | undefined): boolean {
  if (!preview) return false;
  const fleet = preview.entries.filter((entry) => !entry.provider);
  return fleet.length === 0 && preview.excluded.some((entry) => entry.reason === REASON_NOT_BOUND);
}

/**
 * The selection's lines an identity still needs a binding plan on: excluded
 * as not bound, or bound with the credential not yet applied. What
 * bind_selection files approvals for.
 */
export function linesToBind(preview: Pick<BindPreviewReply, "excluded"> | null | undefined): string[] {
  if (!preview) return [];
  const wanted = new Set([REASON_NOT_BOUND, "credential_not_applied"]);
  return [...new Set(preview.excluded.filter((entry) => wanted.has(entry.reason) && entry.line_uuid).map((entry) => entry.line_uuid!))];
}

const NAME_STEPS = new Set(["Regex Filter"]);

/** The longest pattern and label the name check reads; longer ones are not judged here. */
export const NAME_CHECK_LIMIT = { pattern: 256, label: 128, labels: 4096 } as const;

/**
 * A pattern the browser's backtracking engine could take exponential time
 * on: a quantified group that holds a quantifier, such as `(a+)+` or
 * `(\w*)*`. The plugin's engine is linear and runs it fine; this tab skips
 * judging it rather than stall.
 */
function nestedQuantifier(pattern: string): boolean {
  return /\((?:[^()\\]|\\.)*[+*}](?:[^()\\]|\\.)*\)\s*(?:[+*]|\{\d+,?\d*\})/.test(pattern);
}

/**
 * The carried-over name filters that keep no line under the fleet labels. An
 * export record's migrate_record reads no catalogue page, so the check runs
 * here over bind.preview's labels (s2-plan section 6). Only a keep-mode
 * Regex Filter is judged: its patterns are what name a line, and a rename or
 * a sort cannot drop one. 1-based step positions.
 *
 * The answer is advisory: the browser's RegExp is not the plugin's engine,
 * so a pattern either accepts differently can be judged wrong either way.
 * A pattern past NAME_CHECK_LIMIT.pattern, or one with a nested quantifier,
 * is skipped, and labels are cut to NAME_CHECK_LIMIT.label, so a hostile
 * pattern cannot stall the tab.
 */
export function unmatchedNameSteps(process: readonly unknown[], labels: readonly string[]): number[] {
  if (!labels.length) return [];
  const read = labels.slice(0, NAME_CHECK_LIMIT.labels).map((label) => label.slice(0, NAME_CHECK_LIMIT.label));
  const out: number[] = [];
  process.forEach((raw, index) => {
    const step = raw as { type?: string; disabled?: boolean; args?: { regex?: unknown; value?: unknown; keep?: unknown } } | null;
    if (!step || step.disabled || !NAME_STEPS.has(step.type ?? "")) return;
    const patterns = [step.args?.regex, step.args?.value].flat().filter((value): value is string => typeof value === "string" && value !== "");
    if (!patterns.length || step.args?.keep === false) return;
    // A step with any pattern this tab will not judge is left to the plugin.
    if (patterns.some((pattern) => pattern.length > NAME_CHECK_LIMIT.pattern || nestedQuantifier(pattern))) return;
    const compiled = patterns.flatMap((pattern) => {
      try {
        return [new RegExp(pattern)];
      } catch {
        return [];
      }
    });
    if (compiled.length !== patterns.length) return;
    if (!read.some((label) => compiled.some((pattern) => pattern.test(label)))) out.push(index + 1);
  });
  return out;
}

/**
 * The co-migrated siblings that have no staged revision yet: one whose
 * migrate_record failed after the record itself staged. The plan promotes
 * every member together, so nothing moves on while any is missing.
 */
export function unstagedSiblings(siblings: readonly string[], stagedRevisionOf: (id: string) => string): string[] {
  return siblings.filter((id) => !stagedRevisionOf(id));
}

/**
 * The collections core names in its fleet_delete_has_explicit_dependents
 * refusal: "collections A, B list record X as a member, ..."
 * (lattice-server substore_svc_plans.go). Core carries no structured field
 * for them, so the sentence is read here, and planFlow.test.ts pins it: a
 * rewording fails that test rather than losing the names.
 */
export function explicitCollectionsFrom(message: string): string[] {
  const named = /collections (.+?) list record /.exec(message)?.[1] ?? "";
  return named
    .split(",")
    .map((id) => id.trim())
    .filter(Boolean);
}

/** One share's decision in a migration or an assignment: an identity, a clearing, or an archival. */
export interface ShareDecision {
  share_id: string;
  /** The identity to give the share; "" with `clear` set removes the one it has. */
  identity: string;
  archive: boolean;
  clear?: boolean;
}

/** Shares left with neither an identity nor an archival: core refuses such a migration (shares_without_identity). */
export function undecidedShares(shares: readonly PlanShareRow[], decisions: Readonly<Record<string, ShareDecision>>): PlanShareRow[] {
  return shares.filter((share) => {
    if (share.identity_id) return false;
    const decision = decisions[share.share_id];
    return !decision || (!decision.archive && !decision.identity);
  });
}

/**
 * The propose request's share fields (s2-plan section 2.6): identities for
 * the shares that name none and are not archived, "" for a clearing, and the
 * archivals. A share whose identity already holds is left out: a move from
 * one identity to another is refused (identity_change_refused).
 */
export function shareFields(
  shares: readonly PlanShareRow[],
  decisions: Readonly<Record<string, ShareDecision>>,
): { share_identities?: Record<string, string>; archive_shares?: string[] } {
  const identities: Record<string, string> = {};
  const archive: string[] = [];
  for (const share of shares) {
    const decision = decisions[share.share_id];
    if (!decision) continue;
    if (decision.archive) {
      archive.push(share.share_id);
      continue;
    }
    if (decision.clear && share.identity_id) identities[share.share_id] = "";
    else if (decision.identity && !share.identity_id) identities[share.share_id] = decision.identity;
  }
  return {
    ...(Object.keys(identities).length ? { share_identities: identities } : {}),
    ...(archive.length ? { archive_shares: archive } : {}),
  };
}

// ── plans ────────────────────────────────────────────────────────────────────

/** What plans.propose answers (core-types PlansProposeReply). */
export interface ProposeReply {
  required: boolean;
  reason?: string;
  approval_id?: string;
  plan_sha256?: string;
  plan?: string;
  status?: string;
}

/** What plans.publish answers (lattice-server subStorePlansPublishReply). */
export interface PublishReply {
  published: boolean;
  subscription_id: string;
  revision: string;
  action: string;
}

/** The approval's plan text, or null when it is not the plan core writes. */
export function parsePlan(text: string | undefined): Plan | null {
  if (!text) return null;
  try {
    const value = JSON.parse(text) as Plan;
    return value && typeof value === "object" && typeof value.subscription_id === "string" ? value : null;
  } catch {
    return null;
  }
}

/**
 * "the plan is N bytes, over M": core refuses a plan text past 1 MiB
 * (substore_svc_plans.go). The figures, or null for any other refusal.
 */
export function planTooLarge(message: string): { bytes: number; limit: number } | null {
  const found = /the plan is (\d+) bytes, over (\d+)/.exec(message);
  return found ? { bytes: Number(found[1]), limit: Number(found[2]) } : null;
}

/** Whether core's reason says an apply failed after it may have promoted the record. */
export function mayHavePromoted(reason: string | undefined): boolean {
  return /may have been promoted/.test(reason ?? "");
}

/**
 * A multi-record apply that stopped part way: the record it failed at and
 * the records promoted before it, from the approval's reason ("apply_revision
 * failed at record C; records applied before it: A, B, with their shares'
 * changes written"). Null for any other reason.
 */
export function partialApply(reason: string | undefined): { failed: string; applied: string[] } | null {
  const found = /apply_revision failed at record (\S+?);\s*records applied before it: (.+?), with their shares/.exec(reason ?? "");
  if (!found) return null;
  const applied = found[2]!.trim() === "none" ? [] : found[2]!.split(",").map((id) => id.trim()).filter(Boolean);
  return { failed: found[1]!, applied };
}

/**
 * The plan a record waits on, from what plans.status lists for it (core
 * lists the plans whose closure holds the record, newest first): the newest
 * plan over the record's staged revision, else the newest one still pending
 * or whose apply failed, which a multi-record plan over a sibling can be.
 * Undefined when nothing was proposed for it.
 */
export function currentPlan(plans: readonly PlanStatus[], item: SubscriptionListItem): PlanStatus | undefined {
  const staged = item.staged_revision ?? "";
  if (staged) {
    const over = plans.find((plan) => plan.subscription_id === item.id && plan.to_revision === staged);
    if (over) return over;
  }
  return plans.find((plan) => {
    const state = planState(plan);
    return state === "pending" || state === "failed";
  });
}

/**
 * A delete plan the record's row must say before anything else: the newest
 * plan whose primary is the record is a delete, still pending, gone stale or
 * failed. It wins over a staged revision and its promote plan, since the
 * delete drops the staged revision with the record: a record about to be
 * archived must not read as one about to be promoted (the second lane 6b
 * design review). A delete the approver rejected is over, and says nothing.
 */
export function pendingDelete(plans: readonly PlanStatus[] | undefined, item: SubscriptionListItem): PlanStatus | undefined {
  const newest = plans?.find((plan) => plan.subscription_id === item.id);
  if (!newest || newest.action !== "delete") return undefined;
  const state = planState(newest);
  return state === "pending" || state === "stale" || state === "failed" ? newest : undefined;
}

/**
 * The plan the row shows in place of a staged revision: a delete plan
 * (pendingDelete), whether or not anything is staged; else, for a record
 * with nothing staged, a change to its shares' identities still waiting or
 * failed. Nothing else on the record's row says it is about to change, so
 * the table shows it where it would show a staged revision. Only a plan
 * whose primary is the record: a combination in another record's closure is
 * said by that record.
 */
export function rowPlan(plans: readonly PlanStatus[] | undefined, item: SubscriptionListItem): PlanStatus | undefined {
  if (!plans) return undefined;
  const deleting = pendingDelete(plans, item);
  if (deleting || item.staged_revision) return deleting;
  const plan = currentPlan(plans, item);
  return plan && plan.subscription_id === item.id ? plan : undefined;
}

/** What the staged badge says about a record's plan: none proposed, or the plan's state. */
export function stagedPlanState(plans: readonly PlanStatus[] | undefined, item: SubscriptionListItem): PlanState | "none" | "unknown" {
  if (!plans) return "unknown";
  const plan = currentPlan(plans, item);
  return plan ? planState(plan) : "none";
}

/**
 * Whether a failed plan's record ended at the revision the plan promoted,
 * read from the record's live revision in `list` (s2-plan section 2.6: "list
 * shows the outcome"). Null when the list does not say.
 */
export function promotedAfterFailure(plan: Pick<PlanStatus, "from_revision" | "to_revision">, live: string | undefined): boolean | null {
  if (live === undefined) return null;
  if (plan.to_revision && live === plan.to_revision) return true;
  if (live === plan.from_revision) return false;
  return null;
}

/** One share's lines in a plan, for the plan view: what its identity gains, loses and is denied. */
export interface PlanShareView {
  record: string;
  share_id: string;
  slug: string;
  display_name?: string;
  identity_id: string;
  /** The plan gives the share its identity. */
  assigned: boolean;
  /** The plan archives the share. */
  archive: boolean;
  added: PlanLine[];
  removed: PlanLine[];
  changed: PlanLineChange[];
  excluded: BindLine[];
  unchanged: number;
}

/**
 * Every share the plan names, per record section, with its identity's lines.
 * A plan with no `records` (a single-record plan from a core before S2's
 * sections) reads its top-level fields as the one section.
 */
export function planShareViews(plan: Plan): PlanShareView[] {
  const sections = plan.records?.length
    ? plan.records
    : [{ subscription_id: plan.subscription_id, shares: plan.shares, identities: plan.identities }];
  const out: PlanShareView[] = [];
  for (const section of sections) {
    const identities = new Map((section.identities ?? []).map((identity) => [identity.identity_id, identity]));
    for (const share of section.shares ?? []) {
      const identity = identities.get(share.identity_id);
      out.push({
        record: section.subscription_id,
        share_id: share.share_id,
        slug: share.slug,
        display_name: share.display_name,
        identity_id: share.identity_id,
        assigned: share.assigned === true,
        archive: share.archive === true,
        added: identity?.added ?? [],
        removed: identity?.removed ?? [],
        changed: identity?.changed ?? [],
        excluded: identity?.excluded ?? [],
        unchanged: identity?.unchanged ?? 0,
      });
    }
  }
  return out;
}

/** The shares a plan leaves without an identity, by record section. */
export function planUnboundShares(plan: Plan): { record: string; share_id: string; slug: string; display_name?: string; archive: boolean }[] {
  const sections = plan.records?.length ? plan.records : [{ subscription_id: plan.subscription_id, unbound_shares: plan.unbound_shares }];
  return sections.flatMap((section) =>
    (section.unbound_shares ?? []).map((share) => ({
      record: section.subscription_id,
      share_id: share.share_id,
      slug: share.slug,
      display_name: share.display_name,
      archive: share.archive === true,
    })),
  );
}

/**
 * The records a plan promotes or deletes, each with the revisions it moves
 * between, in the plan's section order: every section core marks as the
 * record itself, promoted or deleted, never a dependent collection or a
 * staged-only one. A plan text without sections names its primary alone.
 */
export function planRevisionLines(plan: Plan): { id: string; from: string; to: string }[] {
  const sections = (plan.records ?? []).filter((section) => section.role !== "dependent" && section.role !== "staged_only");
  if (!sections.length) return [{ id: plan.subscription_id, from: plan.from_revision, to: plan.to_revision ?? "" }];
  return sections.map((section) => ({ id: section.subscription_id, from: section.from_revision ?? "", to: section.to_revision ?? "" }));
}

// ── a plan's process chains (S2-SR-2) ──────────────────────────────────────

/**
 * One line of a section's chain changes, read back into its parts so each
 * locale can say it. Core writes them in English (lattice-server
 * substore_svc_plans_chain.go): "step 2 added: Script Operator", "step 3
 * changed: Script Operator (was step 2, now enabled)", "the steps changed: N
 * before, M after, too many to list one by one", "and N more step changes".
 * A line in any other shape is kept as core wrote it.
 */
export type ChainChange =
  | { kind: "step"; verb: "added" | "removed" | "changed"; position: number; type: string; notes: ChainNote[] }
  | { kind: "many"; before: number; after: number }
  | { kind: "more"; count: number }
  | { kind: "text"; text: string };

export type ChainNote = { kind: "disabled" } | { kind: "now-enabled" } | { kind: "now-disabled" } | { kind: "moved"; from: number };

function chainNote(text: string): ChainNote | null {
  if (text === "disabled") return { kind: "disabled" };
  if (text === "now enabled") return { kind: "now-enabled" };
  if (text === "now disabled") return { kind: "now-disabled" };
  const moved = /^was step (\d+)$/.exec(text);
  return moved ? { kind: "moved", from: Number(moved[1]) } : null;
}

export function parseChainChange(line: string): ChainChange {
  const text = line.trim();
  // A step type is ASCII letters, digits and single spaces (core cleans it), so it never holds a parenthesis.
  const step = /^step (\d+) (added|removed|changed): ([^()]+?)(?: \(([^()]*)\))?$/.exec(text);
  if (step) {
    const notes = (step[4] ?? "").split(",").map((note) => note.trim()).filter(Boolean).map(chainNote);
    if (notes.every((note): note is ChainNote => note !== null)) {
      return { kind: "step", verb: step[2] as "added" | "removed" | "changed", position: Number(step[1]), type: step[3]!, notes };
    }
  }
  const many = /^the steps changed: (\d+) before, (\d+) after, too many to list one by one$/.exec(text);
  if (many) return { kind: "many", before: Number(many[1]), after: Number(many[2]) };
  const more = /^and (\d+) more step changes$/.exec(text);
  if (more) return { kind: "more", count: Number(more[1]) };
  return { kind: "text", text };
}

/** A section's chain changes, as the plan view lists them. */
export interface PlanChainSection {
  record: string;
  role: string;
  changes: ChainChange[];
}

/**
 * The sections whose process chain changes, in the plan's section order,
 * each with its parsed lines. A plan text without sections reads its
 * top-level chain as the primary's.
 */
export function planChainSections(plan: Plan): PlanChainSection[] {
  const sections = plan.records?.length
    ? plan.records.map((section) => ({ record: section.subscription_id, role: section.role, lines: section.chain?.changes ?? [] }))
    : [{ record: plan.subscription_id, role: plan.role ?? "self", lines: plan.chain?.changes ?? [] }];
  return sections.filter((section) => section.lines.length > 0).map((section) => ({ record: section.record, role: section.role, changes: section.lines.map(parseChainChange) }));
}

/**
 * Whether the plan changes process steps and no identity's lines: every
 * diff a share shows is empty, and the approver has only the step lines to
 * go by (S2-SR-2: a pass-through Script Operator moves no line).
 */
export function chainOnlyPlan(plan: Plan): boolean {
  if (!planChainSections(plan).length) return false;
  const totals = plan.totals;
  return !totals || (totals.added === 0 && totals.removed === 0 && totals.changed === 0);
}

/** A record's process steps as core reads them: `process`, or the operators of a record written before it. */
export function storedSteps(record: { process?: unknown; operators?: unknown } | null | undefined): unknown[] {
  if (!record) return [];
  if (Array.isArray(record.process) && record.process.length) return record.process;
  return Array.isArray(record.operators) ? record.operators : [];
}

interface ChainStepFacts {
  digest: string;
  type: string;
  disabled: boolean;
}

/** A step type as core prints it: ASCII letters and digits, single spaces, at most 48 bytes. */
function cleanStepType(value: string): string {
  const out = value
    .replace(/[^A-Za-z0-9\s]/g, "")
    .trim()
    .replace(/\s+/g, " ")
    .slice(0, 48)
    .trim();
  return out || "step with no type";
}

function stepFacts(step: unknown): ChainStepFacts {
  const digest = JSON.stringify(step) ?? "null";
  const value = step as { type?: unknown; disabled?: unknown } | null;
  const readable =
    !!value &&
    typeof value === "object" &&
    !Array.isArray(value) &&
    (value.type === undefined || typeof value.type === "string") &&
    (value.disabled === undefined || typeof value.disabled === "boolean");
  if (!readable) return { digest, type: "unreadable step", disabled: false };
  return { digest, type: cleanStepType((value.type as string | undefined) ?? ""), disabled: value.disabled === true };
}

/** Walks two sequences along a longest common subsequence under `equal`. */
function walkCommon(n: number, m: number, equal: (i: number, j: number) => boolean, match: (i: number, j: number) => void, onlyA: (i: number) => void, onlyB: (j: number) => void): void {
  const lcs = Array.from({ length: n + 1 }, () => new Array<number>(m + 1).fill(0));
  for (let i = n - 1; i >= 0; i -= 1) {
    for (let j = m - 1; j >= 0; j -= 1) lcs[i]![j] = equal(i, j) ? lcs[i + 1]![j + 1]! + 1 : Math.max(lcs[i + 1]![j]!, lcs[i]![j + 1]!);
  }
  let i = 0;
  let j = 0;
  while (i < n && j < m) {
    if (equal(i, j)) {
      match(i, j);
      i += 1;
      j += 1;
    } else if (lcs[i + 1]![j]! >= lcs[i]![j + 1]!) {
      onlyA(i);
      i += 1;
    } else {
      onlyB(j);
      j += 1;
    }
  }
  for (; i < n; i += 1) onlyA(i);
  for (; j < m; j += 1) onlyB(j);
}

const CHAIN_DIFF_STEPS = 256;
const CHAIN_LINES = 128;

/**
 * The steps added, removed and changed from one chain to another, by the
 * rule core lists them in a plan (subStorePlanChainChanges): steps matched
 * by content along the longest common subsequence are unchanged; between two
 * matches the rest are matched by type, a pair being a step changed in
 * place, and what is left was removed or added. The propose step reads the
 * live and staged documents to say this before core answers; the plan core
 * files carries its own lines, which the plan view shows.
 */
export function chainChanges(before: readonly unknown[], after: readonly unknown[]): ChainChange[] {
  const a = before.map(stepFacts);
  const b = after.map(stepFacts);
  if (a.length > CHAIN_DIFF_STEPS || b.length > CHAIN_DIFF_STEPS) {
    const same = a.length === b.length && a.every((step, index) => step.digest === b[index]!.digest);
    return same ? [] : [{ kind: "many", before: a.length, after: b.length }];
  }
  const out: ChainChange[] = [];
  const describe = (verb: "added" | "removed" | "changed", position: number, step: ChainStepFacts, notes: ChainNote[] = []) => {
    out.push({ kind: "step", verb, position, type: step.type, notes: step.disabled && !notes.length ? [{ kind: "disabled" }] : notes });
  };
  const changed = (i: number, j: number) => {
    const from = a[i]!;
    const to = b[j]!;
    const notes: ChainNote[] = [];
    if (i !== j) notes.push({ kind: "moved", from: i + 1 });
    if (from.disabled && !to.disabled) notes.push({ kind: "now-enabled" });
    else if (!from.disabled && to.disabled) notes.push({ kind: "now-disabled" });
    else if (to.disabled) notes.push({ kind: "disabled" });
    describe("changed", j + 1, to, notes);
  };
  const run = (fromI: number, fromJ: number, toI: number, toJ: number) =>
    walkCommon(
      toI - fromI,
      toJ - fromJ,
      (i, j) => a[fromI + i]!.type === b[fromJ + j]!.type,
      (i, j) => changed(fromI + i, fromJ + j),
      (i) => describe("removed", fromI + i + 1, a[fromI + i]!),
      (j) => describe("added", fromJ + j + 1, b[fromJ + j]!),
    );
  let startI = 0;
  let startJ = 0;
  walkCommon(
    a.length,
    b.length,
    (i, j) => a[i]!.digest === b[j]!.digest,
    (i, j) => {
      run(startI, startJ, i, j);
      startI = i + 1;
      startJ = j + 1;
    },
    () => {},
    () => {},
  );
  run(startI, startJ, a.length, b.length);
  if (out.length > CHAIN_LINES) return [...out.slice(0, CHAIN_LINES - 1), { kind: "more", count: out.length - (CHAIN_LINES - 1) }];
  return out;
}

/** The plan's records, in the order core applies them, or the primary alone. */
export function planRecords(plan: Plan): string[] {
  if (plan.apply_order?.length) return [...plan.apply_order];
  return [plan.subscription_id];
}

/** Plan texts whose sections cover a record (the primary, a promoted member or a collection in the closure). */
export function planCovers(plan: Plan, id: string): boolean {
  return plan.subscription_id === id || (plan.closure ?? []).includes(id) || (plan.records ?? []).some((section) => section.subscription_id === id);
}

/**
 * The records a partial multi-record migration left staged, for "propose the
 * rest": every promoted record of the plan except those that applied. The
 * collection the plan covered is in the new plan's closure through them.
 */
export function remainingRecords(plan: Plan, applied: readonly string[]): string[] {
  return planRecords(plan).filter((id) => !applied.includes(id));
}

// ── the record page's Steps counts for a fleet record ─────────────────────────

/** One step's count, as the record page's Steps tab reads it (chainExplain StepDelta). */
export interface FleetStepDelta {
  index: number;
  label: string;
  before: number;
  after: number;
}

/**
 * The leading structured steps' counts from fleet_preview rather than from a
 * cut preview. A cut preview of a fleet record starts from the rows the
 * selection already kept, so its first step read "23 → 23"; the catalogue
 * says the step took 117 lines to 23. Core evaluates the pushed steps
 * together and reports them once, at the last pushed step, with the whole
 * catalogue before it; a local step reports its own in and out.
 */
export function fleetStepDeltas(
  steps: readonly { index: number; in?: number; out: number; pushed: boolean }[],
  catalogueLines: number,
  labelOf: (index: number) => string,
): FleetStepDelta[] {
  return steps.map((step) => ({
    index: step.index,
    label: labelOf(step.index),
    before: step.pushed || step.in === undefined ? catalogueLines : step.in,
    after: step.out,
  }));
}

/**
 * The preview's deltas with the fleet steps' counts in their place. The
 * steps core evaluated together (the first `covered`) have no count of their
 * own, so the preview's misleading ones for them are dropped too.
 */
export function withFleetDeltas<T extends { index: number }>(deltas: readonly T[], fleet: readonly T[], covered = 0): T[] {
  if (!fleet.length) return [...deltas];
  const replaced = new Set(fleet.map((delta) => delta.index));
  return [...fleet, ...deltas.filter((delta) => !replaced.has(delta.index) && delta.index >= covered)].sort((a, b) => a.index - b.index);
}
