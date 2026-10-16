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

/**
 * The carried-over name filters that keep no line under the fleet labels. An
 * export record's migrate_record reads no catalogue page, so the check runs
 * here over bind.preview's labels (s2-plan section 6). Only a keep-mode
 * Regex Filter is judged: its patterns are what name a line, and a rename or
 * a sort cannot drop one. 1-based step positions.
 */
export function unmatchedNameSteps(process: readonly unknown[], labels: readonly string[]): number[] {
  if (!labels.length) return [];
  const out: number[] = [];
  process.forEach((raw, index) => {
    const step = raw as { type?: string; disabled?: boolean; args?: { regex?: unknown; value?: unknown; keep?: unknown } } | null;
    if (!step || step.disabled || !NAME_STEPS.has(step.type ?? "")) return;
    const patterns = [step.args?.regex, step.args?.value].flat().filter((value): value is string => typeof value === "string" && value !== "");
    if (!patterns.length || step.args?.keep === false) return;
    const compiled = patterns.flatMap((pattern) => {
      try {
        return [new RegExp(pattern)];
      } catch {
        return [];
      }
    });
    if (!compiled.length) return;
    if (!labels.some((label) => compiled.some((pattern) => pattern.test(label)))) out.push(index + 1);
  });
  return out;
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
