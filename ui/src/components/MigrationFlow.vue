<script setup lang="ts">
import { computed, nextTick, reactive, ref, watch } from "vue";
import { ArrowLeft, ArrowRight, Check, ExternalLink, Link2, Pencil, RotateCcw, Undo2 } from "@lucide/vue";
import { PcButton, PcNotice, PcSidePanel } from "@latticenet/plugin-bridge/chassis";

import PlanStatus from "./PlanStatus.vue";
import ScriptNetworkNotice from "./ScriptNetworkNotice.vue";
import { chainChangeText } from "../chainText";
import { ERROR_CODES, KIND_COLLECTION, KIND_FILE, SOURCE_VPN_CORE_GRAPH } from "../client";
import {
  IDENTITY_ACTIVE,
  VPN_CORE_USERS_ROUTE,
  identityInactive,
  planState,
  shortVersion,
  type BindPreviewReply,
  type Identity,
  type PlanStatus as PlanRow,
} from "../fleet";
import { formatBytes, formatCount, t } from "../i18n";
import { hostOriginFromHash, postNavigate } from "../navigate";
import {
  chainChanges,
  closureOf,
  currentPlan,
  darkenedShares,
  decoyShares,
  defaultIdentity,
  explicitCollectionsFrom,
  explicitDependents,
  fileDependents,
  fleetGatherers,
  holdsNoLine,
  isFleetBound,
  legacyMembers,
  linesToBind,
  liveSharesOn,
  mixedBlock,
  parsePlan,
  partialApply,
  recordName,
  remainingRecords,
  shareFields,
  shareLabel,
  shortRevision,
  storedSteps,
  tagDependents,
  undecidedShares,
  unmatchedNameSteps,
  unstagedSiblings,
  type ChainChange,
  type MigrateRecordReply,
  type PlanShareRow,
  type ShareDecision,
} from "../planFlow";
import { refusedStepIn, scriptNetworkRefusal, type ScriptNetworkRefusal } from "../scriptNetwork";
import type { PlanOutcome, ProposeRequest, UseSubscriptions } from "../useSubscriptions";

/**
 * The plan flow (s2-plan sections 6 and 11): one stepper for every change
 * that goes through a plan, so migrating a legacy record, publishing a
 * staged revision, deleting a fleet-bound record and giving shares their
 * identities read the same way and end on the same status.
 *
 *   migrate   1 what changes, 2 share identities, 3 propose, 4 status
 *   publish   1 what changes, 2 share identities, 3 publish, 4 status
 *   delete    1 what changes, 2 propose, 3 status
 *   assign    1 share identities, 2 propose, 3 status
 *
 * A migration stages the fleet equivalent of the legacy record under the
 * same id (migrate_record) and the other legacy members of every
 * combination that reads it in the same sitting, because a combination
 * cannot serve a fleet member beside a legacy one and there is no order in
 * which one can go first. It then asks for an identity, or an archival, for
 * every live share of every record in the plan and of the combinations that
 * read them, warns when an identity holds none of the lines, and proposes
 * one plan over all of it. A sibling that fails to stage blocks the flow
 * until a retry stages it, since the plan must promote them together. Every
 * step, the status included, offers "Abandon migration", which asks first,
 * naming what it drops and the plan it stales, then drops what the flow
 * staged and leaves the legacy records as they were.
 *
 * Nothing here shows a credential or a share link: shares are named by
 * their display name or slug, and identities by name.
 */
export type FlowMode = "migrate" | "publish" | "delete" | "assign";
type StepId = "changes" | "identities" | "propose" | "status";

const props = defineProps<{
  open: boolean;
  mode: FlowMode;
  recordId: string;
  subs: UseSubscriptions;
  shares: readonly PlanShareRow[] | undefined;
  /** What the table's one-click publish or delete already came to, when it opened the sheet for it. */
  outcome?: PlanOutcome | null;
  /** A plan already proposed for the record, which the sheet opens on instead of proposing a second. */
  plan?: PlanRow | null;
  returnFocusTo?: HTMLElement | null;
}>();

const emit = defineEmits<{
  close: [];
  /** Open the combination's editor with this member removed (a delete blocked by an explicit member). */
  "remove-member": [collectionId: string, memberId: string];
  /** What the flow did, for the table's notice. */
  done: [text: string];
  /** Open a record named in the flow (a file that blocks the migration) in its side panel. */
  "open-record": [id: string];
  /**
   * The record's steps should be fixed (an abandoned migration, a refused
   * step): open its editor on them, at `position` when one is named, with
   * the refusal that asked for it, so the editor says it beside the step.
   */
  "edit-steps": [id: string, position?: number, refusal?: ScriptNetworkRefusal];
}>();

const subs = props.subs;
const items = computed(() => subs.items.value);
const byId = computed(() => new Map(items.value.map((item) => [item.id, item])));
const record = computed(() => byId.value.get(props.recordId));
const name = (id: string) => recordName(byId.value.get(id), id);
/**
 * The fleet-bound combinations that gather this record when it is not
 * fleet-bound itself (S2-SR-1): why its edit staged and why its delete is a
 * plan, which its own flags do not say.
 */
const gatheredBy = computed(() => (record.value && !isFleetBound(record.value) ? fleetGatherers(record.value, items.value) : []));
const gatheredNames = computed(() => gatheredBy.value.map((item) => recordName(item)).join(", "));

const STEPS: Record<FlowMode, StepId[]> = {
  migrate: ["changes", "identities", "propose", "status"],
  publish: ["changes", "identities", "propose", "status"],
  delete: ["changes", "propose", "status"],
  assign: ["identities", "propose", "status"],
};
const steps = computed(() => STEPS[props.mode]);
const step = ref<StepId>("changes");
const stepIndex = computed(() => steps.value.indexOf(step.value));

const title = computed(() => {
  const label = name(props.recordId);
  return t.migration.titles[props.mode](label);
});
const stepLabel = (id: StepId) => (id === "propose" ? t.migration.steps.propose[props.mode] : t.migration.steps[id]);

// ── migration: staging ─────────────────────────────────────────────────────

/** The revision the flow staged per record (migrate_record's reply), beside what the list says. */
const stagedByFlow = ref<Record<string, string>>({});
const prepared = ref<MigrateRecordReply | null>(null);
const together = ref<Record<string, MigrateRecordReply>>({});
const stageRefusal = ref<{ code: string; message: string; files: string[]; collections: string[] } | null>(null);
const stageError = ref("");
/** Siblings whose migrate_record failed, with the reason, until a retry stages them. */
const siblingErrors = ref<Record<string, string>>({});
const staging = ref(false);
/** The legacy record's chain, read before staging, for the name check an export record's migration runs here. */
const legacyProcess = ref<unknown[]>([]);
/** "propose the rest": the records a partial multi-record plan left staged. */
const rest = ref<string[] | null>(null);

function stagedRevisionOf(id: string): string {
  return stagedByFlow.value[id] || byId.value.get(id)?.staged_revision || "";
}

const resumed = computed(() => props.mode === "migrate" && !prepared.value && record.value?.staged_migrated === true);
const staged = computed(() => !!prepared.value || resumed.value);

/** Before anything is staged, what the list already says: the readers, the siblings, the files. */
const predictedCollections = computed(() => closureOf(props.recordId, items.value).slice(1));
const predictedTogether = computed(() => {
  const out = new Set<string>();
  for (const id of predictedCollections.value) {
    const collection = byId.value.get(id);
    if (!collection) continue;
    for (const member of legacyMembers(collection, items.value)) if (member.id !== props.recordId) out.add(member.id);
  }
  return [...out];
});
const predictedFiles = computed(() => (props.mode === "migrate" ? fileDependents(props.recordId, items.value) : []));
const refusedFiles = computed(() => stageRefusal.value?.files ?? []);

async function stage(): Promise<void> {
  const item = record.value;
  if (!item || staging.value) return;
  staging.value = true;
  stageError.value = "";
  stageRefusal.value = null;
  try {
    if (item.source !== SOURCE_VPN_CORE_GRAPH) {
      const full = await subs.get(item.id);
      legacyProcess.value = Array.isArray(full?.process) ? full!.process : Array.isArray(full?.operators) ? full!.operators : [];
    }
    const outcome = await subs.migrateRecord(item.id, item.revision);
    if (outcome.kind === "refused") {
      stageRefusal.value = { code: outcome.code, message: outcome.message, files: outcome.files, collections: outcome.collections };
      return;
    }
    if (outcome.kind !== "staged") {
      stageError.value = outcome.message;
      return;
    }
    prepared.value = outcome.reply;
    stagedByFlow.value = { ...stagedByFlow.value, [item.id]: outcome.reply.staged_revision };
    // The other legacy members of the combinations that read it go in the
    // same sitting: the plan must promote them together.
    await stageSiblings(outcome.reply.dependents.co_migrate.filter((id) => id !== item.id));
  } finally {
    staging.value = false;
  }
}

/** Stage each sibling not yet staged; a failure is kept per sibling, and blocks the flow until a retry. */
async function stageSiblings(ids: readonly string[]): Promise<void> {
  for (const id of ids) {
    if (stagedByFlow.value[id]) continue;
    const sibling = byId.value.get(id);
    const next = sibling ? await subs.migrateRecord(id, sibling.revision) : ({ kind: "error", message: t.migration.changedSinceRead } as const);
    if (next.kind === "staged") {
      together.value = { ...together.value, [id]: next.reply };
      stagedByFlow.value = { ...stagedByFlow.value, [id]: next.reply.staged_revision };
      const rest = { ...siblingErrors.value };
      delete rest[id];
      siblingErrors.value = rest;
    } else {
      siblingErrors.value = { ...siblingErrors.value, [id]: next.message };
    }
  }
}

