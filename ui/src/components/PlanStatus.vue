<script setup lang="ts">
import { computed } from "vue";
import { ChevronRight, ExternalLink, RefreshCw } from "@lucide/vue";
import { PcButton, PcStateDot, type StateTone } from "@latticenet/plugin-bridge/chassis";

import type { SubscriptionListItem } from "../client";
import { chainChangeText } from "../chainText";
import type { BindLine, Plan } from "../core-types";
import { planState, type Identity, type PlanStatus } from "../fleet";
import { formatCount, formatDateTime, t } from "../i18n";
import {
  chainOnlyPlan,
  mayHavePromoted,
  parsePlan,
  partialApply,
  planChainSections,
  planRevisionLines,
  planShareViews,
  planUnboundShares,
  promotedAfterFailure,
  recordName,
  shareLabel,
  shortRevision,
} from "../planFlow";

/**
 * A plan's state, and with `detail` what the plan does (s2-plan sections 2.8
 * and 11). The chip reads planState(): core's status, "stale" from core's
 * own boolean, and "failed" for a plan approved whose apply failed, which
 * core records as approved with the failure as its reason and the chip
 * never calls approved. The detail adds what fixes each state: the
 * console's Approvals for a pending plan, "propose again" for a stale one,
 * and for a failed one the record's live revision from the list, since
 * core says the record may have been promoted and that the list shows
 * whether it was. A multi-record plan that stopped part way names the
 * records that applied, whose shares already serve their identities, and
 * offers "propose the rest".
 *
 * With the plan's text, the view lists what the approver is shown: per
 * share, the lines its identity gains and loses and the lines left out with
 * the reason and the fix, then the assignments, clearings and archivals. A
 * share that receives nothing, or has lines left out, starts open. Before
 * the shares, each record's process steps that the plan adds, removes or
 * changes, as core lists them (S2-SR-2): a step can change what a share
 * serves without moving a line, so a plan whose only change is its steps
 * says that in so many words. Without
 * the text (plans.status carries none, so a plan reopened later has only
 * its row) the view says the content is in Approvals. Every record the plan
 * promotes is named with its revisions: from the text's sections, else from
 * `recordRevisions`, what the surface knows the plan covers.
 * No credential and no share link is in any of it.
 */
const props = defineProps<{
  plan: PlanStatus | null;
  /** The approval's plan text, when the surface has it. */
  planText?: string;
  detail?: boolean;
  /** The record list, for names and for a failed plan's live revision. */
  records?: readonly SubscriptionListItem[];
  identities?: readonly Identity[];
  busy?: boolean;
  /** Whether this session may propose: "propose again" and "propose the rest" need it. */
  canPropose?: boolean;
  /** The console's origin is known, so Approvals can be opened there. */
  canOpenApprovals?: boolean;
  /** The records the plan promotes, when the surface knows them and has no plan text. */
  recordRevisions?: readonly { id: string; from: string; to: string }[];
}>();

const emit = defineEmits<{
  refresh: [];
  "propose-again": [];
  "propose-rest": [applied: string[]];
  "open-approvals": [approvalId: string];
}>();

const state = computed(() => (props.plan ? planState(props.plan) : null));
/** Approved and applied, rejected, or stale: the chain-only warning's "before approving" no longer applies. */
const decided = computed(() => state.value === "applied" || state.value === "rejected" || state.value === "stale");
const tone = computed<StateTone>(() => {
  switch (state.value) {
    case "applied":
      return "healthy";
    case "approved":
      return "info";
    case "pending":
    case "stale":
      return "warning";
    case "failed":
    case "rejected":
      return "error";
    default:
      return "neutral";
  }
});
const label = computed(() => (state.value ? t.plans.state[state.value] : ""));

const parsed = computed<Plan | null>(() => parsePlan(props.planText));
const partial = computed(() => (state.value === "failed" ? partialApply(props.plan?.reason) : null));
const promoted = computed(() => {
  if (state.value !== "failed" || !props.plan || partial.value) return null;
  const live = props.records?.find((item) => item.id === props.plan!.subscription_id)?.revision;
  return { live: live ?? "", verdict: promotedAfterFailure(props.plan, live), says: mayHavePromoted(props.plan.reason) };
});

function nameOf(id: string): string {
  return recordName(props.records?.find((item) => item.id === id), id);
}
function identityName(id: string): string {
  const found = props.identities?.find((identity) => identity.id === id);
  return found?.name || id;
}

const shares = computed(() => (parsed.value ? planShareViews(parsed.value) : []));
const chainSections = computed(() => (parsed.value ? planChainSections(parsed.value) : []));
const chainOnly = computed(() => (parsed.value ? chainOnlyPlan(parsed.value) : false));

