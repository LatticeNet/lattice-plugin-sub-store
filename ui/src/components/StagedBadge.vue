<script setup lang="ts">
import { computed } from "vue";

import type { SubscriptionListItem } from "../client";
import type { PlanStatus } from "../fleet";
import { t } from "../i18n";
import { shortRevision, stagedFacts, stagedPlanState } from "../planFlow";

/**
 * A record's staged revision, where the Published column states what its
 * shares serve (s2-plan section 11). A fleet-bound record's edit waits in
 * staged-v2 until core promotes it, so the column says both facts: what is
 * live, and what waits and on what. "staged a3f1c2d4…" over "plan pending"
 * when a plan was proposed for it, over "publish" when nothing was, and
 * over the plan's state when it went stale or failed. The proof names both
 * revisions, as the title and for a screen reader; `proof` prints it as a
 * second line where the surface has the room.
 */
const props = defineProps<{
  item: SubscriptionListItem;
  /** plans.status for the record; undefined while unread. */
  plans?: PlanStatus[];
  proof?: boolean;
}>();

const facts = computed(() => stagedFacts(props.item));
const state = computed(() => stagedPlanState(props.plans, props.item));
const short = computed(() => shortRevision(facts.value.revision, 8));

const tone = computed(() => {
  if (state.value === "failed" || state.value === "rejected") return "danger";
  if (state.value === "pending" || state.value === "stale") return "warning";
  if (state.value === "applied") return "success";
  return "info";
});

/** First line: the staged revision. Second: what it waits on. */
const revisionText = computed(() =>
  facts.value.neverLive ? t.plans.staged.newRecord(short.value) : t.plans.staged.revision(short.value),
);
const stateText = computed(() => {
  const words = t.plans.staged.state as Record<string, string>;
  return state.value === "unknown" ? "" : words[state.value] ?? "";
});

const proofText = computed(() => {
  const words = t.plans.staged;
  const staged = facts.value.revision;
  if (facts.value.restored) return words.proofRestored(staged);
  if (facts.value.neverLive) return words.proofNew(staged);
  if (facts.value.migrated) return words.proofMigrated(staged, facts.value.live);
  return words.proof(staged, facts.value.live);
});
</script>

<template>
  <span
    v-if="facts.staged"
    class="staged-badge"
    :data-tone="tone"
    :data-state="state"
    :title="proofText"
    data-testid="staged-badge"
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