async function retrySiblings(): Promise<void> {
  if (staging.value) return;
  staging.value = true;
  try {
    await stageSiblings(missingSiblings.value);
  } finally {
    staging.value = false;
  }
}

const coMigrate = computed(() => {
  if (prepared.value) return prepared.value.dependents.co_migrate;
  // A migration staged in an earlier sitting: the siblings the list shows staged by migration too.
  return predictedTogether.value.filter((id) => byId.value.get(id)?.staged_migrated);
});
/** Siblings the plan must promote that have no staged revision: a failed migrate_record. */
const missingSiblings = computed(() =>
  props.mode === "migrate" && staged.value ? unstagedSiblings(coMigrate.value.filter((id) => id !== props.recordId), stagedRevisionOf) : [],
);
const coveredCollections = computed(() => prepared.value?.dependents.collections ?? predictedCollections.value);
/** What migrate_record warned about one record, and for the record itself the name check run here. */
function warningsFor(id: string): string[] {
  const own = id === props.recordId ? prepared.value?.warnings ?? [] : together.value[id]?.warnings ?? [];
  const names = id === props.recordId ? nameWarnings.value.map((index) => t.migration.nameStep(index)) : [];
  return [...own, ...names];
}
/** Step 1's warnings, per record of the migration that has any. */
const warningGroups = computed(() =>
  [props.recordId, ...Object.keys(together.value)].map((id) => ({ id, warnings: id === props.recordId ? prepared.value?.warnings ?? [] : together.value[id]?.warnings ?? [] })).filter((group) => group.warnings.length),
);

// ── who the plan covers ────────────────────────────────────────────────────

/** The records the plan promotes (or deletes), the primary first. */
const promoted = computed<string[]>(() => {
  if (rest.value) return rest.value;
  if (props.mode === "migrate") return [props.recordId, ...coMigrate.value.filter((id) => id !== props.recordId)];
  return [props.recordId];
});
const primary = computed(() => promoted.value[0] ?? props.recordId);

/** Every record of the plan and the combinations that read them. */
const closure = computed(() => {
  const out: string[] = [];
  for (const id of promoted.value) for (const reader of closureOf(id, items.value)) if (!out.includes(reader)) out.push(reader);
  return out;
});

/** The shares the identities step asks about, and the ones it only lists. */
const consideredShares = computed<PlanShareRow[]>(() => {
  const item = record.value;
  if (!item) return [];
  if (props.mode === "assign") return [...decoyShares(item, props.shares), ...darkenedShares(item, props.shares)];
  return liveSharesOn(closure.value, props.shares);
});
const openShares = computed(() => consideredShares.value.filter((share) => !share.identity_id || props.mode === "assign"));
const boundShares = computed(() => (props.mode === "assign" ? [] : consideredShares.value.filter((share) => !!share.identity_id)));

const decisions = reactive<Record<string, ShareDecision>>({});
function decision(share: PlanShareRow): ShareDecision {
  if (!decisions[share.share_id]) decisions[share.share_id] = { share_id: share.share_id, identity: "", archive: false };
  return decisions[share.share_id]!;
}
// Every share the step asks about has its decision before the step draws, so
// the template binds to an entry rather than creating one while rendering.
watch(
  () => openShares.value.map((share) => share.share_id).join(","),
  () => {
    for (const share of openShares.value) decision(share);
  },
  { immediate: true },
);

const identities = computed<Identity[]>(() => subs.identities.value ?? []);
function identityName(id: string): string {
  return identities.value.find((identity) => identity.id === id)?.name || id;
}

/**
 * A graph record's shares start on the identity it composed for, each
 * migrated record on its own (migrate_record names it in `legacy`).
 */
function seedDecisions(): void {
  const legacyOf = (id: string) => (id === props.recordId ? prepared.value?.legacy : together.value[id]?.legacy);
  for (const share of openShares.value) {
    const entry = decision(share);
    if (entry.identity || entry.archive || share.identity_id) continue;
    const seed = defaultIdentity(legacyOf(share.subscription_id), identities.value);
    if (seed) entry.identity = seed;
  }
}

const undecided = computed(() => (props.mode === "assign" ? [] : undecidedShares(openShares.value, decisions)));
const assignChanges = computed(() =>
  openShares.value.filter((share) => {
    const entry = decisions[share.share_id];
    if (!entry) return false;
    return entry.archive || (share.identity_id ? entry.clear === true : !!entry.identity);
  }),
);
const identitiesReady = computed(() => (props.mode === "assign" ? assignChanges.value.length > 0 : undecided.value.length === 0));

// ── a chosen identity's preview: zero bindings, the name check ─────────────

interface PreviewState {
  status: "loading" | "ready" | "error";
  reply?: BindPreviewReply;
  error?: string;
}
const previews = ref<Record<string, PreviewState>>({});
const previewKey = (recordId: string, identityId: string) => `${recordId}\u0000${identityId}`;

/** The revision a share's preview binds: the staged one for a record the plan promotes, else the live one. */
function previewRevision(recordId: string): string | undefined {
  if (props.mode === "assign" || props.mode === "delete") return undefined;
  return promoted.value.includes(recordId) ? stagedRevisionOf(recordId) || undefined : undefined;
}

/**
 * The record a share's preview asks about: its own when the plan promotes it
 * (or changes only shares); for a combination that reads a promoted record,
 * that record at its staged revision, since bind.preview takes no member
 * revisions and the combination's own preview would read its legacy members.
 */
function previewTarget(share: PlanShareRow): string {
  if (props.mode === "assign" || promoted.value.includes(share.subscription_id)) return share.subscription_id;
  const collection = byId.value.get(share.subscription_id);
  const member = collection ? promoted.value.find((id) => closureOf(id, items.value).includes(collection.id)) : undefined;
  return member ?? "";
}

async function preview(recordId: string, identityId: string): Promise<void> {
  if (!identityId || !recordId || !subs.canBindPreview.value) return;
  const key = previewKey(recordId, identityId);
  if (previews.value[key]) return;
  previews.value = { ...previews.value, [key]: { status: "loading" } };
  try {
    const reply = await subs.bindPreview(recordId, identityId, previewRevision(recordId));
    previews.value = { ...previews.value, [key]: { status: "ready", reply } };
  } catch (cause) {
    previews.value = { ...previews.value, [key]: { status: "error", error: cause instanceof Error ? cause.message : "" } };
  }
}

watch(
  () => openShares.value.map((share) => `${share.share_id}:${decisions[share.share_id]?.identity ?? ""}`).join(","),
  () => {
    for (const share of openShares.value) {
      const chosen = decisions[share.share_id]?.identity;
      if (chosen && !decisions[share.share_id]?.archive) void preview(previewTarget(share), chosen);
    }
  },
);

function previewOf(share: PlanShareRow): PreviewState | undefined {
  const entry = decisions[share.share_id];
  if (!entry?.identity || entry.archive) return undefined;
  return previews.value[previewKey(previewTarget(share), entry.identity)];
}
/** The identity holds none of the lines: its preview leaves every line out as not bound, or it holds no binding at all. */
function emptyFor(share: PlanShareRow): boolean {
  const entry = decisions[share.share_id];
  if (!entry?.identity || entry.archive) return false;
  if (identities.value.find((identity) => identity.id === entry.identity)?.bound_lines === 0) return true;
  const state = previewOf(share);
  return state?.status === "ready" && holdsNoLine(state.reply);
}
function inactiveIdentity(share: PlanShareRow): Identity | undefined {
  const entry = decisions[share.share_id];
  const found = entry?.identity ? identities.value.find((identity) => identity.id === entry.identity) : undefined;
  return found && identityInactive(found.status) ? found : undefined;
}

/** An export record's carried name filters that keep none of the lines an identity would receive. */
const nameWarnings = computed(() => {
  if (props.mode !== "migrate" || !legacyProcess.value.length) return [];
  const labels = new Set<string>();
  for (const [key, state] of Object.entries(previews.value)) {
    if (!key.startsWith(`${props.recordId}\u0000`) || state.status !== "ready" || !state.reply) continue;
    for (const entry of state.reply.entries) if (entry.label || entry.name) labels.add(entry.label || entry.name!);
    for (const entry of state.reply.excluded) if (entry.name) labels.add(entry.name);
  }
  return unmatchedNameSteps(legacyProcess.value, [...labels]);
});

// ── bind the identity to the selection (decision 8) ────────────────────────

const binding = ref<Record<string, { busy: boolean; text: string; unavailable: boolean }>>({});
async function bindSelection(share: PlanShareRow): Promise<void> {
  const entry = decisions[share.share_id];
  const state = previewOf(share);
  if (!entry?.identity || !state?.reply) return;
  const key = previewKey(previewTarget(share), entry.identity);
  binding.value = { ...binding.value, [key]: { busy: true, text: "", unavailable: false } };
  const result = await subs.bindSelection(entry.identity, linesToBind(state.reply));
  binding.value = {
    ...binding.value,
    [key]: result.ok
      ? { busy: false, text: t.plans.bind.filed(result.reply.filed.length, result.reply.batch, result.reply.skipped.length), unavailable: false }
      : { busy: false, text: result.message, unavailable: result.unavailable },
  };
}
function bindingOf(share: PlanShareRow) {
  const entry = decisions[share.share_id];
  return entry?.identity ? binding.value[previewKey(previewTarget(share), entry.identity)] : undefined;
}

// ── delete ─────────────────────────────────────────────────────────────────

