<script setup lang="ts">
import { computed, ref } from "vue";
import { ChevronDown } from "@lucide/vue";
import type { SubscriptionPreviewResponse } from "../client";
import NodeRows from "./NodeRows.vue";

const props = defineProps<{
  preview: SubscriptionPreviewResponse;
  /** Set when the preview was cut off partway down the chain. */
  stepLabel?: string;
}>();

/** "kept 41 of 52 nodes" when a filter ran, "52 node(s)" when nothing was dropped. */
const headline = computed(() => {
  const kept = props.preview.node_count;
  const source = props.preview.source_node_count ?? kept;
  return source > kept ? `kept ${kept} of ${source} nodes` : `${kept} node(s)`;
});

const dropped = computed(() => props.preview.dropped ?? []);
/** Counted before the reply capped the list, so a long subscription still
 *  reports every node it lost even when it cannot name them all. */
const droppedCount = computed(() => props.preview.dropped_count ?? dropped.value.length);

/** Protocol breakdown of the previewed set, most common first. */
const typeCounts = computed(() => {
  const counts = new Map<string, number>();
  for (const node of props.preview.nodes ?? []) {
    const type = node.type || "unknown";
    counts.set(type, (counts.get(type) ?? 0) + 1);
  }
  return [...counts.entries()]
    .map(([type, count]) => ({ type, count }))
    .sort((a, b) => b.count - a.count || a.type.localeCompare(b.type));
});

// Both groups open. Either can be folded away, because at pane width a list of
// 200 buries whichever group is not being read.
const keptOpen = ref(true);
const removedOpen = ref(true);
</script>

<template>
  <div class="preview-summary">
    <p v-if="stepLabel" class="preview-cut" role="status">
      Partial run, stopped after "{{ stepLabel }}". Steps below it did not run.
    </p>
    <p v-if="preview.source_version" class="mono" role="status">
      Source {{ preview.source_version }} · {{ preview.stale ? "stale last-good" : "fresh composition" }}
    </p>
    <p class="mono">
      {{ headline }}<span v-if="preview.truncated"> · truncated</span>
    </p>
    <p v-if="typeCounts.length" class="preview-type-chips">
      <span v-for="entry in typeCounts" :key="entry.type" class="badge">
        {{ entry.type }} × {{ entry.count }}
      </span>
    </p>

    <!-- Nothing was removed, so there is nothing to compare against and the
         result is the whole answer. Two labelled groups here would be one
         group and an empty one. -->
    <NodeRows v-if="!droppedCount" :nodes="preview.nodes" />

    <!-- A filter ran. What it removed is the half the result cannot show, and
         it is the half someone tuning that filter is reading for. -->
    <template v-else>
      <section class="rec-group">
        <button
          type="button"
          class="rec-group-head"
          :aria-expanded="keptOpen"
          @click="keptOpen = !keptOpen"
        >
          <ChevronDown :size="14" class="rec-group-caret" :class="{ 'is-collapsed': !keptOpen }" aria-hidden="true" />
          <span>Kept</span>
          <span class="rec-group-count">{{ preview.node_count }}</span>
        </button>
        <NodeRows v-if="keptOpen" :nodes="preview.nodes" />
      </section>

      <section class="rec-group">
        <button
          type="button"
          class="rec-group-head"
          :aria-expanded="removedOpen"
          @click="removedOpen = !removedOpen"
        >
          <ChevronDown :size="14" class="rec-group-caret" :class="{ 'is-collapsed': !removedOpen }" aria-hidden="true" />
          <span>Removed by the chain</span>
          <span class="rec-group-count" data-tone="danger">{{ droppedCount }}</span>
        </button>
        <template v-if="removedOpen">
          <p v-if="preview.dropped_truncated" class="node-group-note">
            Naming the first {{ dropped.length }} of them.
          </p>
          <NodeRows :nodes="dropped" tone="dropped" />
        </template>
      </section>
    </template>
  </div>
</template>
