<script setup lang="ts">
import { computed, nextTick, ref, watch } from "vue";
import { PcButton, PcNotice } from "@latticenet/plugin-bridge/chassis";

import { applyRewrite, regexDiagnostics, type RegexDiagnostic } from "../regexRewrite";
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

const TEXT = {
  title: "The native engine cannot run a pattern in this chain",
  pending: "Until it changes, this record renders on the fallback path, and a save that changes the chain is refused.",
  step: (step: number, label: string) => `Step ${step}, ${label}`,
  rewriteLead: "It keeps every node that does not match. The same filter in drop mode:",
  rewriteAction: (step: number) => `Rewrite step ${step}`,
  noRewrite: "Rewrite it without lookaround or backreferences, or turn the step off.",
  resolvedTitle: "Every pattern in the chain runs natively now",
  resolvedAfterRefusal: "Save again to store it.",
  resolvedAfterRewrite: "The rewrite is in the draft. Save to store it.",
} as const;

const live = computed(() => regexDiagnostics(props.chain));
/** A rewrite was applied here and nothing has reintroduced a pattern since. */
const applied = ref(false);
/**
 * Something this notice named is gone from the chain. A refusal whose
 * pattern this reading did not recognise names nothing, so it never reads as
 * resolved; its reason stays beside Save.
 */
const resolved = computed(() => !live.value.length && (applied.value || !!props.refusal?.diagnostics.length));
watch(
  () => live.value.length,
  (count) => {
    if (count) applied.value = false;
    else if (props.refusal?.diagnostics.length) emit("resolved");
  },
);

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
    v-if="live.length || resolved"
    ref="root"
    :tone="live.length ? 'warning' : 'success'"
    :title="live.length ? TEXT.title : TEXT.resolvedTitle"
    data-testid="regex-rewrite-offer"
  >
    <p v-if="!live.length">{{ refusal ? TEXT.resolvedAfterRefusal : TEXT.resolvedAfterRewrite }}</p>
    <p v-else-if="!refusal">{{ TEXT.pending }}</p>
    <ul v-if="live.length" class="regex-offer-list">
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