/** One line per record the plan moves: the text's sections, else what the surface knows, else the row's own. */
const revisionLines = computed(() => {
  if (!props.plan) return [];
  if (parsed.value) return planRevisionLines(parsed.value);
  const known = (props.recordRevisions ?? []).filter((entry) => entry.to);
  if (known.length > 1 && known.some((entry) => entry.id === props.plan!.subscription_id)) return known;
  return [{ id: props.plan.subscription_id, from: props.plan.from_revision, to: props.plan.to_revision }];
});

/** A share whose identity receives nothing, or that has lines left out: what the approver must see first. */
function startsOpen(share: { added: unknown[]; unchanged: number; excluded: unknown[] }): boolean {
  return (share.added.length === 0 && share.unchanged === 0) || share.excluded.length > 0;
}
// An archived share answers nothing, decoy or not; the archivals list above names it.
const unbound = computed(() => (parsed.value ? planUnboundShares(parsed.value).filter((share) => !share.archive) : []));
const multiRecord = computed(() => new Set(shares.value.map((share) => share.record)).size > 1 || (parsed.value?.records?.length ?? 0) > 1);

/** The excluded lines of one share, grouped by reason, each group with the fix the reason calls for. */
function groups(lines: BindLine[]): { reason: string; label: string; fix: string; lines: BindLine[] }[] {
  const byReason = new Map<string, BindLine[]>();
  for (const line of lines) {
    const reason = line.reason || "other";
    byReason.set(reason, [...(byReason.get(reason) ?? []), line]);
  }
  const labels = t.fleetEditor.bind.reasons as Record<string, string>;
  const fixes = t.plans.view.fixes as Record<string, string>;
  return [...byReason].map(([reason, entries]) => ({
    reason,
    label: reason.startsWith("plan_rejected:") ? t.fleetEditor.bind.rejected(reason.slice("plan_rejected:".length)) : labels[reason] ?? reason,
    fix: fixes[reason] ?? (reason.startsWith("plan_rejected:") ? fixes.plan_rejected! : fixes.other!),
    lines: entries,
  }));
}

function lineName(line: { name?: string; names?: string[]; line_uuid?: string }): string {
  return line.name || line.names?.[0] || line.line_uuid || "";
}
</script>

