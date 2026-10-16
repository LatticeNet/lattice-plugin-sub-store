<script setup lang="ts">
import { computed } from "vue";

import type { SubscriptionListItem } from "../client";
import { planState, type PlanStatus } from "../fleet";
import { t } from "../i18n";
import { rowPlan, shortRevision, stagedFacts, stagedPlanState } from "../planFlow";

/**
 * A record's staged revision, where the Published column states what its
 * shares serve (s2-plan section 11). A fleet-bound record's edit waits in
 * staged-v2 until core promotes it, so the column says both facts: what is
 * live, and what waits and on what. "staged a3f1c2d4…" over "plan pending"
 * when a plan was proposed for it, over "not proposed" when nothing was,
 * and over the plan's state when it went stale or failed. The proof names
 * both revisions, as the title and for a screen reader; `proof` prints it
 * as a second line where the surface has the room.
 *
 * A record with nothing staged can still be about to change: a delete
 * plan, or a plan over its shares' identities, waiting or failed. The badge
 * says that too ("delete pending"), so a record about to be archived does
 * not look like any other. A delete plan wins over a staged revision and
 * its promote plan, since the delete drops both (rowPlan). `hideState`
 * drops the state line where a plan chip beside the badge already says it.
 */
const props = defineProps<{
  item: SubscriptionListItem;
  /** plans.status for the record; undefined while unread. */
  plans?: PlanStatus[];
  proof?: boolean;
  hideState?: boolean;
}>();

const facts = computed(() => stagedFacts(props.item));
const own = computed(() => rowPlan(props.plans, props.item));
const state = computed(() => (own.value ? planState(own.value) : stagedPlanState(props.plans, props.item)));
const short = computed(() => shortRevision(facts.value.revision, 8));
const isDelete = computed(() => own.value?.action === "delete");

const tone = computed(() => {
  if (state.value === "failed" || state.value === "rejected") return "danger";
  if (state.value === "pending" || state.value === "stale") return "warning";
  if (state.value === "applied") return "success";
  return "info";
});

/**
 * First line: the staged revision; for a delete plan with nothing staged,
 * the delete and its state in one ("delete pending"); for a plan over the
 * shares, what it changes.
 */
const revisionText = computed(() => {
  if (own.value) {
    const words = t.plans.staged.deleteState as Record<string, string>;
    return isDelete.value ? words[state.value] ?? t.plans.staged.deleteState.pending : t.plans.staged.sharesPlan;
  }
  return facts.value.neverLive ? t.plans.staged.newRecord(short.value) : t.plans.staged.revision(short.value);
});
const stateText = computed(() => {
  if (props.hideState || state.value === "unknown" || (own.value && isDelete.value)) return "";
  const words = t.plans.staged.state as Record<string, string>;
  return words[state.value] ?? "";
});

const proofText = computed(() => {
  const words = t.plans.staged;
  if (own.value) {
    const live = shortRevision(props.item.revision ?? "");
    return isDelete.value ? words.proofDelete(own.value.approval_id, live) : words.proofShares(own.value.approval_id, live);
  }
  const staged = facts.value.revision;
  if (facts.value.restored) return words.proofRestored(staged);
  if (facts.value.neverLive) return words.proofNew(staged);
  if (facts.value.migrated) return words.proofMigrated(staged, facts.value.live);
  return words.proof(staged, facts.value.live);
});
</script>

<template>
  <span
    v-if="facts.staged || own"
    class="staged-badge"
    :class="{ 'is-plan': !!own }"
    :data-tone="tone"
    :data-state="state"
    :data-action="own ? own.action : undefined"
    :title="proofText"
    :data-testid="own ? 'plan-badge' : 'staged-badge'"
  >
    <span class="staged-badge__line">
      <span class="staged-badge__dot" aria-hidden="true" />
      <span class="staged-badge__text">{{ revisionText }}</span>
    </span>
    <span v-if="stateText" class="staged-badge__state" data-testid="staged-state">{{ stateText }}</span>
    <small v-if="proof" class="staged-badge__proof" data-testid="staged-proof">{{ proofText }}</small>
    <span v-else class="pc-sr-only">{{ proofText }}</span>
  </span>
</template>

<style scoped>
.staged-badge {
  display: inline-flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
  max-width: 100%;
  font-size: var(--lt-text-xs);
  line-height: var(--lt-leading-tight);
  color: var(--muted-foreground);
}
.staged-badge__line {
  display: inline-flex;
  align-items: baseline;
  gap: var(--space-2);
  min-width: 0;
}
.staged-badge__dot {
  flex: none;
  align-self: center;
  width: 7px;
  height: 7px;
  border-radius: var(--radius-sm);
  background: var(--info);
}
.staged-badge[data-tone="warning"] .staged-badge__dot {
  background: var(--warning);
}
.staged-badge[data-tone="danger"] .staged-badge__dot {
  background: var(--destructive);
}
.staged-badge[data-tone="success"] .staged-badge__dot {
  background: var(--success);
}
/* Wraps at its separator rather than cutting the state off: in the Records
   table's Published column the revision and the plan's state are both the
   point. */
.staged-badge__text {
  font-family: var(--font-mono);
  min-width: 0;
  overflow-wrap: anywhere;
}
/* A plan with nothing staged names what it does in words, not an id. */
.staged-badge.is-plan .staged-badge__text {
  font-family: inherit;
}
.staged-badge__state {
  padding-left: calc(7px + var(--space-2));
  color: var(--foreground);
}
.staged-badge[data-tone="danger"] .staged-badge__state {
  color: var(--lt-danger-ink);
}
.staged-badge[data-tone="warning"] .staged-badge__state {
  color: var(--warning-text);
}
.staged-badge__proof {
  overflow-wrap: anywhere;
}
</style>
