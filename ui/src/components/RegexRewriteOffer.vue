<script setup lang="ts">
import { computed, nextTick, ref, watch } from "vue";
import { PcButton, PcNotice } from "@latticenet/plugin-bridge/chassis";

import { applyRewrite, regexDiagnostics, type RegexDiagnostic } from "../regexRewrite";
import { schemaFor } from "../operatorSchema";

/**
 * What to do about a save refused for `regex_incompatible`.
 *
 * The native engine compiles patterns with RE2, so a chain that adds
 * lookaround or a backreference is refused at save rather than stored to fall
 * back on the bundle forever (s1-plan section 3.1). The refusal carries the
 * code; this names the step and its pattern, and where the pattern is the
 * keep-everything-except idiom it offers the drop-mode rewrite that means the
 * same thing. The list is read live from the chain on screen, so a rewrite
 * applied here, or a step edited by hand, drops out of it at once.
 */
const props = defineProps<{
  /** The refusal from the last save, or null when the last save was not refused for this. */
  refusal: { code: string; diagnostics: RegexDiagnostic[] } | null;
  chain: readonly unknown[];
}>();
const emit = defineEmits<{ (e: "apply", chain: unknown[]): void }>();

const TEXT = {
  title: "The native engine cannot run a pattern in this chain",
  step: (step: number, label: string) => `Step ${step}, ${label}`,
  rewriteLead: "It keeps every node that does not match. The same filter in drop mode:",
  rewriteAction: (step: number) => `Rewrite step ${step}`,
  noRewrite: "Rewrite it without lookaround or backreferences, or turn the step off.",
  resolved: "Every pattern in the chain runs natively now. Save again to store it.",
} as const;

const live = computed(() => (props.refusal ? regexDiagnostics(props.chain) : []));

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
  emit("apply", applyRewrite(props.chain, diagnostic));
}
</script>

<template>
  <PcNotice v-if="refusal" ref="root" tone="warning" :title="TEXT.title" data-testid="regex-rewrite-offer">
    <p v-if="!live.length" role="status">{{ TEXT.resolved }}</p>
    <ul v-else class="regex-offer-list">
      <li v-for="diagnostic in live" :key="`${diagnostic.step}:${diagnostic.pattern}`" class="regex-offer-item">
        <span class="regex-offer-step">{{ TEXT.step(diagnostic.step, labelOf(diagnostic.type)) }}</span>
        <code class="regex-offer-pattern">{{ diagnostic.pattern }}</code>
        <template v-if="diagnostic.rewrite">
          <span>{{ TEXT.rewriteLead }} <code class="regex-offer-pattern">{{ diagnostic.rewrite }}</code></span>
          <PcButton compact @click="rewrite(diagnostic)">{{ TEXT.rewriteAction(diagnostic.step) }}</PcButton>
        </template>
        <span v-else>{{ TEXT.noRewrite }}</span>
      </li>
    </ul>
  </PcNotice>
</template>