const ownShares = computed(() => liveSharesOn([props.recordId], props.shares));
const explicit = computed(() => explicitDependents(props.recordId, items.value));
const byTag = computed(() => tagDependents(props.recordId, items.value));
const serverExplicit = ref<string[]>([]);
/**
 * What a delete drops beyond the record and its shares: its staged revision,
 * never published, and the pending plan over that revision, which goes stale
 * once the record is gone. Discard names both; the delete, which does more,
 * names them too.
 */
const deleteDrops = computed(() => {
  const item = record.value;
  if (props.mode !== "delete" || !item?.staged_revision) return [];
  const lines = [t.migration.deleteDropsStaged(shortRevision(item.staged_revision))];
  const over = currentPlan(subs.plansByRecord.value[item.id] ?? [], item);
  if (over && over.action !== "delete" && planState(over) === "pending") lines.push(t.migration.deleteStalesPlan(over.approval_id));
  return lines;
});
const blockingCollections = computed(() => {
  const ids = new Set([...explicit.value.map((item) => item.id), ...serverExplicit.value]);
  return [...ids];
});

// ── propose ────────────────────────────────────────────────────────────────

const proposing = ref(false);
const outcome = ref<PlanOutcome | null>(null);
const planRow = ref<PlanRow | null>(null);
const planText = ref("");
const published = ref("");
const banner = ref<{ tone: "danger" | "warning"; code: string; text: string } | null>(null);
/** Core refused the plan because a fleet render's script asked for the network (S2-SR-2). */
const networkRefusal = ref<ScriptNetworkRefusal | null>(null);
/** The revisions the refusal answered (refusalKey below). */
const refusedAt = ref("");
/** The steps each promoted record changes, read for the propose step (readStepSummary below). */
const stepSummary = ref<Record<string, { status: "loading" | "ready" | "error"; changes: ChainChange[]; steps: unknown[] }>>({});

function buildRequest(): ProposeRequest {
  const id = primary.value;
  const item = byId.value.get(id);
  const live = item?.revision ?? "";
  if (props.mode === "delete") return { subscription_id: id, from_revision: live, action: "delete" };
  const fields = shareFields(openShares.value, decisions);
  if (props.mode === "assign") return { subscription_id: id, from_revision: live, to_revision: live, ...fields };
  const others = promoted.value.slice(1).map((other) => ({
    subscription_id: other,
    from_revision: byId.value.get(other)?.revision ?? "",
    to_revision: stagedRevisionOf(other),
  }));
  return {
    subscription_id: id,
    from_revision: live,
    to_revision: stagedRevisionOf(id),
    ...(others.length ? { records: others } : {}),
    ...fields,
  };
}

function publishPath(request: ProposeRequest): { revision: string; expected_revision: string; action?: "promote" | "delete" } | null {
  if (props.mode === "delete") return { revision: request.from_revision, expected_revision: request.from_revision, action: "delete" };
  if (props.mode === "assign" || request.records?.length) return null;
  return { revision: request.to_revision ?? "", expected_revision: request.from_revision };
}

async function propose(): Promise<void> {
  if (proposing.value) return;
  proposing.value = true;
  banner.value = null;
  networkRefusal.value = null;
  try {
    const request = buildRequest();
    land(await subs.proposeOrPublish(request, publishPath(request)));
  } finally {
    proposing.value = false;
  }
}

/** Where a plan request's answer takes the flow. */
function land(result: PlanOutcome): void {
  outcome.value = result;
  if (result.kind === "plan") {
    planText.value = result.reply.plan ?? "";
    planRow.value = result.reply.approval_id
      ? ({
          approval_id: result.reply.approval_id,
          status: result.reply.status || "pending",
          plan_sha256: result.reply.plan_sha256 ?? "",
          subscription_id: primary.value,
          from_revision: result.plan?.from_revision ?? "",
          to_revision: result.plan?.to_revision ?? "",
          action: result.plan?.action === "delete" ? "delete" : "promote",
          mode: result.plan?.mode ?? "",
          created_at: new Date().toISOString(),
          updated_at: new Date().toISOString(),
        } as PlanRow)
      : null;
    step.value = "status";
    if (result.reply.approval_id) void refreshPlan();
    return;
  }
  if (result.kind === "published") {
    published.value = props.mode === "delete" ? t.migration.deleted(name(primary.value)) : t.migration.published(name(primary.value), shortRevision(result.reply.revision));
    step.value = "status";
    emit("done", published.value);
    return;
  }
  // A script step that asked for the network: said beside the record and step it names, with a way to the step.
  const network =
    (result.kind === "refused" && result.code === ERROR_CODES.fleetScriptNetworkUnavailable) || result.kind === "error" ? scriptNetworkRefusal(result.message) : null;
  if (network) {
    networkRefusal.value = network;
    step.value = "propose";
    refusedAt.value = refusalKey();
    return;
  }
  if (result.kind === "too-large") {
    banner.value = { tone: "danger", code: "plan_too_large", text: t.migration.tooLarge(formatBytes(result.bytes), formatBytes(result.limit)) };
    step.value = "propose";
    return;
  }
  if (result.kind === "refused") {
    const code = result.code;
    if (code === ERROR_CODES.sharesWithoutIdentity || code === ERROR_CODES.migrationRequiresPlan) {
      banner.value = { tone: "warning", code, text: code === ERROR_CODES.sharesWithoutIdentity ? t.migration.needIdentities : t.migration.migratedNeedsPlan };
      if (steps.value.includes("identities")) step.value = "identities";
      return;
    }
    if (code === ERROR_CODES.fleetDeleteHasExplicitDependents) {
      serverExplicit.value = explicitCollectionsFrom(result.message);
      banner.value = { tone: "danger", code, text: t.migration.explicitMembers };
      step.value = "changes";
      return;
    }
    banner.value = { tone: "danger", code, text: (t.migration.refusals as Record<string, string>)[code] ?? result.message };
    if (code === ERROR_CODES.fleetFileUnavailable && steps.value.includes("changes")) step.value = "changes";
    return;
  }
  banner.value = { tone: "danger", code: "", text: result.message };
}

const checking = ref(false);
async function refreshPlan(): Promise<void> {
  const id = planRow.value?.approval_id;
  if (!id || checking.value) return;
  checking.value = true;
  try {
    const row = await subs.readPlan(id);
    if (row) planRow.value = row;
    await subs.load();
  } finally {
    checking.value = false;
  }
}

function proposeAgain(): void {
  outcome.value = null;
  planRow.value = null;
  planText.value = "";
  banner.value = null;
  networkRefusal.value = null;
  step.value = steps.value.includes("identities") ? "identities" : steps.value[0]!;
}

function proposeRest(applied: string[]): void {
  const plan = parsePlan(planText.value);
  const remaining = (plan ? remainingRecords(plan, applied) : promoted.value.filter((id) => !applied.includes(id))).filter(
    (id) => !!byId.value.get(id)?.staged_revision,
  );
  if (!remaining.length) return;
  rest.value = remaining;
  outcome.value = null;
  planRow.value = null;
  planText.value = "";
  banner.value = null;
  step.value = "identities";
}

const canOpenApprovals = computed(() => !!hostOriginFromHash(typeof window === "undefined" ? "" : window.location.hash));
function openApprovals(approvalId: string): void {
  const origin = hostOriginFromHash(window.location.hash);
  if (origin) postNavigate(window, `/approvals?open=${encodeURIComponent(approvalId)}`, origin);
}
function openUsers(): void {
  const origin = hostOriginFromHash(window.location.hash);
  if (origin) postNavigate(window, VPN_CORE_USERS_ROUTE, origin);
}

// ── abandon ────────────────────────────────────────────────────────────────

const abandoning = ref(false);
const abandonError = ref("");
/**
 * What "Abandon migration" drops: every record of the migration that has a
 * staged revision. A migration staged earlier and reopened from Publish is
 * still one: its record and the siblings staged with it.
 */
const flowStaged = computed(() => {
  if (props.mode === "migrate") return promoted.value.filter((id) => !!stagedRevisionOf(id));
  if (props.mode === "publish" && record.value?.staged_migrated) {
    return [props.recordId, ...coMigrate.value.filter((id) => id !== props.recordId)].filter((id) => !!stagedRevisionOf(id));
  }
  return [];
});
/** Abandon asks first; `edit` names the record whose steps open once it is done, at `position` when a step is named. */
const abandonAsk = ref<{ edit: string; position?: number; refusal?: ScriptNetworkRefusal } | null>(null);
/** The pending plan abandoning would stale: the one this sitting filed or reopened, else the record's own. */
const abandonPlan = computed(() => {
  const row = planRow.value ?? (record.value ? currentPlan(subs.plansByRecord.value[primary.value] ?? [], record.value) : undefined);
  return row && planState(row) === "pending" ? row : null;
});
/**
 * The combinations an abandon leaves refusing to serve: each reads a member
 * already promoted to the fleet (a multi-record plan that stopped part way)
 * beside one this abandon keeps legacy. Abandon keeps the live revisions, so
 * what the list says now is what holds after it.
 */
