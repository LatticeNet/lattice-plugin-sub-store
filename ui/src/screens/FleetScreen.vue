<script setup lang="ts">
import { computed, nextTick, onActivated, onMounted, ref, watch } from "vue";
import { ChevronDown, ChevronRight, ExternalLink, Link2, Network, Send, SquareArrowOutUpRight, UserCheck } from "@lucide/vue";
import {
  PcButton,
  PcEmptyState,
  PcNotice,
  PcPanel,
  PcPanelBody,
  PcPanelHeader,
  PcSkeleton,
  PcStateDot,
  useOverlayEscape,
  type StateTone,
} from "@latticenet/plugin-bridge/chassis";

import PlanStatus from "../components/PlanStatus.vue";
import StagedBadge from "../components/StagedBadge.vue";
import { BINDINGS, ERROR_CODES, KIND_COLLECTION, SOURCE_FLEET, callMethod, errorCodeOf, type SubscriptionListItem } from "../client";
import {
  IDENTITY_ACTIVE,
  VPN_CORE_USERS_ROUTE,
  groupExclusions,
  identityInactive,
  probeUnavailable,
  shortVersion,
  type BindPreviewReply,
  type FleetPreviewReply,
  type Identity,
} from "../fleet";
import { useHost } from "../host";
import { compareText, formatCount, t } from "../i18n";
import { useLensChrome } from "../lensChrome";
import { hostOriginFromHash, postNavigate, sharesRoute } from "../navigate";
import {
  currentPlan,
  decoyShares,
  isFleetBound,
  linesToBind,
  liveSharesOn,
  recordName,
  shortRevision,
  type PlanShareRow,
} from "../planFlow";
import { recordIntent } from "../recordIntent";
import { safeErrorMessage } from "../subStoreModel";
import { sourceKindLabel } from "../pipeline";
import { usePipeline } from "../usePipeline";
import { useSubscriptions } from "../useSubscriptions";

/**
 * The Fleet view (s2-plan section 11): which identities receive what from
 * each fleet-bound record. Left, the fleet records and the combinations that
 * gather them, from the list's own flags. Right, for the chosen record,
 * vpn-core's identities as rows (name, status, bound lines); a row asks
 * core's bind.preview for that one identity when it is opened, and "Preview
 * all" asks for each in turn, never every identity of every record at once.
 * A previewed row says how many entries the identity's share would receive
 * and why the rest are left out, as chips by reason.
 *
 * From a row the operator can open the console's share form for the record
 * (only the console mints a share and reveals its link once, and core admits
 * an identity on a fleet-bound record alone), or file one batch of binding
 * approvals for the lines the identity still lacks. The proof line names
 * the revision previewed and the catalogue version. Switching between the
 * live and the staged revision asks again for every open row, so the two
 * can be compared row by row. An identity vpn-core does not hold active
 * says, collapsed, that its share answers the decoy, whatever its lines.
 */
const host = useHost();
const subs = useSubscriptions(host);
const pipe = usePipeline(host);
const chrome = useLensChrome();
const intent = recordIntent(host);
useOverlayEscape();

const shares = computed(() => pipe.shareStore.shares.value as PlanShareRow[] | undefined);

/** Fleet records live or staged, and the combinations a fleet member makes fleet-bound. */
const records = computed<SubscriptionListItem[]>(() =>
  pipe.items.value
    .filter((item) => isFleetBound(item) || item.source === SOURCE_FLEET || item.staged_source === SOURCE_FLEET)
    .sort((a, b) => Number(a.kind === KIND_COLLECTION) - Number(b.kind === KIND_COLLECTION) || compareText(recordName(a), recordName(b))),
);

const chosenId = ref("");
const chosen = computed(() => records.value.find((item) => item.id === chosenId.value) ?? records.value[0]);
watch(
  records,
  (list) => {
    if (!list.some((item) => item.id === chosenId.value)) chosenId.value = list[0]?.id ?? "";
  },
  { immediate: true },
);

// ── what this session can read and do ──────────────────────────────────────

const canIdentities = computed(() => subs.canReadIdentities.value);
const canPreview = computed(() => subs.canBindPreview.value);
const canCatalogue = computed(() => host.available(BINDINGS.subFleetPreview));
const denied = computed(() => !!host.init.value && (!canIdentities.value || !canPreview.value));
const shareOrigin = computed(() => hostOriginFromHash(typeof window === "undefined" ? "" : window.location.hash));

// ── the catalogue, for the proof line ──────────────────────────────────────

