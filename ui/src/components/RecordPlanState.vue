<script setup lang="ts">
import { computed, watch } from "vue";
import { ArrowRightLeft, Send, UserCheck } from "@lucide/vue";
import { PcButton } from "@latticenet/plugin-bridge/chassis";

import type { SubscriptionListItem } from "../client";
import { useHost } from "../host";
import { t } from "../i18n";
import { useLensChrome } from "../lensChrome";
import { hostOriginFromHash, postNavigate } from "../navigate";
import { currentPlan, decoyShares, mixedBlock, pendingDelete, recordName, type PlanShareRow } from "../planFlow";
import type { ActionId } from "../recordActions";
import { recordIntent } from "../recordIntent";
import { useSubscriptions } from "../useSubscriptions";
import PlanStatus from "./PlanStatus.vue";
import StagedBadge from "./StagedBadge.vue";

/**
 * What design 28 S2 adds to a record shown on its own, in the side panel and
 * on the record page: the staged revision with its proof, the plan it waits
 * on with core's state and reason (a failed apply with the live revision
 * from the list), the shares that answer the decoy, and a combination core
 * refuses until a legacy member migrates. The Overview's attention items
 * open this record; without it the panel read "ok" beside a failed plan.
 *
 * Each state carries its way out, handed to the Records screen, which holds
 * the plan flow: Publish opens the plan that waits (or proposes again), Share
 * identities opens the assignment, Migrate opens the member's migration.
 */
const props = defineProps<{
  id: string;
  items: readonly SubscriptionListItem[];
  shares: readonly PlanShareRow[] | undefined;
}>();

const host = useHost();
const subs = useSubscriptions(host);
const chrome = useLensChrome();
const intent = recordIntent(host);

const record = computed(() => props.items.find((item) => item.id === props.id));
watch(
  () => props.id,
  (id) => {
    if (id) void subs.loadPlans([id]);
  },
  { immediate: true },
);

const plans = computed(() => subs.plansByRecord.value[props.id]);
const plan = computed(() => {
  const item = record.value;
  if (!item || !plans.value) return undefined;
  // A delete waiting on the record is said first: it drops a staged revision and its promote plan with the record.
  const found = pendingDelete(plans.value, item) ?? currentPlan(plans.value, item);
  // A plan over another record whose closure holds this one is that record's to say.
  return found && (found.subscription_id === item.id || item.staged_revision) ? found : undefined;
});
const decoys = computed(() => (record.value ? decoyShares(record.value, props.shares) : []));
const mixed = computed(() => (record.value ? mixedBlock(record.value, props.items) ?? [] : []));
const shown = computed(() => !!record.value && (!!record.value.staged_revision || !!plan.value || decoys.value.length > 0 || mixed.value.length > 0));

const shareOrigin = computed(() => hostOriginFromHash(typeof window === "undefined" ? "" : window.location.hash));

function handOver(action: ActionId, recordId: string): void {
  chrome.openId.value = "";
  chrome.openLens("records");
  intent.value = { recordId, action };
}
function openApprovals(approvalId: string): void {
  if (shareOrigin.value) postNavigate(window, `/approvals?open=${encodeURIComponent(approvalId)}`, shareOrigin.value);
}
</script>

<template>
  <section v-if="shown && record" class="record-plan" :aria-label="t.plans.recordState.label" data-testid="record-plan-state">
    <div v-if="record.staged_revision || plan" class="record-plan__staged">
      <StagedBadge :item="record" :plans="plans" proof :hide-state="!!plan" />
      <PlanStatus
        v-if="plan"
        :plan="plan"
        detail
        :records="items"
        :identities="subs.identities.value ?? []"
        :can-propose="subs.canPropose.value"
        :can-open-approvals="!!shareOrigin"
        @refresh="subs.loadPlans([id], true)"
        @propose-again="handOver(plan.action === 'delete' ? 'delete' : 'publish-staged', id)"
        @propose-rest="handOver('publish-staged', id)"
        @open-approvals="openApprovals"
      />
      <PcButton
        v-if="record.staged_revision && !plan"
        compact
        :disabled="!subs.canPropose.value"
        :title="subs.canPropose.value ? t.plans.actions.publishStagedTitle : t.plans.cannotPropose"
        data-testid="record-plan-publish"
        @click="handOver('publish-staged', id)"
      >
        <template #icon><Send :size="13" aria-hidden="true" /></template>
        {{ t.plans.actions.publishStaged }}
      </PcButton>
    </div>

    <div v-if="decoys.length" class="record-plan__row" data-testid="record-plan-decoy">
      <p>{{ t.fleet.decoy(decoys.length) }}</p>
      <PcButton compact :disabled="!subs.canPropose.value" :title="subs.canPropose.value ? undefined : t.plans.cannotPropose" @click="handOver('share-identities', id)">
        <template #icon><UserCheck :size="13" aria-hidden="true" /></template>
        {{ t.plans.actions.shareIdentities }}
      </PcButton>
    </div>

    <div v-if="mixed.length" class="record-plan__row" data-testid="record-plan-mixed">
      <p>{{ t.plans.attention.mixed(recordName(record), mixed.map((member) => recordName(member)).join(", ")) }}</p>
      <PcButton compact :disabled="!subs.canPropose.value" :title="subs.canPropose.value ? undefined : t.plans.cannotPropose" @click="handOver('migrate', mixed[0]!.id)">
        <template #icon><ArrowRightLeft :size="13" aria-hidden="true" /></template>
        {{ t.migration.migrateMember(recordName(mixed[0]!)) }}
      </PcButton>
    </div>
  </section>
</template>

<style scoped>
.record-plan {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  min-width: 0;
}
.record-plan__staged {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-3);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  min-width: 0;
}
.record-plan__row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2) var(--space-3);
  padding: var(--space-2) var(--space-3);
  border-left: 2px solid var(--warning);
  border-radius: var(--radius-sm);
  background: var(--lt-warn-soft);
}
.record-plan__row p {
  margin: 0;
  flex: 1 1 220px;
  font-size: var(--lt-text-sm);
  max-width: var(--lt-measure-prose);
}
</style>