const blockedAfterAbandon = computed(() => {
  const dropped = new Set(flowStaged.value);
  const out: string[] = [];
  for (const id of flowStaged.value) {
    for (const reader of closureOf(id, items.value).slice(1)) {
      const collection = byId.value.get(reader);
      if (!collection || out.includes(reader)) continue;
      if (mixedBlock(collection, items.value)?.some((member) => dropped.has(member.id))) out.push(reader);
    }
  }
  return out;
});
const abandonLines = computed(() => {
  const lines = flowStaged.value.map((id) => t.migration.abandonDrops(name(id), shortRevision(stagedRevisionOf(id))));
  for (const id of blockedAfterAbandon.value) {
    lines.push(t.migration.abandonBlocked(name(id), liveSharesOn([id], props.shares).map(shareLabel).join(", ")));
  }
  if (abandonPlan.value) lines.push(t.plans.discard.stalesPlan(abandonPlan.value.approval_id));
  if (step.value !== "status" && Object.values(decisions).some((entry) => !!entry.identity || entry.archive || entry.clear === true)) lines.push(t.migration.abandonDecisions);
  return lines;
});
/** The way to finish instead, when the abandon leaves a combination blocked. */
const abandonFinish = computed(() => {
  if (!blockedAfterAbandon.value.length) return "";
  return partialOf.value ? t.migration.abandonFinishRest : t.migration.abandonFinishPropose;
});
/** The line under "Abandon migration…": what keeps serving, and what does not. */
const abandonNote = computed(() =>
  blockedAfterAbandon.value.length ? t.migration.abandonNoteBlocked(blockedAfterAbandon.value.map(name).join(", ")) : t.migration.abandonNote,
);
const abandonBox = ref<HTMLElement | null>(null);
function askAbandon(edit = "", position?: number, refusal?: ScriptNetworkRefusal): void {
  abandonAsk.value = { edit, ...(position ? { position } : {}), ...(refusal ? { refusal } : {}) };
  abandonError.value = "";
  void nextTick(() => abandonBox.value?.querySelector<HTMLElement>("[data-abandon-heading]")?.focus());
}
async function abandon(): Promise<void> {
  if (abandoning.value) return;
  abandoning.value = true;
  abandonError.value = "";
  try {
    const dropped: string[] = [];
    for (const id of flowStaged.value) {
      const revision = stagedRevisionOf(id);
      if (!revision) continue;
      if (await subs.discardStaged(id, revision)) dropped.push(name(id));
      else {
        abandonError.value = subs.actionError.value;
        return;
      }
    }
    const edit = abandonAsk.value?.edit ?? "";
    const position = abandonAsk.value?.position;
    const refusal = abandonAsk.value?.refusal;
    abandonAsk.value = null;
    emit("done", t.migration.abandoned(dropped.join(", ") || name(props.recordId)));
    if (edit) emit("edit-steps", edit, position, refusal);
    else emit("close");
  } finally {
    abandoning.value = false;
  }
}

// ── navigation ─────────────────────────────────────────────────────────────

const body = ref<HTMLElement | null>(null);
/** The steps this sitting showed: a step jumped over (a plan reopened at its status) is not marked done. */
const visited = ref<Set<StepId>>(new Set());
watch(step, (id) => {
  if (!visited.value.has(id)) visited.value = new Set([...visited.value, id]);
}, { immediate: true });
function go(next: StepId): void {
  step.value = next;
  abandonAsk.value = null;
  if (next === "identities") seedDecisions();
  void nextTick(() => body.value?.querySelector<HTMLElement>("[data-step-heading]")?.focus());
}
function back(): void {
  const at = stepIndex.value;
  if (at > 0) go(steps.value[at - 1]!);
}
const nextBlocked = computed(() => {
  // The siblings stage after the record itself; nothing moves on until they have.
  if (staging.value) return t.migration.staging;
  if (step.value === "changes" && props.mode === "migrate" && !staged.value) {
    // Files first: a record they read cannot be staged, so "stage it first" would send the operator round.
    if (predictedFiles.value.length || refusedFiles.value.length) return t.migration.filesBlockShort;
    return t.migration.stageFirst;
  }
  if (missingSiblings.value.length && step.value !== "status") return t.migration.siblingsMissing(missingSiblings.value.map(name).join(", "));
  if (step.value === "changes" && props.mode === "delete" && blockingCollections.value.length) return t.migration.explicitMembers;
  if (step.value === "identities" && !identitiesReady.value) return props.mode === "assign" ? t.migration.chooseChange : t.migration.undecided(undecided.value.length);
  return "";
});
function next(): void {
  const at = stepIndex.value;
  if (nextBlocked.value || at < 0 || at >= steps.value.length - 1) return;
  go(steps.value[at + 1]!);
}
/** The footer repeats why Next is off only when the step's body does not already say it. */
const footerWhy = computed(() => {
  if (!nextBlocked.value || step.value === "propose" || step.value === "status") return "";
  if (step.value === "identities" && undecided.value.length && identitiesReady.value === false && props.mode !== "assign") return "";
  if (step.value === "changes" && props.mode === "delete" && blockingCollections.value.length) return "";
  if (missingSiblings.value.length) return "";
  return nextBlocked.value;
});

// ── opening ────────────────────────────────────────────────────────────────

function reset(): void {
  stagedByFlow.value = {};
  prepared.value = null;
  together.value = {};
  stageRefusal.value = null;
  stageError.value = "";
  siblingErrors.value = {};
  abandonAsk.value = null;
  legacyProcess.value = [];
  rest.value = null;
  previews.value = {};
  binding.value = {};
  serverExplicit.value = [];
  outcome.value = null;
  planRow.value = null;
  planText.value = "";
  published.value = "";
  banner.value = null;
  networkRefusal.value = null;
  refusedAt.value = "";
  stepSummary.value = {};
  abandonError.value = "";
  for (const key of Object.keys(decisions)) delete decisions[key];
}

watch(
  () => [props.open, props.recordId, props.mode] as const,
  async ([open]) => {
    if (!open) return;
    reset();
    step.value = steps.value[0]!;
    void subs.loadIdentities();
    void subs.loadPlans([props.recordId]);
    if (props.mode === "migrate" && record.value?.staged_migrated) {
      // Staged in an earlier sitting: resume at the shares, or at the plan when one waits.
      await subs.loadPlans([props.recordId]);
      const waiting = (subs.plansByRecord.value[props.recordId] ?? []).find((plan) => plan.to_revision === record.value?.staged_revision && plan.status === "pending");
      if (waiting) {
        planRow.value = waiting;
        step.value = "status";
      } else {
        go("identities");
      }
    }
    if (props.outcome) land(props.outcome);
    else if (props.plan) {
      planRow.value = props.plan;
      step.value = "status";
    }
    // Only the step the flow opened on counts as shown: a plan reopened at its status skipped the rest.
    visited.value = new Set([step.value]);
  },
  { immediate: true },
);

watch(identities, () => {
  if (step.value === "identities") seedDecisions();
});

const proposeDenied = computed(() => {
  if (!subs.canPropose.value) return t.plans.cannotPropose;
  if ((props.mode === "publish" || props.mode === "delete") && !subs.canPublishPlan.value) return t.plans.cannotPublish;
  if (missingSiblings.value.length) return t.migration.siblingsMissing(missingSiblings.value.map(name).join(", "));
  return "";
});
const stageDenied = computed(() => (subs.canMigrateRecord.value ? "" : t.migration.cannotMigrate));

const proposeSummary = computed(() => {
  const request = buildRequest();
  const assigned = Object.entries(request.share_identities ?? {}).filter(([, identity]) => identity !== "");
  const cleared = Object.entries(request.share_identities ?? {}).filter(([, identity]) => identity === "");
  const label = (id: string) => {
    const share = consideredShares.value.find((entry) => entry.share_id === id);
    return share ? shareLabel(share) : id;
  };
  return {
    records: [request.subscription_id, ...(request.records ?? []).map((entry) => entry.subscription_id)].map((id) => ({
      id,
      name: name(id),
      warnings: props.mode === "migrate" ? warningsFor(id) : [],
      from: id === request.subscription_id ? request.from_revision : request.records?.find((entry) => entry.subscription_id === id)?.from_revision ?? "",
      to: id === request.subscription_id ? request.to_revision ?? "" : request.records?.find((entry) => entry.subscription_id === id)?.to_revision ?? "",
    })),
    assigned: assigned.map(([share, identity]) => t.migration.summaryAssign(label(share), identityName(identity))),
    cleared: cleared.map(([share]) => t.migration.summaryClear(label(share))),
    archived: (request.archive_shares ?? []).map((share) => t.migration.summaryArchive(label(share))),
  };
});

// ── the steps each promoted record changes, before core answers (S2-SR-2) ──

/**
 * Per record the plan promotes, the steps its staged revision adds, removes
 * or changes against the live one, read from both documents and listed by
 * core's rule (planFlow.ts chainChanges). Core lists the same lines in the
 * plan it files; this says them where the operator decides to propose.
 */
async function readStepSummary(ids: readonly string[]): Promise<void> {
  for (const id of ids) {
    if (stepSummary.value[id]) continue;
    stepSummary.value = { ...stepSummary.value, [id]: { status: "loading", changes: [], steps: [] } };
    const read = await subs.readRevisions(id);
    const next = read?.staged
      ? { status: "ready" as const, changes: chainChanges(storedSteps(read.live), storedSteps(read.staged)), steps: storedSteps(read.staged) }
      : { status: "error" as const, changes: [], steps: [] };
    stepSummary.value = { ...stepSummary.value, [id]: next };
  }
}
watch(
  () => (props.open && step.value === "propose" && (props.mode === "publish" || props.mode === "migrate") ? proposeSummary.value.records.map((entry) => entry.id).join(",") : ""),
  (ids) => {
    if (ids) void readStepSummary(ids.split(","));
  },
);
const anyStepChange = computed(() => Object.values(stepSummary.value).some((entry) => entry.status === "ready" && entry.changes.length > 0));

/** The record a script network refusal is about: the one it names, else the plan's primary. */
const refusalRecord = computed(() => (networkRefusal.value ? networkRefusal.value.record || primary.value : ""));
/**
 * The step it is about, from the refusal or from the staged steps read for
 * the summary; proven only for a subscription, which renders its own chain
 * alone.
 */
