<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { ChevronLeft, ChevronRight, LoaderCircle } from "@lucide/vue";

import { groupDropped, type StepDelta } from "../chainExplain";
import type { SubscriptionPreviewNode, SubscriptionPreviewResponse } from "../client";
import { schemaFor } from "../operatorSchema";
import type { ChainStep } from "./ProcessChain.vue";

/**
 * The expanded-row account of a record: the chain as a process, the source
 * as a proof line, dropped nodes grouped by the operation that removed them
 * and paged. It is not more table rows. The parent table's columns (source,
 * published, last fetch) have nothing to say about an operation.
 */
const props = withDefaults(
  defineProps<{
    loading?: boolean;
    error?: string;
    sourceKind?: string;
    url?: string;
    urlRevealed?: boolean;
    urlMasked?: string;
    steps?: ChainStep[];
    deltas?: StepDelta[];
    dropped?: SubscriptionPreviewNode[];
    droppedBy?: Map<string, string>;
    droppedCount?: number;
    droppedTruncated?: boolean;
    isCombination?: boolean;
    canPreview?: boolean;
    running?: number | null;
    final?: SubscriptionPreviewResponse | null;
    pageSize?: number;
  }>(),
  {
    loading: false,
    error: "",
    sourceKind: "",
    url: "",
    urlRevealed: false,
    urlMasked: "",
    steps: () => [],
    deltas: () => [],
    dropped: () => [],
    droppedBy: () => new Map(),
    droppedCount: 0,
    droppedTruncated: false,
    isCombination: false,
    canPreview: false,
    running: null,
    final: null,
    pageSize: 12,
  },
);

const emit = defineEmits<{ reveal: [] }>();

const groups = computed(() => groupDropped(props.dropped, props.droppedBy ?? new Map()));
const selectedLabel = ref("");
const page = ref(0);

const selectedGroup = computed(() => groups.value.find((group) => group.label === selectedLabel.value) ?? null);
const pageCount = computed(() => {
  const n = selectedGroup.value?.nodes.length ?? 0;
  return Math.max(1, Math.ceil(n / props.pageSize));
});
const pageNodes = computed(() => {
  const nodes = selectedGroup.value?.nodes ?? [];
  return nodes.slice(page.value * props.pageSize, (page.value + 1) * props.pageSize);
});
const pageFrom = computed(() => (selectedGroup.value?.nodes.length ? page.value * props.pageSize + 1 : 0));
const pageTo = computed(() => Math.min(selectedGroup.value?.nodes.length ?? 0, (page.value + 1) * props.pageSize));

watch(
  [groups, () => props.deltas],
  () => {
    page.value = 0;
    const next = groups.value;
    if (next.some((group) => group.label === selectedLabel.value)) return;
    const anyCut = props.deltas.some((delta) => delta.after < delta.before);
    selectedLabel.value = anyCut ? "" : (next[0]?.label ?? "");
  },
  { immediate: true },
);

const headline = computed(() => {
  const kept = props.final?.node_count;
  const source = props.final?.source_node_count ?? kept;
  if (typeof kept !== "number") return "";
  if (typeof source === "number" && source > kept) return `${source} → ${kept}`;
  return `${kept} nodes`;
});

const droppedTotal = computed(() => props.droppedCount || props.dropped.length);

const proofMeta = computed(() => {
  const bits: string[] = [];
  if (props.steps.length) bits.push(`${props.steps.length} operation${props.steps.length === 1 ? "" : "s"}`);
  if (droppedTotal.value) bits.push(`${droppedTotal.value} dropped`);
  return bits.join(" · ");
});

function deltaOf(index: number): StepDelta | undefined {
  return props.deltas.find((entry) => entry.index === index);
}

function sparkOf(index: number, step: ChainStep): string {
  if (step.disabled) return "";
  if (props.isCombination) return "";
  const delta = deltaOf(index);
  if (delta) return `${delta.before} → ${delta.after}`;
  if (props.running === index) return "…";
  if (!props.canPreview) return "";
  if (props.final) return "";
  return "…";
}

function verbOf(index: number, step: ChainStep): string {
  if (step.disabled) return "off";
  if (props.isCombination) return "";
  const delta = deltaOf(index);
  if (delta) {
    if (delta.after === delta.before) return "kept all";
    if (delta.after < delta.before) return `dropped ${delta.before - delta.after}`;
    return `added ${delta.after - delta.before}`;
  }
  if (props.running === index) return "running";
  if (!props.canPreview) return "";
  if (props.final) return "not run";
  return "";
}

function isCut(index: number): boolean {
  const delta = deltaOf(index);
  return !!delta && delta.after < delta.before;
}

function selectCut(index: number): void {
  const delta = deltaOf(index);
  if (!delta || delta.after >= delta.before) return;
  selectedLabel.value = delta.label;
  page.value = 0;
}

function endpointOf(node: SubscriptionPreviewNode): string {
  if (!node.server) return "";
  return node.port ? `${node.server}:${node.port}` : node.server;
}

function nameOf(step: ChainStep): string {
  return (step.customName ?? "").trim() || schemaFor(step.type)?.label || step.type;
}
</script>

