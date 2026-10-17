<script setup lang="ts">
import { computed } from "vue";
import { ListOrdered } from "@lucide/vue";
import { PcButton, PcNotice } from "@latticenet/plugin-bridge/chassis";

import { t } from "../i18n";
import type { RefusedStep, ScriptNetworkRefusal } from "../scriptNetwork";

/**
 * fleet_script_network_unavailable, said where the render was asked for
 * (S2-SR-2): which step of which record asked for the network, the rule
 * (scripts on fleet renders run without network until isolation lands),
 * core's own sentence, and one way to the step. `step` is the step the
 * surface resolved (scriptNetwork.ts refusedStepIn); without one the notice
 * names the record and offers its steps. The button sits under the text at
 * every width, so a narrow frame does not squeeze the sentences into a
 * column beside it.
 */
const props = defineProps<{
  refusal: ScriptNetworkRefusal;
  /** The record's display name, or "this record" for the one being edited. */
  recordName: string;
  step: RefusedStep | null;
  /** The surface can open the step (or the record's steps). */
  canOpen?: boolean;
  /** The step belongs to another record than the one on screen: the button names it. */
  otherRecord?: boolean;
}>();

const emit = defineEmits<{ "open-step": [position: number | null] }>();

const what = computed(() =>
  props.step ? t.plans.scriptNetwork.step(props.step.position, props.step.type, props.recordName) : t.plans.scriptNetwork.anyStep(props.recordName),
);
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
  >
    <div class="sn-body">
      <p data-testid="script-network-step">{{ what }}</p>
      <p data-testid="script-network-rule">{{ t.plans.scriptNetwork.rule }}</p>
      <p v-if="refusal.message" class="sn-core">
        <span class="sn-core__label">{{ t.plans.view.coreSays }}</span>
        {{ refusal.message }}
      </p>
      <div v-if="canOpen" class="sn-actions">
        <PcButton compact data-testid="script-network-open-step" @click="emit('open-step', step?.position ?? null)">
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
}
</style>