const refusalStep = computed(() => {
  const refusal = networkRefusal.value;
  if (!refusal) return null;
  const item = byId.value.get(refusalRecord.value);
  const ownChainOnly = !!item && ![item.kind, item.staged_kind].some((kind) => kind === KIND_COLLECTION || kind === KIND_FILE);
  return refusedStepIn(refusal, stepSummary.value[refusalRecord.value]?.steps ?? [], ownChainOnly);
});
/**
 * What the refusal answered: every record the plan promotes and the one it
 * names, at their live and staged revisions. Core answers the same until
 * one of them changes, so while they stand, proposing again cannot get past
 * it, and the way forward is the step (the lane 6c design review: Publish
 * stayed the primary button under a refusal it could not pass).
 */
function refusalKey(): string {
  const ids = [...new Set([...proposeSummary.value.records.map((entry) => entry.id), refusalRecord.value])];
  return ids.map((id) => `${id}@${byId.value.get(id)?.revision ?? ""}/${stagedRevisionOf(id)}`).join(",");
}
const refusalStands = computed(() => !!networkRefusal.value && refusedAt.value === refusalKey());
/** The record the refusal names is one the summary lists, so the notice sits under it. */
const refusalListed = computed(() => proposeSummary.value.records.some((entry) => entry.id === refusalRecord.value));
function flaggedLine(recordId: string, change: ChainChange): boolean {
  return recordId === refusalRecord.value && change.kind === "step" && change.verb !== "removed" && change.position === refusalStep.value?.position;
}
/**
 * Open the step a refusal names. A migration's staged revision is built from
 * the legacy record as it is now, so the step is fixed there: abandon, then
 * the editor. Any other plan's record opens in the editor on its staged
 * revision, at the step.
 */
function openRefusedStep(position: number | null): void {
  const id = refusalRecord.value;
  if (!id) return;
  const refusal = networkRefusal.value ?? undefined;
  if (props.mode === "migrate" && flowStaged.value.includes(id)) {
    askAbandon(id, position ?? undefined, refusal);
    return;
  }
  emit("edit-steps", id, position ?? undefined, refusal);
}

/**
 * An identity as the select lists it: its name, and its state when it is
 * not active, since an inactive identity's share answers the decoy. The
 * chosen one's state and bound lines are said once, on the line under the
 * select (the second lane 6b design review: the facts read twice).
 */
function identityOption(identity: Identity): string {
  const name = identity.name || identity.id;
  if (!identityInactive(identity.status)) return name;
  const status = t.fleetEditor.identity.status[identity.status as keyof typeof t.fleetEditor.identity.status] ?? identity.status;
  return `${name} (${status})`;
}
const sortedIdentities = computed(() =>
  [...identities.value].sort((a, b) => Number(b.status === IDENTITY_ACTIVE) - Number(a.status === IDENTITY_ACTIVE) || (a.name || a.id).localeCompare(b.name || b.id)),
);

function isDecoy(share: PlanShareRow): boolean {
  const owner = byId.value.get(share.subscription_id);
  return !!owner && isFleetBound(owner) && !share.identity_id;
}
function multipleRecords(): boolean {
  return new Set(consideredShares.value.map((share) => share.subscription_id)).size > 1;
}
const partialOf = computed(() => (planRow.value ? partialApply(planRow.value.reason) : null));

/** The records the status names with their revisions when no plan text is in hand: what this flow knows the plan promotes. */
const statusRecords = computed(() => {
  if (props.mode === "delete" || props.mode === "assign") return [];
  const ids = props.mode === "migrate" ? promoted.value : flowStaged.value.length ? flowStaged.value : [props.recordId];
  return ids.map((id) => ({ id, from: byId.value.get(id)?.revision ?? "", to: stagedRevisionOf(id) }));
});

/** A file named in the flow opens in its side panel when the list has it; else it is only named. */
function fileKnown(id: string): boolean {
  return byId.value.has(id);
}
/** A file refusal (predicted from the list or named by core) leaves nothing to stage. */
const filesBlock = computed(() => predictedFiles.value.length > 0 || refusedFiles.value.length > 0);

/** The chosen identity's facts, under the select, where a long name cuts them off in the option. */
function identityFacts(share: PlanShareRow): string {
  const chosen = decisions[share.share_id]?.identity;
  const found = chosen ? identities.value.find((identity) => identity.id === chosen) : undefined;
  if (!found) return "";
  return [t.fleetEditor.identity.status[found.status as keyof typeof t.fleetEditor.identity.status] ?? found.status, t.fleetEditor.identity.boundLines(found.bound_lines)].join(" · ");
}

/** Keep, clear or archive an identity-bound share: one choice of three. */
function setChoice(share: PlanShareRow, choice: "keep" | "clear" | "archive"): void {
  const entry = decision(share);
  entry.clear = choice === "clear";
  entry.archive = choice === "archive";
}
</script>

