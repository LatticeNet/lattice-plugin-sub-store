<script setup lang="ts">
import { computed, nextTick, ref, watch } from "vue";
import { PcButton, PcNotice } from "@latticenet/plugin-bridge/chassis";

import { applyRewrite, offerState, regexDiagnostics, type RegexDiagnostic } from "../regexRewrite";
import { t } from "../i18n";
import { schemaFor } from "../operatorSchema";

/**
 * A pattern in the chain the native engine cannot run, and what to do about it.
 *
 * The native engine compiles patterns with RE2, so a chain that adds
 * lookaround or a backreference is refused at save rather than stored to fall
 * back on the bundle forever, and a stored record that already has one is
 * flagged `regex_incompatible` (s1-plan section 3.1). This names the step and
 * its pattern, and where the pattern is the keep-everything-except idiom it
 * offers the drop-mode rewrite that means the same thing.
 *
 * The list is read live from the chain on screen, not from the refusal, so it
 * shows the moment a flagged record opens, before any save: the save only
 * compiles a chain that changed, so waiting for a refusal left an unchanged
 * flagged chain with no way to the rewrite. A rewrite applied here, or a step
 * edited by hand, drops out of the list at once; when the list empties the
 * notice says so, and a refusal's message beside Save is withdrawn
 * (`resolved`).
 */
const props = defineProps<{
  /** The refusal from the last save, or null when the last save was not refused for this. */
  refusal: { code: string; diagnostics: RegexDiagnostic[] } | null;
  chain: readonly unknown[];
}>();
const emit = defineEmits<{
  (e: "apply", chain: unknown[]): void;
  /** The chain on screen no longer has what the last save was refused for. */
  (e: "resolved"): void;
}>();


const live = computed(() => regexDiagnostics(props.chain));
/** A rewrite was applied here and nothing has reintroduced a pattern since. */
const applied = ref(false);
const state = computed(() => offerState(live.value, props.refusal, applied.value));
watch(state, (now) => {
  if (now === "patterns") applied.value = false;
  else if (now === "resolved-refusal") emit("resolved");
});

/** The step as the chain list names it: "Regex filter", not the wire type. */
function labelOf(type: string): string {
  return schemaFor(type)?.label ?? type;
}

/** A refusal lands where Save was pressed; the offer is brought into view under it. */
const root = ref<{ $el?: Element } | null>(null);
watch(
  () => props.refusal,
  async (refusal) => {
    if (!refusal) return;
    await nextTick();
    root.value?.$el?.scrollIntoView?.({ block: "nearest" });
  },
);

function rewrite(diagnostic: RegexDiagnostic): void {
  applied.value = true;
  emit("apply", applyRewrite(props.chain, diagnostic));
}
</script>

<template>
  <PcNotice
    v-if="state !== 'none'"
    ref="root"
    :tone="state === 'patterns' ? 'warning' : 'success'"
    :title="state === 'patterns' ? t.regexOffer.title : t.regexOffer.resolvedTitle"
    data-testid="regex-rewrite-offer"
  >
    <p v-if="state === 'resolved-refusal'">{{ t.regexOffer.resolvedAfterRefusal }}</p>
    <p v-else-if="state === 'resolved-rewrite'">{{ t.regexOffer.resolvedAfterRewrite }}</p>
    <p v-else-if="!refusal">{{ t.regexOffer.pending }}</p>
    <ul v-if="state === 'patterns'" class="regex-offer-list">
      <li v-for="diagnostic in live" :key="`${diagnostic.step}:${diagnostic.pattern}`" class="regex-offer-item">
        <span class="regex-offer-step">{{ t.regexOffer.step(diagnostic.step, labelOf(diagnostic.type)) }}</span>
        <code class="regex-offer-pattern">{{ diagnostic.pattern }}</code>
        <template v-if="diagnostic.rewrite">
          <span>{{ t.regexOffer.rewriteLead }} <code class="regex-offer-pattern">{{ diagnostic.rewrite }}</code></span>
          <PcButton compact @click="rewrite(diagnostic)">{{ t.regexOffer.rewriteAction(diagnostic.step) }}</PcButton>
        </template>
        <span v-else>{{ t.regexOffer.noRewrite }}</span>
      </li>
    </ul>
  </PcNotice>
</template>