<template>
  <div class="rec-chain-pane">
    <p v-if="loading" class="rec-chain-note" role="status">
      <LoaderCircle :size="13" class="spin" aria-hidden="true" /> Reading the record…
    </p>
    <p v-else-if="error" class="rec-chain-note is-error" role="alert">{{ error }}</p>
    <template v-else>
      <p v-if="headline" class="rec-chain-proof">
        <span class="rec-chain-proof-count">{{ headline }}</span><template v-if="proofMeta"> · {{ proofMeta }}</template>
      </p>

      <ol v-if="steps.length" class="rec-chain-steps" aria-label="What each operation kept">
        <li
          v-for="(step, index) in steps"
          :key="index"
          :class="{
            'is-cut': isCut(index),
            'is-off': step.disabled,
            'is-running': running === index,
            'is-selected': isCut(index) && deltaOf(index)?.label === selectedLabel,
          }"
        >
          <button
            v-if="isCut(index)"
            type="button"
            class="rec-chain-step"
            :aria-pressed="deltaOf(index)?.label === selectedLabel"
            :title="`Show nodes dropped by ${deltaOf(index)?.label}`"
            @click="selectCut(index)"
          >
            <span class="rec-chain-idx">{{ index + 1 }}</span>
            <span class="rec-chain-name">{{ nameOf(step) }}</span>
            <span class="rec-chain-spark">{{ sparkOf(index, step) }}</span>
            <span class="rec-chain-verb">{{ verbOf(index, step) }}</span>
          </button>
          <div v-else class="rec-chain-step" :title="step.disabled ? 'Switched off: the chain skips this operation' : undefined">
            <span class="rec-chain-idx">{{ index + 1 }}</span>
            <span class="rec-chain-name">{{ nameOf(step) }}</span>
            <span class="rec-chain-spark">{{ sparkOf(index, step) }}</span>
            <span class="rec-chain-verb">{{ verbOf(index, step) }}</span>
          </div>
        </li>
      </ol>
      <p v-else class="rec-chain-note">
        No operations. The nodes are served as the source provides them<template v-if="final">: {{ final.node_count }} of them</template>.
      </p>
      <p v-if="isCombination && steps.length" class="rec-chain-note">
        The engine runs a combination's operations over its members' merged output and reports one result<template v-if="final">, {{ final.node_count }} nodes</template>; per-operation counts exist for a subscription only.
      </p>
      <p v-else-if="!canPreview && steps.length" class="rec-chain-note">
        This session cannot run a preview, so what each operation kept is unknown.
      </p>
      <p v-else-if="droppedTotal && !selectedGroup" class="rec-chain-note">
        {{ droppedTotal }} dropped. Choose a cut to name them.
      </p>

      <p v-if="sourceKind || url" class="rec-chain-source">
        <span class="rec-chain-eyebrow">Source</span>
        <span v-if="sourceKind">{{ sourceKind }}</span>
        <template v-if="url">
          <code class="rec-chain-url" :title="urlRevealed ? 'Masks itself again after a minute' : 'Masked: the query string carries the provider token'">{{ urlRevealed ? url : urlMasked }}</code>
          <button type="button" class="rec-reveal" @click="emit('reveal')">
            {{ urlRevealed ? "Hide" : "Reveal" }}
          </button>
        </template>
      </p>

      <section v-if="selectedGroup" class="rec-chain-dropped" :aria-label="`Nodes dropped by ${selectedGroup.label}`">
        <header class="rec-chain-dropped-head">
          <h4>Dropped by {{ selectedGroup.label }}</h4>
          <span class="rec-chain-dropped-count">{{ selectedGroup.nodes.length }}</span>
        </header>
        <p v-if="groups.length > 1" class="rec-chain-dropped-switch">
          <button
            v-for="group in groups"
            :key="group.label"
            type="button"
            class="rec-chain-group"
            :class="{ 'is-current': group.label === selectedGroup.label }"
            :aria-pressed="group.label === selectedGroup.label"
            @click="selectedLabel = group.label; page = 0"
          >
            {{ group.label }} {{ group.nodes.length }}
          </button>
        </p>
        <ul class="rec-chain-dropped-list">
          <li v-for="(node, index) in pageNodes" :key="`${node.name}-${index}`">
            <span class="rec-chain-dropped-name" :title="node.name">{{ node.name }}</span>
            <span v-if="endpointOf(node)" class="mono rec-chain-dropped-endpoint">{{ endpointOf(node) }}</span>
          </li>
        </ul>
        <p v-if="droppedTruncated" class="rec-chain-note">
          Naming the first {{ dropped.length }} of {{ droppedTotal }}.
        </p>
        <nav v-if="pageCount > 1" class="compare-pager" aria-label="Pages of dropped nodes">
          <button type="button" class="button button-secondary button-compact" :disabled="page === 0" aria-label="Previous page" @click="page -= 1">
            <ChevronLeft :size="13" aria-hidden="true" />
          </button>
          <span class="mono" role="status">{{ pageFrom }} to {{ pageTo }} of {{ selectedGroup.nodes.length }}</span>
          <button type="button" class="button button-secondary button-compact" :disabled="page >= pageCount - 1" aria-label="Next page" @click="page += 1">
            <ChevronRight :size="13" aria-hidden="true" />
          </button>
        </nav>
      </section>
    </template>
  </div>
</template>