<template>
  <div v-if="plan" class="plan-status" :data-state="state" data-testid="plan-status">
    <div class="plan-status__head">
      <span class="plan-chip" :data-state="state" data-testid="plan-chip">
        <PcStateDot :tone="tone" :label="label" />
      </span>
      <template v-if="detail">
        <span class="plan-status__id" :title="t.plans.view.approvalTitle">{{ plan.approval_id }}</span>
        <span class="plan-status__when">{{ t.plans.view.proposedAt(formatDateTime(Date.parse(plan.created_at))) }}</span>
      </template>
    </div>

    <template v-if="detail">
      <ul class="plan-status__revisions" data-testid="plan-revisions">
        <li v-for="line in revisionLines" :key="line.id" data-testid="plan-revision">
          {{ plan.action === "delete" ? t.plans.view.deleteOf(nameOf(line.id), shortRevision(line.from)) : t.plans.view.revisions(nameOf(line.id), shortRevision(line.from) || t.plans.view.noLive, shortRevision(line.to)) }}
        </li>
      </ul>

      <p v-if="state === 'pending'" class="plan-status__note">{{ t.plans.view.pendingNote }}</p>
      <p v-else-if="state === 'approved'" class="plan-status__note">{{ t.plans.view.approvedNote }}</p>
      <p v-else-if="state === 'applied'" class="plan-status__note">{{ plan.action === "delete" ? t.plans.view.appliedDeleteNote : t.plans.view.appliedNote }}</p>
      <p v-else-if="state === 'rejected'" class="plan-status__note">{{ t.plans.view.rejectedNote }}</p>

      <p v-if="plan.reason" class="plan-status__reason" data-testid="plan-reason">
        <span class="plan-status__reason-label">{{ t.plans.view.coreSays }}</span>
        {{ plan.reason }}
      </p>

      <!-- A multi-record plan that stopped part way. -->
      <div v-if="partial" class="plan-status__partial" data-testid="plan-partial">
        <p>{{ t.plans.view.partialApplied(partial.applied.map(nameOf).join(", ") || t.plans.view.noneApplied) }}</p>
        <p>{{ t.plans.view.partialFailed(nameOf(partial.failed)) }}</p>
        <p class="plan-status__note">{{ t.plans.view.partialNote }}</p>
      </div>

      <!-- A failed plan: core cannot say whether the record moved; the list can. -->
      <p v-if="promoted" class="plan-status__promoted" data-testid="plan-live-revision">
        {{ t.plans.view.liveNow(shortRevision(promoted.live) || t.plans.view.noLive) }}
        <strong v-if="promoted.verdict === true">{{ t.plans.view.wasPromoted }}</strong>
        <strong v-else-if="promoted.verdict === false">{{ t.plans.view.notPromoted }}</strong>
        <strong v-else>{{ t.plans.view.unclear }}</strong>
      </p>

      <div class="plan-status__actions">
        <PcButton
          v-if="state === 'pending' || state === 'approved'"
          compact
          :disabled="!canOpenApprovals"
          :title="canOpenApprovals ? t.plans.view.openApprovalsTitle : t.plans.view.noConsole"
          data-testid="plan-open-approvals"
          @click="emit('open-approvals', plan.approval_id)"
        >
          <template #icon><ExternalLink :size="13" aria-hidden="true" /></template>
          {{ t.plans.view.openApprovals }}
        </PcButton>
        <PcButton
          v-if="partial"
          compact
          variant="primary"
          :disabled="!canPropose || busy"
          :title="canPropose ? undefined : t.plans.cannotPropose"
          data-testid="plan-propose-rest"
          @click="emit('propose-rest', partial.applied)"
        >
          {{ t.plans.view.proposeRest }}
        </PcButton>
        <PcButton
          v-else-if="state === 'stale' || (state === 'failed' && promoted?.verdict !== true) || state === 'rejected'"
          compact
          variant="primary"
          :disabled="!canPropose || busy"
          :title="canPropose ? undefined : t.plans.cannotPropose"
          data-testid="plan-propose-again"
          @click="emit('propose-again')"
        >
          {{ t.plans.view.proposeAgain }}
        </PcButton>
        <PcButton compact :busy="busy" data-testid="plan-refresh" @click="emit('refresh')">
          <template #icon><RefreshCw :size="13" aria-hidden="true" /></template>
          {{ t.plans.view.checkAgain }}
        </PcButton>
      </div>
      <p v-if="!parsed && (state === 'pending' || state === 'approved')" class="plan-status__note" data-testid="plan-content-elsewhere">{{ t.plans.view.contentInApprovals }}</p>

      <!-- What the approver is shown. -->
      <section v-if="parsed" class="plan-view" :aria-label="t.plans.view.title" data-testid="plan-view">
        <h4 class="plan-view__title">{{ t.plans.view.title }}</h4>
        <p class="plan-view__totals" data-testid="plan-totals">
          {{ t.plans.view.totals(parsed.totals.shares, parsed.totals.identities, parsed.totals.added, parsed.totals.removed, parsed.totals.excluded) }}
        </p>
        <p v-if="parsed.coverage" class="plan-view__coverage">{{ parsed.coverage }}</p>

        <!-- The steps each record runs, which no line count shows. -->
        <div v-if="chainSections.length" class="plan-view__chain" data-testid="plan-chain" :data-only="chainOnly ? 'true' : undefined">
          <h5 class="plan-view__subtitle">{{ t.plans.chain.title }}</h5>
          <p
            v-if="chainOnly"
            class="plan-view__chain-only"
            :class="{ 'is-decided': decided }"
            data-testid="plan-chain-only"
            :data-decided="decided ? 'true' : undefined"
          >
            {{ decided ? t.plans.chain.onlyChainDecided : t.plans.chain.onlyChain }}
          </p>
          <p v-else class="plan-view__chain-intro">{{ t.plans.chain.intro }}</p>
          <div v-for="section in chainSections" :key="section.record" class="plan-chain" data-testid="plan-chain-section" :data-record="section.record">
            <p v-if="chainSections.length > 1 || multiRecord" class="plan-chain__record">{{ nameOf(section.record) }}</p>
            <ul class="plan-chain__lines">
              <li
                v-for="(change, index) in section.changes"
                :key="index"
                data-testid="plan-chain-line"
                :data-verb="change.kind === 'step' ? change.verb : change.kind"
                :data-position="change.kind === 'step' ? change.position : undefined"
              >
                {{ chainChangeText(change) }}
              </li>
            </ul>
          </div>
        </div>

        <ul v-if="parsed.assignments?.length || parsed.clearings?.length || parsed.archivals?.length" class="plan-view__changes" data-testid="plan-changes">
          <li v-for="entry in parsed.assignments ?? []" :key="`a-${entry.share_id}`" data-testid="plan-assignment">
            {{ t.plans.view.assignment(shareLabel(entry), identityName(entry.identity_id), entry.lines) }}
          </li>
          <li v-for="entry in parsed.clearings ?? []" :key="`c-${entry.share_id}`" data-testid="plan-clearing">
            {{ t.plans.view.clearing(shareLabel(entry), identityName(entry.identity_id)) }}
          </li>
          <li v-for="entry in parsed.archivals ?? []" :key="`r-${entry.share_id}`" data-testid="plan-archival">
            {{ t.plans.view.archival(shareLabel(entry)) }}
          </li>
        </ul>

        <ul class="plan-view__shares">
          <li v-for="share in shares" :key="`${share.record}-${share.share_id}`" class="plan-share" data-testid="plan-share" :data-share="share.share_id">
            <details :open="startsOpen(share)">
              <summary class="plan-share__head">
                <ChevronRight class="plan-share__chevron" :size="14" aria-hidden="true" />
                <span class="plan-share__name">{{ shareLabel(share) }}</span>
                <span v-if="multiRecord" class="plan-share__record">{{ nameOf(share.record) }}</span>
                <span class="plan-share__identity">{{ identityName(share.identity_id) }}</span>
                <span v-if="share.assigned" class="plan-share__tag">{{ t.plans.view.assigned }}</span>
                <span v-if="share.archive" class="plan-share__tag">{{ t.plans.view.archived }}</span>
                <span class="plan-share__counts" data-testid="plan-share-counts">
                  {{ t.plans.view.shareCounts(share.added.length, share.removed.length, share.changed.length, share.excluded.length) }}
                </span>
              </summary>
              <div class="plan-share__body">
                <p v-if="!share.added.length && !share.removed.length && !share.changed.length" class="plan-share__none">
                  {{ t.plans.view.unchangedLines(share.unchanged) }}
                </p>
                <template v-if="share.added.length">
                  <h5>{{ t.plans.view.receives(share.added.length) }}</h5>
                  <ul class="plan-share__lines" data-testid="plan-received">
                    <li v-for="line in share.added" :key="line.line_uuid">{{ lineName(line) }}</li>
                  </ul>
                </template>
                <template v-if="share.removed.length">
                  <h5>{{ t.plans.view.loses(share.removed.length) }}</h5>
                  <ul class="plan-share__lines">
                    <li v-for="line in share.removed" :key="line.line_uuid">{{ lineName(line) }}</li>
                  </ul>
                </template>
                <template v-if="share.excluded.length">
                  <h5>{{ t.plans.view.leftOut(share.excluded.length) }}</h5>
                  <ul class="plan-share__groups">
                    <li v-for="group in groups(share.excluded)" :key="group.reason" data-testid="plan-excluded" :data-reason="group.reason">
                      <span class="plan-share__reason">{{ group.label }} · {{ formatCount(group.lines.length) }}</span>
                      <span class="plan-share__fix">{{ group.fix }}</span>
                      <span class="plan-share__names">{{ group.lines.slice(0, 6).map(lineName).join(", ") }}<template v-if="group.lines.length > 6"> {{ t.plans.view.andMore(group.lines.length - 6) }}</template></span>
                    </li>
                  </ul>
                </template>
              </div>
            </details>
          </li>
        </ul>

        <div v-if="unbound.length" class="plan-view__unbound" data-testid="plan-unbound">
          <p>{{ t.plans.view.unbound(unbound.length) }}</p>
          <ul>
            <li v-for="share in unbound" :key="share.share_id">{{ shareLabel(share) }}</li>
          </ul>
        </div>
      </section>
    </template>
  </div>