const catalogue = ref<FleetPreviewReply | null>(null);
const catalogueError = ref("");
async function readCatalogue(): Promise<void> {
  if (!host.bridge || !canCatalogue.value) return;
  catalogueError.value = "";
  try {
    catalogue.value = await callMethod<FleetPreviewReply>(host.bridge, BINDINGS.subFleetPreview, { process: [], sample: 0 }).promise;
  } catch (cause) {
    catalogue.value = null;
    catalogueError.value = errorCodeOf(cause) === ERROR_CODES.catalogueUnavailable ? t.fleet.catalogueUnavailable : safeErrorMessage(cause, t.fleet.catalogueUnavailable);
  }
}

// ── which revision is previewed ────────────────────────────────────────────

type Revision = "live" | "staged";
const revisionChoice = ref<Revision>("live");
const hasLive = computed(() => !!chosen.value?.revision);
const hasStaged = computed(() => !!chosen.value?.staged_revision);
const revision = computed<Revision>(() => (!hasLive.value && hasStaged.value ? "staged" : hasStaged.value ? revisionChoice.value : "live"));
const previewedRevision = computed(() => (revision.value === "staged" ? chosen.value?.staged_revision ?? "" : chosen.value?.revision ?? ""));
/** The detail panel: on a narrow frame it sits under the list, so a choice there brings it into view. */
const detailPanel = ref<{ $el?: HTMLElement } | null>(null);
const NARROW = "(max-width: 959px)";
watch(chosenId, (id, previous) => {
  revisionChoice.value = "live";
  expanded.value = new Set();
  // A plan over the record, staged or not: a failed apply is said here with the live revision beside it.
  if (id) void subs.loadPlans([id]);
  if (!previous || !id || typeof window === "undefined" || !window.matchMedia?.(NARROW).matches) return;
  void nextTick(() => {
    const element = detailPanel.value?.$el;
    if (!element) return;
    element.scrollIntoView({ block: "start", behavior: window.matchMedia("(prefers-reduced-motion: reduce)").matches ? "auto" : "smooth" });
    element.focus({ preventScroll: true });
  });
});

// ── identities and their previews ──────────────────────────────────────────

const identities = computed<Identity[]>(() =>
  [...(subs.identities.value ?? [])].sort(
    (a, b) => Number(b.status === IDENTITY_ACTIVE) - Number(a.status === IDENTITY_ACTIVE) || compareText(a.name || a.id, b.name || b.id),
  ),
);

interface Preview {
  status: "loading" | "ready" | "error";
  reply?: BindPreviewReply;
  error?: string;
}
const previews = ref<Record<string, Preview>>({});
const keyOf = (identityId: string) => `${chosen.value?.id ?? ""}\u0000${previewedRevision.value}\u0000${identityId}`;
const previewOf = (identityId: string) => previews.value[keyOf(identityId)];

function previewError(cause: unknown): string {
  const code = errorCodeOf(cause);
  if (code === ERROR_CODES.catalogueUnavailable) return t.fleet.catalogueUnavailable;
  if (code === ERROR_CODES.fleetResponseChainUnavailable) return t.fleetEditor.transformer.title;
  if (code === ERROR_CODES.ownerCredentialsWithheld) return t.fleetEditor.withheld.body;
  return safeErrorMessage(cause, t.fleetEditor.bind.error);
}

async function preview(identityId: string, force = false): Promise<void> {
  const record = chosen.value;
  if (!record || !canPreview.value) return;
  const key = keyOf(identityId);
  if (!force && previews.value[key] && previews.value[key]!.status !== "error") return;
  previews.value = { ...previews.value, [key]: { status: "loading" } };
  try {
    const reply = await subs.bindPreview(record.id, identityId, revision.value === "staged" ? record.staged_revision : undefined);
    previews.value = { ...previews.value, [key]: { status: "ready", reply } };
  } catch (cause) {
    previews.value = { ...previews.value, [key]: { status: "error", error: previewError(cause) } };
  }
}

const expanded = ref<Set<string>>(new Set());
// The other revision: every open row asks again, so the rows compare live and staged one for one.
watch(previewedRevision, () => {
  for (const id of expanded.value) void preview(id);
});
function toggle(identityId: string): void {
  const next = new Set(expanded.value);
  if (next.has(identityId)) next.delete(identityId);
  else {
    next.add(identityId);
    void preview(identityId);
  }
  expanded.value = next;
}

/** One identity after another, two at a time: each is a render and a bind in core. */
const previewingAll = ref(false);
async function previewAll(): Promise<void> {
  if (previewingAll.value) return;
  previewingAll.value = true;
  try {
    const queue = identities.value.map((identity) => identity.id);
    const worker = async () => {
      for (let id = queue.shift(); id !== undefined; id = queue.shift()) await preview(id);
    };
    await Promise.all([worker(), worker()]);
  } finally {
    previewingAll.value = false;
  }
}

