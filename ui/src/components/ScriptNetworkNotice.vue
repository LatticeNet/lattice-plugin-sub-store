<script setup lang="ts">
import { computed } from "vue";
import { ListOrdered } from "@lucide/vue";
import { PcButton, PcNotice } from "@latticenet/plugin-bridge/chassis";

import { t } from "../i18n";
import type { RefusedStep, ResolvedStep, ScriptNetworkRefusal } from "../scriptNetwork";

/**
 * fleet_script_network_unavailable, said where the render was asked for
 * (S2-SR-2): which step of which record asked for the network, the rule
 * (scripts on fleet renders run without network until isolation lands),
 * core's own sentence, and one way to the step. `step` is the step the
 * surface resolved (scriptNetwork.ts refusedStepIn); without one the notice
 * names the record and offers its steps. A step that is not proven to be
 * the one that asked is said as the first step that can ask, which is all
 * core can say (the lane 6c code review). The button sits under the text
 * at every width, so a narrow frame does not squeeze the sentences into a
 * column beside it.
 */
const props = defineProps<{
  refusal: ScriptNetworkRefusal;
  /** The record's display name, or "this record" for the one being edited. */
  recordName: string;
  step: ResolvedStep | RefusedStep | null;
  /** The surface can open the step (or the record's steps). */
  canOpen?: boolean;
  /** Opening the step is the way forward on this surface (a refused plan), so its button is the primary one. */
  primary?: boolean;
  /** The step belongs to another record than the one on screen: the button names it. */
  otherRecord?: boolean;
}>();

const emit = defineEmits<{ "open-step": [position: number | null] }>();

const certain = computed(() => !!props.step && "certain" in props.step && props.step.certain);
const what = computed(() => {
  const step = props.step;
  if (!step) return t.plans.scriptNetwork.anyStep(props.recordName);
  return certain.value ? t.plans.scriptNetwork.step(step.position, step.type, props.recordName) : t.plans.scriptNetwork.stepCandidate(step.position, step.type, props.recordName);
});
const openLabel = computed(() => {
  if (!props.step) return t.plans.scriptNetwork.openSteps(props.recordName);
  return props.otherRecord ? t.plans.scriptNetwork.openStepOf(props.step.position, props.recordName) : t.plans.scriptNetwork.openStep(props.step.position);
});
</script>

<template>
  <PcNotice
    tone="danger"
    :title="t.plans.scriptNetwork.title"
    data-testid="script-network-refusal"
    :data-record="refusal.record || undefined"
    :data-position="step?.position"
    :data-certain="step ? String(certain) : undefined"
  >
    <div class="sn-body">
      <p data-testid="script-network-step">{{ what }}</p>
      <p data-testid="script-network-rule">{{ t.plans.scriptNetwork.rule }}</p>
      <p v-if="refusal.message" class="sn-core">
        <span class="sn-core__label">{{ t.plans.view.coreSays }}</span>
        {{ refusal.message }}
      </p>
      <div v-if="canOpen" class="sn-actions">
        <PcButton compact :variant="primary ? 'primary' : 'secondary'" data-testid="script-network-open-step" @click="emit('open-step', step?.position ?? null)">
          <template #icon><ListOrdered :size="13" aria-hidden="true" /></template>
          {{ openLabel }}
        </PcButton>
      </div>
    </div>
  </PcNotice>
</template>

<style scoped>
.sn-body {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  min-width: 0;
}
.sn-body p {
  margin: 0;
  max-width: var(--lt-measure-prose);
  overflow-wrap: anywhere;
}
.sn-core {
  font-size: var(--lt-text-xs);
  color: var(--muted-foreground);
}
.sn-core__label {
  font-weight: 600;
  margin-right: var(--space-1);
}
.sn-actions {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
  margin-top: var(--space-1);
  min-width: 0;
}
/* "Open step 2 of <a long record name>" wraps inside the notice rather than
   run past it: the chassis button does not wrap, and a 375 Fleet row cut it. */
.sn-actions :deep(.pc-button) {
  max-width: 100%;
  white-space: normal;
  text-align: start;
}
</style>