<template>
  <PcSidePanel
    :open="open"
    :title="title"
    :description="mode === 'delete' && gatheredBy.length ? t.plans.stagedSave.deleteDescriptionGathered : t.migration.descriptions[mode]"
    size="output"
    :close-label="t.migration.close"
    :return-focus-to="returnFocusTo ?? null"
    @close="emit('close')"
  >
    <div ref="body" class="flow" data-testid="plan-flow" :data-mode="mode" :data-step="step">
      <ol class="flow-steps" :aria-label="t.migration.stepsLabel">
        <li
          v-for="(id, index) in steps"
          :key="id"
          class="flow-step"
          :class="{ 'is-current': id === step, 'is-done': index < stepIndex && visited.has(id) }"
          :aria-current="id === step ? 'step' : undefined"
          data-testid="flow-step"
          :data-done="index < stepIndex && visited.has(id) ? 'true' : undefined"
        >
          <span class="flow-step__mark" aria-hidden="true">
            <Check v-if="index < stepIndex && visited.has(id)" :size="12" />
            <template v-else>{{ formatCount(index + 1) }}</template>
          </span>
          <span class="flow-step__label">{{ stepLabel(id) }}</span>
        </li>
      </ol>

      <PcNotice v-if="banner" :tone="banner.tone" data-testid="flow-banner" :data-code="banner.code">
        {{ banner.text }}
      </PcNotice>

      <!-- ── 1 what changes ─────────────────────────────────────────────── -->
      <section v-if="step === 'changes'" class="flow-section" aria-labelledby="flow-changes">
        <h3 id="flow-changes" class="flow-heading" tabindex="-1" data-step-heading>{{ t.migration.steps.changes }}</h3>

        <template v-if="mode === 'migrate'">
          <p class="flow-prose">{{ t.migration.intro }}</p>
          <dl class="flow-facts">
            <div>
              <dt>{{ t.migration.facts.source }}</dt>
              <dd>{{ record?.source === 'vpn-core-graph' ? t.migration.facts.graph : t.migration.facts.export }}</dd>
            </div>
            <div v-if="prepared">
              <dt>{{ t.migration.facts.selection }}</dt>
              <dd data-testid="migration-selection">
                <template v-if="prepared.selection.every_bound_line">{{ t.migration.selectionEvery }}</template>
                <template v-else>
                  {{ t.migration.selectionRoots(prepared.selection.count, prepared.legacy.entry_roots?.length ?? 0) }}
                  <span v-if="prepared.selection.catalogue_version" class="flow-mono">{{ t.fleetEditor.proof.catalogue(shortVersion(prepared.selection.catalogue_version)) }}</span>
                </template>
              </dd>
            </div>
            <div v-if="prepared || resumed">
              <dt>{{ t.migration.facts.staged }}</dt>
              <dd class="flow-mono" data-testid="migration-staged">{{ shortRevision(stagedRevisionOf(recordId)) }}</dd>
            </div>
            <div v-if="coveredCollections.length">
              <dt>{{ t.migration.facts.collections }}</dt>
              <dd>{{ coveredCollections.map(name).join(", ") }}</dd>
            </div>
          </dl>

          <div v-if="(prepared ? coMigrate : predictedTogether).length" class="flow-block" data-testid="migration-together">
            <h4>{{ t.migration.togetherTitle }}</h4>
            <p class="flow-note">{{ t.migration.togetherNote }}</p>
            <ul class="flow-list">
              <li v-for="id in prepared ? coMigrate : predictedTogether" :key="id" data-testid="migration-together-row" :data-record="id">
                <span>{{ name(id) }}</span>
                <span v-if="stagedRevisionOf(id)" class="flow-mono">{{ t.migration.stagedAs(shortRevision(stagedRevisionOf(id))) }}</span>
                <span v-else-if="siblingErrors[id]" class="flow-error-inline">{{ t.migration.notStagedYet }}</span>
                <span v-else class="flow-note">{{ t.migration.stagedWithIt }}</span>
              </li>
            </ul>
          </div>

          <!-- A sibling that failed to stage: the plan promotes them together, so nothing moves on until it stages. -->
          <PcNotice v-if="missingSiblings.length && !staging" tone="danger" :title="t.migration.siblingsTitle" class="flow-notice-stack" data-testid="migration-sibling-failed">
            <ul class="flow-list">
              <li v-for="id in missingSiblings" :key="id">{{ t.migration.siblingFailed(name(id), siblingErrors[id] || t.migration.notStagedYet) }}</li>
            </ul>
            <p>{{ t.migration.siblingsMissing(missingSiblings.map(name).join(", ")) }}</p>
            <template #actions>
              <PcButton compact variant="primary" :busy="staging" data-testid="migration-retry" @click="retrySiblings()">
                <template #icon><RotateCcw :size="13" aria-hidden="true" /></template>
                {{ t.migration.retrySiblings }}
              </PcButton>
            </template>
          </PcNotice>

          <!-- Files cannot read fleet lines before S3: predicted from the list, and named by the refusal. -->
          <PcNotice v-if="predictedFiles.length && !staged" tone="warning" :title="t.migration.filesTitle" data-testid="migration-blocked-files">
            <p>{{ t.migration.filesBlock(predictedFiles.length) }}</p>
            <ul class="flow-list">
              <li v-for="file in predictedFiles" :key="file.id" data-testid="migration-blocked-file">
                <button type="button" class="flow-link" data-testid="migration-file-link" :title="t.migration.openFile(name(file.id))" @click="emit('open-record', file.id)">{{ name(file.id) }}</button>
              </li>
            </ul>
          </PcNotice>
          <PcNotice v-if="stageRefusal" tone="danger" :title="t.migration.refusedTitle" data-testid="migration-refused" :data-code="stageRefusal.code">
            <!-- Core's sentence names the files by id; the list below names each once, linked where the list has it. -->
            <p :title="refusedFiles.length ? stageRefusal.message : undefined">{{ refusedFiles.length ? t.migration.filesBlock(refusedFiles.length) : stageRefusal.message }}</p>
            <ul v-if="refusedFiles.length" class="flow-list">
              <li v-for="file in refusedFiles" :key="file" data-testid="migration-refused-file">
                <button v-if="fileKnown(file)" type="button" class="flow-link" :title="t.migration.openFile(name(file))" @click="emit('open-record', file)">{{ name(file) }}</button>
                <template v-else>{{ name(file) }}</template>
              </li>
            </ul>
          </PcNotice>
          <PcNotice v-if="stageError" tone="danger">{{ stageError }}</PcNotice>

          <!-- What migrate_record warned about, per record, with the way out: abandon, fix the step, migrate again. -->
          <PcNotice v-if="warningGroups.length" tone="warning" :title="t.migration.warningsTitle" data-testid="migration-warnings">
            <div v-for="group in warningGroups" :key="group.id" class="flow-warning-group">
              <p class="flow-warning-group__name">{{ name(group.id) }}</p>
              <ul class="flow-list">
                <li v-for="warning in group.warnings" :key="warning" data-testid="migration-warning">{{ warning }}</li>
              </ul>
              <p class="flow-note">{{ t.migration.warningsFix }}</p>
              <PcButton
                v-if="flowStaged.length"
                compact
                :aria-label="t.migration.abandonAndEditFor(name(group.id))"
                data-testid="migration-fix-steps"
                :disabled="staging || abandoning"
                @click="askAbandon(group.id)"
              >
                <template #icon><Pencil :size="13" aria-hidden="true" /></template>
                {{ t.migration.abandonAndEditShort }}
              </PcButton>
            </div>
          </PcNotice>

          <p v-if="resumed" class="flow-note" data-testid="migration-resumed">{{ t.migration.resumed(shortRevision(stagedRevisionOf(recordId))) }}</p>
          <div v-if="!staged" class="flow-actions">
            <PcButton
              variant="primary"
              :busy="staging"
              :disabled="!!stageDenied || filesBlock"
              :title="stageDenied || (filesBlock ? t.migration.filesBlockShort : t.migration.stageTitle)"
              data-testid="migration-stage"
              @click="stage()"
            >
              {{ t.migration.stage }}
            </PcButton>
            <p v-if="stageDenied" class="flow-note" data-testid="migration-denied">{{ stageDenied }}</p>
          </div>
        </template>

        <template v-else-if="mode === 'publish'">
          <p class="flow-prose" data-testid="publish-revisions">
            {{ record?.revision ? t.migration.publishOver(shortRevision(stagedRevisionOf(recordId)), shortRevision(record.revision)) : t.migration.publishFirst(shortRevision(stagedRevisionOf(recordId))) }}
          </p>
          <p class="flow-note">{{ t.migration.publishNote }}</p>
          <p v-if="gatheredBy.length" class="flow-note" data-testid="flow-gathered-by">{{ t.plans.stagedSave.gatheredBy(gatheredNames, gatheredBy.length) }}</p>
          <div v-if="closure.length > 1" class="flow-block">
            <h4>{{ t.migration.facts.collections }}</h4>
            <ul class="flow-list">
              <li v-for="id in closure.slice(1)" :key="id">{{ name(id) }}</li>
            </ul>
          </div>
        </template>

        <template v-else-if="mode === 'delete'">
          <p class="flow-prose" data-testid="delete-intro">{{ gatheredBy.length ? t.plans.stagedSave.deleteIntroGathered(gatheredNames, gatheredBy.length) : t.migration.deleteIntro }}</p>
          <div class="flow-block" data-testid="delete-shares">
            <h4>{{ t.migration.deleteSharesTitle(ownShares.length) }}</h4>
            <ul v-if="ownShares.length" class="flow-list">
              <li v-for="share in ownShares" :key="share.share_id" data-testid="delete-archived-share">
                {{ shareLabel(share) }}
                <span v-if="share.identity_id" class="flow-note">{{ t.migration.servesIdentity(identityName(share.identity_id)) }}</span>
              </li>
            </ul>
            <p v-else class="flow-note">{{ shares === undefined ? t.migration.sharesUnread : t.migration.noShares }}</p>
          </div>
          <div v-if="deleteDrops.length" class="flow-block" data-testid="delete-drops">
            <h4>{{ t.migration.deleteDropsTitle }}</h4>
            <ul class="flow-list">
              <li v-for="line in deleteDrops" :key="line" data-testid="delete-drop">{{ line }}</li>
            </ul>
          </div>
          <div v-if="byTag.length" class="flow-block">
            <h4>{{ t.migration.loseMember }}</h4>
            <ul class="flow-list">
              <li v-for="item in byTag" :key="item.id">{{ name(item.id) }}</li>
            </ul>
          </div>
          <PcNotice v-if="blockingCollections.length" tone="danger" :title="t.migration.explicitTitle" data-testid="delete-blocked">
            <p>{{ t.migration.explicitMembers }}</p>
            <ul class="flow-list">
              <li v-for="id in blockingCollections" :key="id" class="flow-row">
                <span>{{ name(id) }}</span>
                <PcButton compact data-testid="delete-remove-member" @click="emit('remove-member', id, recordId)">
                  {{ t.migration.removeFrom(name(id)) }}
                </PcButton>
              </li>
            </ul>
          </PcNotice>
        </template>
      </section>

      <!-- ── share identities ───────────────────────────────────────────── -->
      <section v-else-if="step === 'identities'" class="flow-section" aria-labelledby="flow-identities">
        <h3 id="flow-identities" class="flow-heading" tabindex="-1" data-step-heading>{{ t.migration.steps.identities }}</h3>
        <p class="flow-prose">{{ t.migration.identitiesIntro[mode] }}</p>
        <p v-if="rest" class="flow-note" data-testid="flow-rest">{{ t.migration.restNote(rest.map(name).join(", ")) }}</p>
        <p v-if="subs.identitiesError.value" class="flow-error" role="alert">{{ subs.identitiesError.value }}</p>
        <p v-else-if="!subs.canReadIdentities.value" class="flow-note" data-testid="identities-denied">{{ t.fleetEditor.denied.title }}</p>

        <p v-if="shares === undefined" class="flow-note">{{ t.migration.sharesUnread }}</p>
        <p v-else-if="!openShares.length && !boundShares.length" class="flow-note" data-testid="flow-no-shares">{{ t.migration.noSharesToDecide }}</p>

        <ul class="flow-shares">
          <li
            v-for="share in openShares.filter((entry) => !!decisions[entry.share_id])"
            :key="share.share_id"
            class="flow-share"
            data-testid="share-decision"
            :data-share="share.share_id"
          >
            <div class="flow-share__head">
              <span class="flow-share__name">{{ shareLabel(share) }}</span>
              <span v-if="multipleRecords()" class="flow-note">{{ name(share.subscription_id) }}</span>
            </div>

            <!-- An identity-bound share on a record that is not fleet-bound: the identity buys nothing. -->
            <template v-if="share.identity_id">
              <p class="flow-note" data-testid="share-darkened">{{ t.migration.darkened(identityName(share.identity_id)) }}</p>
              <fieldset class="flow-share__controls flow-choice">
                <legend class="pc-sr-only">{{ t.migration.choiceFor(shareLabel(share)) }}</legend>
                <label class="flow-check">
                  <input
                    type="radio"
                    :name="`share-choice-${share.share_id}`"
                    :checked="!decisions[share.share_id]!.clear && !decisions[share.share_id]!.archive"
                    data-testid="share-keep"
                    @change="setChoice(share, 'keep')"
                  />
                  {{ t.migration.keepIdentity }}
                </label>
                <label class="flow-check">
                  <input type="radio" :name="`share-choice-${share.share_id}`" :checked="decisions[share.share_id]!.clear === true" data-testid="share-clear" @change="setChoice(share, 'clear')" />
                  {{ t.migration.clearIdentity }}
                </label>
                <label class="flow-check">
                  <input type="radio" :name="`share-choice-${share.share_id}`" :checked="decisions[share.share_id]!.archive" data-testid="share-archive" @change="setChoice(share, 'archive')" />
                  {{ t.migration.archive }}
                </label>
              </fieldset>
            </template>

            <template v-else>
              <p v-if="isDecoy(share)" class="flow-note" data-testid="share-decoy">{{ t.migration.decoy }}</p>
              <div class="flow-share__controls">
                <label class="flow-select">
                  <span class="pc-sr-only">{{ t.migration.identityFor(shareLabel(share)) }}</span>
                  <select
                    v-model="decisions[share.share_id]!.identity"
                    class="pc-select"
                    :disabled="decisions[share.share_id]!.archive || !identities.length"
                    :aria-label="t.migration.identityFor(shareLabel(share))"
                    data-testid="share-identity"
                  >
                    <option value="">{{ t.migration.chooseIdentity }}</option>
                    <option v-for="identity in sortedIdentities" :key="identity.id" :value="identity.id">{{ identityOption(identity) }}</option>
                  </select>
                  <span v-if="identityFacts(share)" class="flow-note flow-select__facts" data-testid="identity-facts">{{ identityFacts(share) }}</span>
                </label>
                <label class="flow-check">
                  <input v-model="decisions[share.share_id]!.archive" type="checkbox" data-testid="share-archive" />
                  {{ t.migration.archive }}
                </label>
              </div>
              <p v-if="previewOf(share)?.status === 'loading'" class="flow-note">{{ t.fleetEditor.bind.loading }}</p>
              <p v-else-if="previewOf(share)?.status === 'error'" class="flow-error">{{ t.fleetEditor.bind.error }}</p>
              <p v-if="inactiveIdentity(share)" class="flow-warn" data-testid="identity-inactive">
                {{ t.migration.inactive(inactiveIdentity(share)!.name || inactiveIdentity(share)!.id, t.fleetEditor.identity.status[inactiveIdentity(share)!.status as keyof typeof t.fleetEditor.identity.status] ?? inactiveIdentity(share)!.status) }}
              </p>
              <div v-if="emptyFor(share)" class="flow-warn" data-testid="identity-no-lines">
                <p>{{ t.migration.noLines }}</p>
                <div class="flow-actions">
                  <PcButton
                    compact
                    :busy="bindingOf(share)?.busy"
                    :disabled="!previewOf(share)?.reply || bindingOf(share)?.unavailable === true"
                    data-testid="bind-selection"
                    @click="bindSelection(share)"
                  >
                    <template #icon><Link2 :size="13" aria-hidden="true" /></template>
                    {{ t.plans.bind.action }}
                  </PcButton>
                  <PcButton v-if="bindingOf(share)?.unavailable" compact :disabled="!canOpenApprovals" @click="openUsers()">
                    <template #icon><ExternalLink :size="13" aria-hidden="true" /></template>
                    {{ t.fleetEditor.bind.openUsers }}
                  </PcButton>
                </div>
                <p v-if="bindingOf(share)?.text" class="flow-note" data-testid="bind-selection-result" role="status">{{ bindingOf(share)!.text }}</p>
              </div>
            </template>
          </li>
        </ul>

        <div v-if="boundShares.length" class="flow-block" data-testid="flow-bound-shares">
          <h4>{{ t.migration.boundTitle }}</h4>
          <ul class="flow-list">
            <li v-for="share in boundShares" :key="share.share_id">
              {{ shareLabel(share) }} <span class="flow-note">{{ t.migration.servesIdentity(identityName(share.identity_id!)) }}</span>
            </li>
          </ul>
        </div>

        <PcNotice v-if="nameWarnings.length" tone="warning" :title="t.migration.warningsTitle" data-testid="migration-name-warnings">
          <ul class="flow-list">
            <li v-for="index in nameWarnings" :key="index" data-testid="migration-warning">{{ t.migration.nameStep(index) }}</li>
          </ul>
          <p class="flow-note">{{ t.migration.warningsFix }}</p>
          <PcButton
            v-if="flowStaged.length"
            compact
            :aria-label="t.migration.abandonAndEditFor(name(recordId))"
            data-testid="migration-fix-steps"
            :disabled="abandoning"
            @click="askAbandon(recordId)"
          >
            <template #icon><Pencil :size="13" aria-hidden="true" /></template>
            {{ t.migration.abandonAndEditShort }}
          </PcButton>
        </PcNotice>

        <p v-if="undecided.length" class="flow-warn" data-testid="shares-undecided" role="status">{{ t.migration.undecided(undecided.length) }}</p>
      </section>

      <!-- ── propose ────────────────────────────────────────────────────── -->
      <section v-else-if="step === 'propose'" class="flow-section" aria-labelledby="flow-propose-heading">
        <h3 id="flow-propose-heading" class="flow-heading" tabindex="-1" data-step-heading>{{ stepLabel("propose") }}</h3>
        <p class="flow-prose">{{ t.migration.proposeIntro[mode] }}</p>
        <ul class="flow-list" data-testid="propose-records">
          <li v-for="entry in proposeSummary.records" :key="entry.id" data-testid="propose-record" class="flow-propose-record">
            <span>{{ entry.name }}</span>
            <span class="flow-mono">{{ mode === 'delete' ? t.migration.deletes(shortRevision(entry.from)) : mode === 'assign' ? t.migration.stays(shortRevision(entry.from)) : t.migration.fromTo(shortRevision(entry.from) || t.plans.view.noLive, shortRevision(entry.to)) }}</span>
            <ul v-if="entry.warnings.length" class="flow-propose-warnings">
              <li v-for="warning in entry.warnings" :key="warning" class="flow-warn" data-testid="propose-warning">{{ warning }}</li>
            </ul>
            <!-- The steps this record's staged revision changes, which core lists in the plan too. -->
            <div v-if="stepSummary[entry.id]" class="flow-propose-steps" data-testid="propose-steps" :data-record="entry.id" :data-status="stepSummary[entry.id]!.status">
              <p v-if="stepSummary[entry.id]!.status === 'loading'" class="flow-note">{{ t.plans.chain.proposeReading }}</p>
              <p v-else-if="stepSummary[entry.id]!.status === 'error'" class="flow-note">{{ t.plans.chain.proposeUnread }}</p>
              <p v-else-if="!stepSummary[entry.id]!.changes.length" class="flow-note" data-testid="propose-steps-unchanged">{{ t.plans.chain.proposeUnchanged }}</p>
              <ul v-else class="flow-propose-chain">
                <li
                  v-for="(change, index) in stepSummary[entry.id]!.changes"
                  :key="index"
                  data-testid="propose-step-line"
                  :data-verb="change.kind === 'step' ? change.verb : change.kind"
                  :data-position="change.kind === 'step' ? change.position : undefined"
                >
                  <span>{{ chainChangeText(change) }}</span>
                  <span
                    v-if="flaggedLine(entry.id, change)"
                    class="badge"
                    data-tone="danger"
                    data-testid="propose-step-flag"
                    :data-certain="refusalStep?.certain ? 'true' : 'false'"
                    :title="refusalStep?.certain ? t.plans.scriptNetwork.flagTitle : t.plans.scriptNetwork.flagCandidateTitle"
                  >{{ refusalStep?.certain ? t.plans.scriptNetwork.flag : t.plans.scriptNetwork.flagCandidate }}</span>
                </li>
              </ul>
            </div>
            <ScriptNetworkNotice
              v-if="networkRefusal && refusalRecord === entry.id"
              class="flow-propose-refusal"
              :refusal="networkRefusal"
              :record-name="name(entry.id)"
              :step="refusalStep"
              :can-open="true"
              :primary="refusalStands"
              :other-record="proposeSummary.records.length > 1"
              @open-step="openRefusedStep"
            />
          </li>
        </ul>
        <p v-if="anyStepChange" class="flow-note" data-testid="propose-steps-note">{{ t.plans.chain.proposeNote }}</p>
        <!-- A refusal about a record the summary does not list (a combination the plan covers). -->
        <ScriptNetworkNotice
          v-if="networkRefusal && !refusalListed"
          :refusal="networkRefusal"
          :record-name="name(refusalRecord)"
          :step="refusalStep"
          :can-open="byId.has(refusalRecord)"
          :primary="refusalStands && byId.has(refusalRecord)"
          other-record
          @open-step="openRefusedStep"
        />
        <ul v-if="proposeSummary.assigned.length || proposeSummary.cleared.length || proposeSummary.archived.length" class="flow-list" data-testid="propose-changes">
          <li v-for="line in [...proposeSummary.assigned, ...proposeSummary.cleared, ...proposeSummary.archived]" :key="line">{{ line }}</li>
        </ul>
        <div class="flow-actions">
          <PcButton
            :variant="refusalStands ? 'secondary' : mode === 'delete' ? 'danger' : 'primary'"
            :busy="proposing"
            :disabled="!!proposeDenied || refusalStands"
            :title="proposeDenied || (refusalStands ? t.plans.scriptNetwork.proposeBlocked : undefined)"
            data-testid="flow-propose"
            @click="propose()"
          >
            {{ t.migration.proposeVerb[mode] }}
          </PcButton>
        </div>
        <p v-if="refusalStands && !proposeDenied" class="flow-note" data-testid="propose-refusal-blocked">{{ t.plans.scriptNetwork.proposeBlocked }}</p>
        <p v-if="mode === 'delete' && !proposeDenied" class="flow-note" data-testid="propose-proof">{{ t.migration.deleteProof }}</p>
        <p v-if="proposeDenied" class="flow-note" data-testid="propose-denied">{{ proposeDenied }}</p>
      </section>

      <!-- ── status ─────────────────────────────────────────────────────── -->
      <section v-else class="flow-section" aria-labelledby="flow-status">
        <h3 id="flow-status" class="flow-heading" tabindex="-1" data-step-heading>{{ t.migration.steps.status }}</h3>
        <PcNotice v-if="published" tone="success" data-testid="flow-published">{{ published }}</PcNotice>
        <template v-else-if="planRow">
          <p v-if="partialOf" class="flow-note">{{ t.migration.partialNote }}</p>
          <PlanStatus
            :plan="planRow"
            :plan-text="planText"
            :record-revisions="statusRecords"
            detail
            :records="items"
            :identities="identities"
            :busy="checking"
            :can-propose="subs.canPropose.value"
            :can-open-approvals="canOpenApprovals"
            @refresh="refreshPlan()"
            @propose-again="proposeAgain()"
            @propose-rest="proposeRest"
            @open-approvals="openApprovals"
          />
        </template>
        <p v-else class="flow-note">{{ t.migration.noPlan }}</p>
      </section>

      <!-- Abandon asks first, here, beside where it was asked for: what it drops, and the plan it stales. -->
      <div v-if="abandonAsk" ref="abandonBox" class="flow-abandon-ask" role="group" aria-labelledby="flow-abandon-heading" data-testid="abandon-confirm">
        <h4 id="flow-abandon-heading" class="flow-abandon-ask__title" tabindex="-1" data-abandon-heading>{{ t.migration.abandonTitle }}</h4>
        <ul class="flow-list">
          <li v-for="line in abandonLines" :key="line" data-testid="abandon-line">{{ line }}</li>
        </ul>
        <p v-if="abandonFinish" class="flow-note" data-testid="abandon-finish">{{ abandonFinish }}</p>
        <p v-if="abandonAsk.edit" class="flow-note">{{ t.migration.abandonThenEdit(name(abandonAsk.edit)) }}</p>
        <p v-if="abandonError" class="flow-error" role="alert">{{ abandonError }}</p>
        <div class="flow-actions">
          <PcButton compact variant="danger" :busy="abandoning" data-testid="abandon-run" @click="abandon()">
            {{ abandonAsk.edit ? t.migration.abandonAndEditVerb : t.migration.abandon }}
          </PcButton>
          <PcButton compact :disabled="abandoning" data-testid="abandon-cancel" @click="abandonAsk = null">{{ t.migration.abandonKeep }}</PcButton>
        </div>
      </div>

      <footer class="flow-footer">
        <PcButton v-if="stepIndex > 0 && step !== 'status'" compact data-testid="flow-back" @click="back()">
          <template #icon><ArrowLeft :size="13" aria-hidden="true" /></template>
          {{ t.migration.back }}
        </PcButton>
        <span class="flow-footer__gap" />
        <p v-if="footerWhy" class="flow-note flow-footer__why" data-testid="flow-why">{{ footerWhy }}</p>
        <PcButton
          v-if="step !== 'propose' && step !== 'status'"
          compact
          variant="primary"
          :disabled="!!nextBlocked"
          data-testid="flow-next"
          @click="next()"
        >
          {{ t.migration.next }}
          <ArrowRight :size="13" aria-hidden="true" />
        </PcButton>
        <PcButton v-if="step === 'status'" compact data-testid="flow-close" @click="emit('close')">{{ t.migration.close }}</PcButton>
      </footer>
      <!-- Away from Back and Next, in a quieter slot of its own. -->
      <div v-if="flowStaged.length && !abandonAsk" class="flow-abandon">
        <PcButton compact :disabled="staging || abandoning" data-testid="flow-abandon" @click="askAbandon()">
          <template #icon><Undo2 :size="13" aria-hidden="true" /></template>
          {{ t.migration.abandonAsk }}
        </PcButton>
        <span class="flow-note" data-testid="flow-abandon-note">{{ abandonNote }}</span>
      </div>
    </div>
  </PcSidePanel>