function counts(reply: BindPreviewReply): { included: number; excluded: number } {
  return { included: reply.entries.length, excluded: reply.excluded.length };
}
function reasonLabel(reason: string): string {
  if (reason.startsWith("plan_rejected:")) return t.fleetEditor.bind.rejected(reason.slice("plan_rejected:".length));
  return (t.fleetEditor.bind.reasons as Record<string, string>)[reason] ?? reason;
}
function chipsOf(reply: BindPreviewReply) {
  return groupExclusions(reply.excluded).map((group) => ({ reason: group.reason, label: reasonLabel(group.reason), count: group.entries.length }));
}
function groupsOf(reply: BindPreviewReply) {
  return groupExclusions(reply.excluded).map((group) => {
    const fixes = new Set(group.entries.map((entry) => entry.fix ?? ""));
    return { reason: group.reason, label: reasonLabel(group.reason), fix: fixes.size === 1 ? [...fixes][0]! : "", entries: group.entries };
  });
}
/** An identity vpn-core does not hold active answers the decoy, so its row says that before any count. */
function decoyAnswer(identity: Identity): string {
  return identityInactive(identity.status) ? t.fleet.answersDecoy(statusText(identity.status)) : "";
}
function identityTone(identity: Identity): StateTone {
  if (identity.status === IDENTITY_ACTIVE) return "healthy";
  if (identity.status === "expired" || identity.status === "disabled") return "error";
  return "warning";
}
function statusText(status: string): string {
  return (t.fleetEditor.identity.status as Record<string, string>)[status] ?? status;
}
function inactiveText(status: string): string {
  const table = t.fleetEditor.bind.inactive as Record<string, string>;
  return table[status] ?? table.other!;
}
function refusedText(code: string): string {
  const table = t.fleetEditor.bind.refused as Record<string, string>;
  return table[code] ?? table.other!;
}

// ── actions on an identity row ─────────────────────────────────────────────

const actionNote = ref<{ identity: string; text: string; tone: "success" | "danger" | "info"; users?: boolean } | null>(null);

/**
 * The console's share form for the record. The console admits no identity
 * on that route today, so the identity to choose there is named here.
 */
function createShare(identity: Identity): void {
  const record = chosen.value;
  if (!record || !shareOrigin.value) return;
  postNavigate(window, sharesRoute(record.id), shareOrigin.value);
  actionNote.value = { identity: identity.id, text: t.fleet.askedShareForm(recordName(record), identity.name || identity.id), tone: "info" };
}

const bindingBusy = ref("");
async function bindSelection(identity: Identity): Promise<void> {
  const reply = previewOf(identity.id)?.reply;
  if (!reply) return;
  bindingBusy.value = identity.id;
  try {
    const result = await subs.bindSelection(identity.id, linesToBind(reply));
    actionNote.value = result.ok
      ? {
          identity: identity.id,
          text: t.plans.bind.filed(result.reply.filed.length, result.reply.batch, result.reply.skipped.length),
          tone: "success",
        }
      : { identity: identity.id, text: result.message, tone: result.unavailable ? "info" : "danger", users: result.unavailable };
    if (!result.ok && result.unavailable) bindUnavailable.value = true;
  } finally {
    bindingBusy.value = "";
  }
}

function openApprovals(approvalId: string): void {
  if (shareOrigin.value) postNavigate(window, `/approvals?open=${encodeURIComponent(approvalId)}`, shareOrigin.value);
}

function openUsers(): void {
  if (shareOrigin.value) postNavigate(window, VPN_CORE_USERS_ROUTE, shareOrigin.value);
}

/** The Records screen holds the plan flow: hand a record's plan verb over to it. */
function handOver(action: "share-identities" | "publish-staged"): void {
  const record = chosen.value;
  if (!record) return;
  chrome.openLens("records");
  intent.value = { recordId: record.id, action };
}

const createBlocked = computed(() => {
  const record = chosen.value;
  if (!record) return "";
  if (!isFleetBound(record)) return t.fleet.createNeedsLive;
  if (!shareOrigin.value) return t.fleet.noConsole;
  return "";
});
/** Why a share for this identity would serve nothing: the record's reason, else the identity's own. */
function createBlockedFor(identity: Identity): string {
  return createBlocked.value || (identityInactive(identity.status) ? t.fleet.createInactive(statusText(identity.status)) : "");
}
/** bind_selection answered unknown method (decision 8 not taken): asking again would answer the same. */
const bindUnavailable = ref(false);
const bindBlocked = computed(() => (subs.canBindSelection.value ? "" : t.plans.bind.needsAdmin));