</template>

<style scoped>
.plan-status {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  min-width: 0;
}
.plan-status__head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2) var(--space-3);
}
.plan-status__id,
.plan-status__when,
.plan-status__revisions {
  font-size: var(--lt-text-xs);
  color: var(--muted-foreground);
}
.plan-status__id {
  font-family: var(--font-mono);
  overflow-wrap: anywhere;
}
.plan-status__revisions {
  margin: 0;
  padding: 0;
  list-style: none;
  font-family: var(--font-mono);
  overflow-wrap: anywhere;
}
.plan-status__note,
.plan-status__reason,
.plan-status__promoted,
.plan-status__partial p {
  margin: 0;
  font-size: var(--lt-text-sm);
  max-width: var(--lt-measure-prose);
}
.plan-status__note {
  color: var(--muted-foreground);
}
.plan-status__reason-label {
  font-weight: 600;
  margin-right: var(--space-1);
}
.plan-status__partial {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  padding: var(--space-2) var(--space-3);
  border-left: 2px solid var(--warning);
  background: var(--lt-warn-soft);
  border-radius: var(--radius-sm);
}
.plan-status__actions {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
}
.plan-view {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding-top: var(--space-3);
  border-top: 1px solid var(--border);
}
.plan-view__title {
  margin: 0;
  font-size: var(--lt-text-sm);
  font-weight: 600;
}
.plan-view__totals,
.plan-view__coverage {
  margin: 0;
  font-size: var(--lt-text-xs);
  color: var(--muted-foreground);
  max-width: var(--lt-measure-prose);
}
.plan-view__chain {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
}
.plan-view__subtitle {
  margin: 0;
  font-size: var(--lt-text-xs);
  font-weight: 600;
  color: var(--muted-foreground);
}
.plan-view__chain-intro {
  margin: 0;
  font-size: var(--lt-text-xs);
  color: var(--muted-foreground);
  max-width: var(--lt-measure-prose);
}
/* The one thing an approver must read when no line moves: drawn as the partial-apply notice is. */
.plan-view__chain-only {
  margin: 0;
  padding: var(--space-2) var(--space-3);
  border-left: 2px solid var(--warning);
  background: var(--lt-warn-soft);
  border-radius: var(--radius-sm);
  font-size: var(--lt-text-sm);
  max-width: var(--lt-measure-prose);
}
/* A decided plan's note is a fact about what it did, not a warning to act on. */
.plan-view__chain-only.is-decided {
  border-left-color: var(--border);
  background: var(--muted);
}
.plan-chain__record {
  margin: var(--space-1) 0 0;
  font-size: var(--lt-text-xs);
  font-weight: 600;
  overflow-wrap: anywhere;
}
.plan-chain__lines {
  margin: 0;
  padding-left: var(--space-4);
  font-size: var(--lt-text-sm);
  overflow-wrap: anywhere;
}
.plan-view__changes,
.plan-view__shares,
.plan-view__unbound ul {
  margin: 0;
  padding: 0;
  list-style: none;
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
}
.plan-view__changes li {
  font-size: var(--lt-text-sm);
}
.plan-share {
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  background: var(--card);
}
.plan-share__head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  list-style: none;
  gap: var(--space-1) var(--space-3);
  padding: var(--space-2) var(--space-3);
  cursor: pointer;
  font-size: var(--lt-text-sm);
}
.plan-share__head::-webkit-details-marker {
  display: none;
}
/* The disclosure the flex summary hides, drawn as the Fleet view draws its rows'. */
.plan-share__chevron {
  flex: none;
  color: var(--muted-foreground);
  transition: transform 120ms ease-out;
}
details[open] > .plan-share__head .plan-share__chevron {
  transform: rotate(90deg);
}
@media (prefers-reduced-motion: reduce) {
  .plan-share__chevron {
    transition: none;
  }
}
.plan-share__head:focus-visible {
  outline: none;
  box-shadow: var(--lt-focus-ring);
  border-radius: var(--radius-md);
}
.plan-share__name {
  font-weight: 600;
  overflow-wrap: anywhere;
}
.plan-share__record,
.plan-share__identity {
  color: var(--muted-foreground);
  overflow-wrap: anywhere;
}
.plan-share__tag {
  font-size: var(--lt-text-xs);
  padding: 0 var(--space-1);
  border-radius: var(--radius-sm);
  background: var(--lt-neutral-soft);
}
.plan-share__counts {
  margin-left: auto;
  font-family: var(--font-mono);
  font-size: var(--lt-text-xs);
  color: var(--muted-foreground);
}
.plan-share__body {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  padding: 0 var(--space-3) var(--space-3);
}
.plan-share__body h5 {
  margin: var(--space-2) 0 0;
  font-size: var(--lt-text-xs);
  font-weight: 600;
  color: var(--muted-foreground);
}
.plan-share__none {
  margin: 0;
  font-size: var(--lt-text-xs);
  color: var(--muted-foreground);
}
.plan-share__lines {
  margin: 0;
  padding-left: var(--space-4);
  font-size: var(--lt-text-xs);
  overflow-wrap: anywhere;
}
.plan-share__groups {
  margin: 0;
  padding: 0;
  list-style: none;
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}
.plan-share__groups li {
  display: flex;
  flex-direction: column;
  gap: 2px;
  font-size: var(--lt-text-xs);
}
.plan-share__reason {
  font-weight: 600;
}
.plan-share__fix {
  color: var(--muted-foreground);
}
.plan-share__names {
  overflow-wrap: anywhere;
}
.plan-view__unbound p {
  margin: 0;
  font-size: var(--lt-text-sm);
}
</style>