</template>

<style scoped>
.flow {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  min-width: 0;
}
.flow-steps {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2) var(--space-4);
  margin: 0;
  padding: 0 0 var(--space-3);
  list-style: none;
  border-bottom: 1px solid var(--border);
}
.flow-step {
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
  font-size: var(--lt-text-sm);
  color: var(--muted-foreground);
}
.flow-step.is-current {
  color: var(--foreground);
  font-weight: 600;
}
.flow-step__mark {
  display: inline-grid;
  place-items: center;
  width: 20px;
  height: 20px;
  border-radius: 50%;
  border: 1px solid var(--border);
  font-family: var(--font-mono);
  font-size: var(--lt-text-xs);
}
.flow-step.is-current .flow-step__mark {
  border-color: var(--lt-accent-border);
  background: var(--lt-accent-soft);
  color: var(--lt-accent-ink);
}
.flow-step.is-done .flow-step__mark {
  border-color: var(--lt-ok-border);
  background: var(--lt-ok-soft);
}
.flow-section {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  min-width: 0;
}
.flow-heading {
  margin: 0;
  font-size: var(--lt-text-lg);
  font-weight: 600;
}
.flow-heading:focus-visible {
  outline: none;
  box-shadow: var(--lt-focus-ring);
  border-radius: var(--radius-sm);
}
.flow-prose {
  margin: 0;
  max-width: var(--lt-measure-prose);
  font-size: var(--lt-text-sm);
  line-height: var(--lt-leading);
}
.flow-note {
  margin: 0;
  font-size: var(--lt-text-xs);
  color: var(--muted-foreground);
  max-width: var(--lt-measure-prose);
}
.flow-error {
  margin: 0;
  font-size: var(--lt-text-sm);
  color: var(--lt-danger-ink);
}
.flow-warn {
  margin: 0;
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-3);
  border-left: 2px solid var(--warning);
  background: var(--lt-warn-soft);
  border-radius: var(--radius-sm);
  font-size: var(--lt-text-sm);
}
.flow-warn p {
  margin: 0;
}
.flow-mono {
  font-family: var(--font-mono);
  font-size: var(--lt-text-xs);
  color: var(--muted-foreground);
  overflow-wrap: anywhere;
}
.flow-facts {
  display: grid;
  grid-template-columns: minmax(0, 1fr);
  gap: var(--space-2);
  margin: 0;
}
.flow-facts > div {
  display: grid;
  grid-template-columns: minmax(120px, 180px) minmax(0, 1fr);
  gap: var(--space-3);
  font-size: var(--lt-text-sm);
}
.flow-facts dt {
  color: var(--muted-foreground);
}
.flow-facts dd {
  margin: 0;
  min-width: 0;
  overflow-wrap: anywhere;
}
.flow-block {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
}
.flow-block h4 {
  margin: 0;
  font-size: var(--lt-text-sm);
  font-weight: 600;
}
.flow-list {
  margin: 0;
  padding: 0;
  list-style: none;
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  font-size: var(--lt-text-sm);
}
.flow-list li {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: var(--space-1) var(--space-3);
  min-width: 0;
  overflow-wrap: anywhere;
}
.flow-row {
  justify-content: space-between;
}
.flow-actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
}
.flow-shares {
  margin: 0;
  padding: 0;
  list-style: none;
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}
.flow-share {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding: var(--space-3);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  background: var(--card);
  min-width: 0;
}
.flow-share__head {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: var(--space-1) var(--space-3);
}
.flow-share__name {
  font-weight: 600;
  overflow-wrap: anywhere;
}
.flow-share__controls {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2) var(--space-4);
}
.flow-select {
  flex: 1 1 260px;
  min-width: 0;
  max-width: 100%;
}
.flow-select select {
  width: 100%;
  max-width: 100%;
}
.flow-check {
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
  font-size: var(--lt-text-sm);
}
.flow-footer {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
  padding-top: var(--space-3);
  border-top: 1px solid var(--border);
}
.flow-footer__gap {
  flex: 1 1 auto;
}
.flow-footer__why {
  flex: 1 1 200px;
  text-align: right;
}
.flow-step:not(.is-current):not(.is-done) .flow-step__mark {
  border-style: dashed;
}
.flow-error-inline {
  font-size: var(--lt-text-xs);
  color: var(--lt-danger-ink);
}
.flow-link {
  padding: 0;
  border: 0;
  background: none;
  color: var(--lt-accent-ink);
  font: inherit;
  text-align: left;
  text-decoration: underline;
  text-underline-offset: 2px;
  cursor: pointer;
}
.flow-link:focus-visible {
  outline: none;
  box-shadow: var(--lt-focus-ring);
  border-radius: var(--radius-sm);
}
.flow-warning-group {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: var(--space-1);
}
.flow-warning-group + .flow-warning-group {
  margin-top: var(--space-3);
}
.flow-warning-group__name {
  margin: 0;
  font-weight: 600;
}
.flow-choice {
  margin: 0;
  padding: 0;
  border: 0;
  min-width: 0;
}
.flow-select__facts {
  display: block;
  margin-top: var(--space-1);
}
.flow-propose-record {
  flex-direction: row;
}
.flow-propose-steps {
  flex: 1 1 100%;
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  min-width: 0;
}
.flow-propose-chain {
  margin: 0;
  padding-left: var(--space-4);
  font-size: var(--lt-text-sm);
}
.flow-propose-chain li {
  overflow-wrap: anywhere;
}
.flow-propose-chain .badge {
  margin-left: var(--space-2);
  white-space: nowrap;
}
.flow-propose-refusal {
  flex: 1 1 100%;
}
.flow-propose-warnings {
  flex: 1 1 100%;
  margin: 0;
  padding: 0;
  list-style: none;
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
}
.flow-abandon-ask {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding: var(--space-3);
  border: 1px solid var(--lt-danger-border, var(--border));
  border-left: 2px solid var(--destructive);
  border-radius: var(--radius-md);
  background: var(--card);
}
.flow-abandon-ask__title {
  margin: 0;
  font-size: var(--lt-text-sm);
  font-weight: 600;
}
.flow-abandon-ask__title:focus-visible {
  outline: none;
  box-shadow: var(--lt-focus-ring);
  border-radius: var(--radius-sm);
}
/* Below 480px a notice's action takes a line of its own under the body,
   rather than a right-hand column that squeezes the reason to two words a line. */
@media (max-width: 479px) {
  .flow-notice-stack {
    flex-wrap: wrap;
  }
  .flow-notice-stack :deep(.pc-notice-actions) {
    flex-basis: 100%;
    margin-left: calc(17px + var(--space-2));
  }
}
.flow-abandon {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2) var(--space-3);
  padding-top: var(--space-2);
}
</style>