// ── the chosen record's shares and plan ────────────────────────────────────

const recordShares = computed(() => (chosen.value ? liveSharesOn([chosen.value.id], shares.value) : []));
const decoys = computed(() => (chosen.value ? decoyShares(chosen.value, shares.value) : []));
const plan = computed(() => (chosen.value ? currentPlan(subs.plansByRecord.value[chosen.value.id] ?? [], chosen.value) : undefined));
const tooLarge = computed(() => {
  const error = chosen.value?.last_error ?? "";
  return /snapshot_too_large/.test(error) ? safeErrorMessage(new Error(error), "") : "";
});
/**
 * Selected lines with no client shape core can bind (shadowsocks among
 * them): render drops them, so no identity receives them. Read from the
 * first preview of this record: the selection's size against the fleet
 * nodes core bound.
 */
const dropped = computed(() => {
  const record = chosen.value;
  if (!record || record.kind === KIND_COLLECTION || typeof record.nodes_in !== "number") return 0;
  const reply = identities.value.map((identity) => previewOf(identity.id)?.reply).find((entry) => !!entry);
  return reply ? Math.max(0, record.nodes_in - reply.fleet_nodes) : 0;
});

const stale = computed(() => {
  const served = chosen.value?.selection_version;
  const now = catalogue.value?.catalogue_version;
  return served && now && served !== now ? { served, now } : null;
});

const proof = computed(() => {
  const parts: string[] = [];
  if (catalogue.value) {
    parts.push(t.fleetEditor.proof.catalogue(shortVersion(catalogue.value.catalogue_version)));
    parts.push(t.fleetEditor.proof.lines(catalogue.value.count));
    if (probeUnavailable(catalogue.value)) parts.push(t.fleetEditor.proof.probeUnavailable);
  }
  if (chosen.value) parts.push(previewedRevision.value ? t.fleet.revisionPreviewed(shortRevision(previewedRevision.value)) : t.fleet.noRevision);
  parts.push(t.fleet.identityCount(identities.value.length));
  return parts;
});

// ── loading ────────────────────────────────────────────────────────────────

function load(): void {
  if (!host.init.value) return;
  void subs.loadIdentities();
  void readCatalogue();
  void pipe.shareStore.load();
  if (pipe.catalogue.state.value === "idle") void pipe.catalogue.reload();
  const staged = records.value.filter((item) => item.staged_revision).map((item) => item.id);
  if (chosen.value) staged.push(chosen.value.id);
  if (staged.length) void subs.loadPlans(staged);
}
onMounted(load);
onActivated(load);
watch(host.init, (value) => {
  if (value) load();
});
watch(
  () => records.value.filter((item) => item.staged_revision).map((item) => item.id).join(","),
  (ids) => {
    if (ids) void subs.loadPlans(ids.split(","));
  },
);
</script>

<template>
  <section class="lens fleet" aria-labelledby="fleet-title" data-testid="fleet-view">
    <h2 id="fleet-title" class="pc-sr-only">{{ t.fleet.title }}</h2>

    <PcPanel v-if="!host.init.value || pipe.catalogue.state.value === 'loading' || pipe.catalogue.state.value === 'idle'" :label="t.fleet.loadingPanel" data-testid="fleet-loading">
      <PcSkeleton :count="5" :label="t.fleet.loading" />
    </PcPanel>

    <PcNotice v-else-if="pipe.catalogue.state.value === 'error'" tone="danger" :title="t.fleet.recordsFailed" data-testid="fleet-records-error">
      {{ pipe.catalogue.loadError.value }}
      <template #actions><PcButton compact @click="pipe.catalogue.reload()">{{ t.common.tryAgain }}</PcButton></template>
    </PcNotice>

    <PcPanel v-else-if="!records.length" :label="t.fleet.title" data-testid="fleet-empty">
      <PcEmptyState :title="t.fleet.emptyTitle">
        <template #icon><Network :size="26" aria-hidden="true" /></template>
        <p>{{ t.fleet.emptyBody }}</p>
        <template #actions>
          <PcButton @click="chrome.openLens('records', { kind: 'source' }, { focus: true })">{{ t.fleet.goToRecords }}</PcButton>
        </template>
      </PcEmptyState>
    </PcPanel>

    <template v-else>
      <PcNotice v-if="denied" tone="warning" :title="t.fleetEditor.denied.title" data-testid="fleet-denied">
        {{ t.fleet.deniedBody }}
      </PcNotice>

      <div class="fleet-layout">
        <!-- The fleet-bound records. A radio group: it picks what the right side shows. -->
        <PcPanel class="fleet-records" :label="t.fleet.recordsTitle">
          <PcPanelHeader :title="t.fleet.recordsTitle" :description="t.fleet.recordsDescription" />
          <div class="fleet-record-list" role="radiogroup" :aria-label="t.fleet.recordsTitle">
            <label
              v-for="item in records"
              :key="item.id"
              class="fleet-record"
              :class="{ 'is-selected': chosen?.id === item.id }"
              data-testid="fleet-record"
              :data-record="item.id"
            >
              <input v-model="chosenId" type="radio" name="fleet-record" :value="item.id" class="pc-sr-only" :aria-label="recordName(item)" />
              <span class="fleet-record__name">{{ recordName(item) }}</span>
              <span class="fleet-record__facts">
                <span>{{ item.kind === KIND_COLLECTION ? t.kinds.combination : sourceKindLabel(item) }}</span>
                <span v-if="typeof item.nodes_in === 'number'">{{ t.fleetEditor.proof.lines(item.nodes_in) }}</span>
                <span v-if="!item.revision" class="fleet-record__new">{{ t.fleet.notLive }}</span>
              </span>
              <StagedBadge v-if="item.staged_revision" :item="item" :plans="subs.plansByRecord.value[item.id]" />
            </label>
          </div>
        </PcPanel>

        <!-- The chosen record: identities as rows. -->
        <PcPanel v-if="chosen" ref="detailPanel" class="fleet-detail" tabindex="-1" :label="t.fleet.detailLabel(recordName(chosen))" data-testid="fleet-detail">
          <PcPanelHeader :title="recordName(chosen)" :description="t.fleet.detailDescription">
            <PcButton
              compact
              :busy="previewingAll"
              :disabled="!canPreview || !identities.length"
              :title="canPreview ? t.fleet.previewAllTitle : t.fleetEditor.denied.title"
              data-testid="fleet-preview-all"
              @click="previewAll()"
            >
              {{ t.fleet.previewAll }}
            </PcButton>
          </PcPanelHeader>
          <PcPanelBody>

          <p class="fleet-proof" data-testid="fleet-proof">
            <span v-for="(part, index) in proof" :key="index" class="fleet-proof__seg">{{ part }}</span>
          </p>

          <fieldset v-if="hasLive && hasStaged" class="fleet-revision">
            <legend class="pc-sr-only">{{ t.fleet.revisionLegend }}</legend>
            <label :class="{ 'is-selected': revision === 'live' }">
              <input v-model="revisionChoice" type="radio" name="fleet-revision" value="live" data-testid="fleet-revision-live" />
              {{ t.fleetEditor.identity.revisionLive }}
            </label>
            <label :class="{ 'is-selected': revision === 'staged' }">
              <input v-model="revisionChoice" type="radio" name="fleet-revision" value="staged" data-testid="fleet-revision-staged" />
              {{ t.fleetEditor.identity.revisionStaged(shortRevision(chosen.staged_revision, 8)) }}
            </label>
          </fieldset>

          <PcNotice v-if="catalogueError" tone="danger" :title="t.fleetEditor.error.title" data-testid="fleet-catalogue-error">
            {{ catalogueError }}
            <template #actions><PcButton compact @click="readCatalogue()">{{ t.fleetEditor.retry }}</PcButton></template>
          </PcNotice>
          <PcNotice v-if="stale" tone="info" data-testid="fleet-stale">
            {{ t.fleetEditor.stale(shortVersion(stale.served), shortVersion(stale.now)) }}
          </PcNotice>
          <PcNotice v-if="tooLarge" tone="danger" :title="t.fleetEditor.tooLarge.title" data-testid="fleet-too-large">{{ tooLarge }}</PcNotice>
          <p v-if="dropped" class="fleet-note" data-testid="fleet-dropped">{{ t.fleet.dropped(dropped) }}</p>

          <!-- What the record's shares get now. -->
          <div class="fleet-shares" data-testid="fleet-shares">
            <p class="fleet-shares__line">
              {{ shares === undefined ? t.fleet.sharesUnread : t.fleet.shareCount(recordShares.length) }}
            </p>
            <div v-if="decoys.length" class="fleet-decoy" data-testid="fleet-decoy">
              <p>{{ t.fleet.decoy(decoys.length) }}</p>
              <PcButton compact :disabled="!subs.canPropose.value" :title="subs.canPropose.value ? undefined : t.plans.cannotPropose" data-testid="fleet-assign" @click="handOver('share-identities')">
                <template #icon><UserCheck :size="13" aria-hidden="true" /></template>
                {{ t.plans.actions.shareIdentities }}
              </PcButton>
            </div>
            <div v-if="chosen.staged_revision || plan" class="fleet-staged" data-testid="fleet-staged">
              <StagedBadge v-if="chosen.staged_revision" :item="chosen" :plans="subs.plansByRecord.value[chosen.id]" proof :hide-state="!!plan" />
              <PlanStatus v-if="plan" :plan="plan" detail :records="pipe.items.value" :identities="identities" :can-propose="subs.canPropose.value" :can-open-approvals="!!shareOrigin" @refresh="subs.loadPlans([chosen.id], true)" @propose-again="handOver('publish-staged')" @propose-rest="handOver('publish-staged')" @open-approvals="openApprovals" />
              <PcButton
                v-if="chosen.staged_revision && (!plan || plan.status !== 'pending')"
                compact
                :disabled="!subs.canPropose.value"
                :title="subs.canPropose.value ? undefined : t.plans.cannotPropose"
                data-testid="fleet-publish"
                @click="handOver('publish-staged')"
              >
                <template #icon><Send :size="13" aria-hidden="true" /></template>
                {{ t.plans.actions.publishStaged }}
              </PcButton>
            </div>
          </div>

          <PcSkeleton v-if="canIdentities && subs.identities.value === undefined && !subs.identitiesError.value" :count="4" :label="t.fleetEditor.identity.loading" />
          <PcNotice v-else-if="subs.identitiesError.value" tone="danger" data-testid="fleet-identities-error">
            {{ subs.identitiesError.value }}
            <template #actions><PcButton compact @click="subs.loadIdentities(true)">{{ t.fleetEditor.retry }}</PcButton></template>
          </PcNotice>
          <p v-else-if="canIdentities && !identities.length" class="fleet-note">{{ t.fleetEditor.identity.none }}</p>

          <ul v-if="identities.length" class="fleet-identities" :aria-label="t.fleetEditor.identity.listLabel">
            <li
              v-for="identity in identities"
              :key="identity.id"
              class="fleet-identity"
              :class="{ 'is-open': expanded.has(identity.id) }"
              data-testid="fleet-identity"
              :data-identity="identity.id"
            >
              <div class="fleet-identity__row">
                <button
                  type="button"
                  class="fleet-identity__toggle"
                  :aria-expanded="expanded.has(identity.id)"
                  :disabled="!canPreview"
                  :title="canPreview ? t.fleet.previewOne(identity.name || identity.id) : t.fleetEditor.denied.title"
                  data-testid="fleet-identity-toggle"
                  @click="toggle(identity.id)"
                >
                  <component :is="expanded.has(identity.id) ? ChevronDown : ChevronRight" :size="14" aria-hidden="true" />
                  <span class="fleet-identity__name">{{ identity.name || identity.id }}</span>
                </button>
                <span class="fleet-identity__status"><PcStateDot :tone="identityTone(identity)" :label="statusText(identity.status)" /></span>
                <span class="fleet-identity__bound">{{ t.fleetEditor.identity.boundLines(identity.bound_lines) }}</span>
                <span class="fleet-identity__result" data-testid="fleet-identity-result" :data-decoy="decoyAnswer(identity) ? 'true' : undefined">
                  <template v-if="decoyAnswer(identity)">{{ decoyAnswer(identity) }}</template>
                  <template v-else-if="previewOf(identity.id)?.status === 'loading' || (expanded.has(identity.id) && !previewOf(identity.id))">{{ t.fleetEditor.bind.loading }}</template>
                  <template v-else-if="previewOf(identity.id)?.status === 'error'"><span class="fleet-error">{{ t.fleetEditor.bind.error }}</span></template>
                  <template v-else-if="previewOf(identity.id)?.reply">
                    {{ t.fleet.counts(counts(previewOf(identity.id)!.reply!).included, counts(previewOf(identity.id)!.reply!).excluded) }}
                  </template>
                </span>
              </div>

              <ul v-if="previewOf(identity.id)?.reply?.excluded.length" class="fleet-chips" :aria-label="t.fleet.reasonsLabel">
                <li v-for="chip in chipsOf(previewOf(identity.id)!.reply!)" :key="chip.reason" class="fleet-chip" data-testid="fleet-reason-chip" :data-reason="chip.reason">
                  {{ chip.label }} · {{ formatCount(chip.count) }}
                </li>
              </ul>

              <div v-if="expanded.has(identity.id)" class="fleet-identity__body" data-testid="fleet-identity-body">
                <p v-if="identityInactive(identity.status)" class="fleet-warn" data-testid="fleet-identity-inactive">{{ inactiveText(identity.status) }}</p>
                <p v-if="previewOf(identity.id)?.status === 'error'" class="fleet-error" role="alert" data-testid="fleet-preview-error">{{ previewOf(identity.id)!.error }}</p>
                <template v-else-if="previewOf(identity.id)?.reply">
                  <p v-if="previewOf(identity.id)!.reply!.refused" class="fleet-warn" data-testid="fleet-preview-refused">
                    {{ t.fleetEditor.bind.refused.title }}: {{ refusedText(previewOf(identity.id)!.reply!.refused!) }}
                  </p>
                  <ul class="fleet-groups">
                    <li v-for="group in groupsOf(previewOf(identity.id)!.reply!)" :key="group.reason" data-testid="fleet-exclusion-group" :data-reason="group.reason">
                      <span class="fleet-groups__reason">{{ group.label }} · {{ formatCount(group.entries.length) }}</span>
                      <span v-if="group.fix" class="fleet-groups__fix">{{ group.fix }}</span>
                      <span class="fleet-groups__names">{{ group.entries.slice(0, 8).map((entry) => entry.name || entry.line_uuid).join(", ") }}<template v-if="group.entries.length > 8"> {{ t.plans.view.andMore(group.entries.length - 8) }}</template></span>
                    </li>
                  </ul>
                </template>

                <div class="fleet-identity__actions">
                  <PcButton
                    compact
                    :disabled="!!createBlockedFor(identity)"
                    :title="createBlockedFor(identity) || t.fleet.createShareTitle"
                    data-testid="fleet-create-share"
                    @click="createShare(identity)"
                  >
                    <template #icon><SquareArrowOutUpRight :size="13" aria-hidden="true" /></template>
                    {{ t.fleet.createShare }}
                  </PcButton>
                  <PcButton
                    v-if="previewOf(identity.id)?.reply && linesToBind(previewOf(identity.id)!.reply!).length"
                    compact
                    :busy="bindingBusy === identity.id"
                    :disabled="!!bindBlocked || bindUnavailable"
                    :title="bindBlocked || (bindUnavailable ? t.plans.bind.unavailable : t.plans.bind.title)"
                    data-testid="fleet-bind-selection"
                    @click="bindSelection(identity)"
                  >
                    <template #icon><Link2 :size="13" aria-hidden="true" /></template>
                    {{ t.plans.bind.action }}
                  </PcButton>
                </div>
                <p v-if="createBlockedFor(identity) && createBlockedFor(identity) !== t.fleet.noConsole" class="fleet-note" data-testid="fleet-create-blocked">{{ createBlockedFor(identity) }}</p>
                <p class="fleet-note">{{ t.fleet.narrowingNote }}</p>
                <div v-if="actionNote && actionNote.identity === identity.id" class="fleet-action-note" :data-tone="actionNote.tone" role="status" data-testid="fleet-action-note">
                  <span>{{ actionNote.text }}</span>
                  <PcButton v-if="actionNote.users" compact :disabled="!shareOrigin" @click="openUsers()">
                    <template #icon><ExternalLink :size="13" aria-hidden="true" /></template>
                    {{ t.fleetEditor.bind.openUsers }}
                  </PcButton>
                </div>
              </div>
            </li>
          </ul>
          </PcPanelBody>
        </PcPanel>
      </div>
    </template>
  </section>
</template>

<style scoped>
.fleet-layout {
  display: grid;
  grid-template-columns: minmax(0, 1fr);
  gap: var(--space-4);
  align-items: start;
}
@media (min-width: 960px) {
  .fleet-layout {
    grid-template-columns: minmax(240px, 320px) minmax(0, 1fr);
  }
}
.fleet-record-list {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
}
.fleet-record {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: var(--space-2) var(--space-3);
  border-radius: var(--radius-md);
  border: 1px solid transparent;
  cursor: pointer;
  min-width: 0;
}
.fleet-record:hover {
  background: var(--lt-neutral-soft);
}
.fleet-record.is-selected {
  border-color: var(--lt-accent-border);
  background: var(--lt-accent-soft);
}
.fleet-record:focus-within {
  box-shadow: var(--lt-focus-ring);
}
.fleet-record__name {
  font-weight: 600;
  font-size: var(--lt-text-sm);
  overflow-wrap: anywhere;
}
.fleet-record__facts {
  display: flex;
  flex-wrap: wrap;
  gap: 0 var(--space-2);
  font-size: var(--lt-text-xs);
  color: var(--muted-foreground);
}
.fleet-record__new {
  font-family: var(--font-mono);
}
.fleet-proof {
  display: flex;
  flex-wrap: wrap;
  gap: 0 var(--space-2);
  margin: 0 0 var(--space-3);
  font-family: var(--font-mono);
  font-size: var(--lt-text-xs);
  color: var(--muted-foreground);
}
.fleet-proof__seg + .fleet-proof__seg::before {
  content: "· ";
}
.fleet-revision {
  display: inline-flex;
  flex-wrap: wrap;
  gap: var(--space-2);
  margin: 0 0 var(--space-3);
  padding: 0;
  border: 0;
}
.fleet-revision label {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  padding: 2px var(--space-2);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  font-size: var(--lt-text-xs);
  cursor: pointer;
}
.fleet-revision label.is-selected {
  border-color: var(--lt-accent-border);
  background: var(--lt-accent-soft);
}
.fleet-shares {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  margin-bottom: var(--space-3);
}
.fleet-shares__line,
.fleet-note {
  margin: 0;
  font-size: var(--lt-text-xs);
  color: var(--muted-foreground);
  max-width: var(--lt-measure-prose);
}
.fleet-decoy,
.fleet-staged {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2) var(--space-3);
  padding: var(--space-2) var(--space-3);
  border-radius: var(--radius-sm);
}
.fleet-staged {
  flex-direction: column;
  align-items: flex-start;
}
.fleet-decoy {
  border-left: 2px solid var(--warning);
  background: var(--lt-warn-soft);
}
.fleet-decoy p {
  margin: 0;
  flex: 1 1 260px;
  font-size: var(--lt-text-sm);
}
.fleet-staged {
  border: 1px solid var(--border);
}
.fleet-identities {
  margin: 0;
  padding: 0;
  list-style: none;
  border-top: 1px solid var(--border);
}
.fleet-identity {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  padding: var(--space-2) 0;
  border-bottom: 1px solid var(--border);
  min-width: 0;
}
.fleet-identity__row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  gap: var(--space-1) var(--space-3);
  align-items: center;
}
/* Narrow: the name and its status on the first line, the bound lines and
   the counts under the name, so a long count never squeezes the name. */
@media (max-width: 719px) {
  .fleet-identity__bound,
  .fleet-identity__result {
    grid-column: 1 / -1;
    padding-left: calc(14px + var(--space-1));
  }
}
.fleet-detail:focus {
  outline: none;
}
.fleet-detail:focus-visible {
  box-shadow: var(--lt-focus-ring);
}
.fleet-identity__result[data-decoy] {
  color: var(--warning-text);
  font-family: inherit;
}
@media (min-width: 720px) {
  .fleet-identity__row {
    grid-template-columns: minmax(0, 2fr) 110px 120px minmax(0, 1.4fr);
  }
}
.fleet-identity__toggle {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  min-width: 0;
  padding: 2px 0;
  border: 0;
  background: none;
  color: inherit;
  font: inherit;
  font-size: var(--lt-text-sm);
  text-align: left;
  cursor: pointer;
}
.fleet-identity__toggle:focus-visible {
  outline: none;
  box-shadow: var(--lt-focus-ring);
  border-radius: var(--radius-sm);
}
.fleet-identity__toggle:disabled {
  cursor: default;
}
.fleet-identity__name {
  font-weight: 600;
  overflow-wrap: anywhere;
}
.fleet-identity__bound,
.fleet-identity__result {
  font-size: var(--lt-text-xs);
  color: var(--muted-foreground);
  font-family: var(--font-mono);
}
.fleet-chips {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-1);
  margin: 0;
  padding: 0 0 0 var(--space-5);
  list-style: none;
}
.fleet-chip {
  padding: 1px var(--space-2);
  border-radius: var(--radius-sm);
  background: var(--lt-neutral-soft);
  font-size: var(--lt-text-xs);
}
.fleet-identity__body {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding: var(--space-2) 0 var(--space-1) var(--space-5);
}
.fleet-groups {
  margin: 0;
  padding: 0;
  list-style: none;
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}
.fleet-groups li {
  display: flex;
  flex-direction: column;
  gap: 2px;
  font-size: var(--lt-text-xs);
}
.fleet-groups__reason {
  font-weight: 600;
}
.fleet-groups__fix {
  color: var(--muted-foreground);
}
.fleet-groups__names {
  overflow-wrap: anywhere;
}
.fleet-identity__actions {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
}
.fleet-warn {
  margin: 0;
  padding: var(--space-1) var(--space-2);
  border-left: 2px solid var(--warning);
  background: var(--lt-warn-soft);
  font-size: var(--lt-text-sm);
}
.fleet-error {
  color: var(--lt-danger-ink);
}
.fleet-action-note {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
  font-size: var(--lt-text-sm);
}
</style>
