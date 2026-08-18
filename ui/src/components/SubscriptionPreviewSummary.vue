<script setup lang="ts">
import { computed } from "vue";
import type { SubscriptionPreviewResponse } from "../client";

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
</script>

<template>
  <div class="preview-summary">
    <p v-if="stepLabel" class="preview-cut" role="status">
      Partial run — stopped after “{{ stepLabel }}”. Steps below it did not run.
    </p>
    <p v-if="preview.source_version" class="mono" role="status">
      Source {{ preview.source_version }} · {{ preview.stale ? "stale last-good" : "fresh composition" }}
    </p>
    <p class="mono">
      {{ headline }}<span v-if="preview.truncated"> — truncated</span>
    </p>
    <p v-if="typeCounts.length" class="preview-type-chips">
      <span v-for="entry in typeCounts" :key="entry.type" class="badge">
        {{ entry.type }} × {{ entry.count }}
      </span>
    </p>
    <ul class="sub-list">
      <li
        v-for="(node, index) in preview.nodes"
        :key="`${node.name}-${index}`"
        class="sub-card sub-card-column"
      >
        <span class="sub-title">
          {{ node.name }}
          <span class="badge">{{ node.type }}</span>
          <span v-if="node.network" class="badge">{{ node.network }}</span>
          <span v-if="node.security" class="badge">{{ node.security }}</span>
          <span v-if="node.udp" class="badge" title="UDP relay">UDP</span>
          <span v-if="node.tfo" class="badge" title="TCP Fast Open">TFO</span>
          <span v-if="node.skip_cert_verify" class="badge" title="Skips TLS certificate verification">skip-cert</span>
          <span v-if="node.aead" class="badge" title="VMess AEAD">AEAD</span>
        </span>
        <span v-if="node.server" class="sub-meta mono">{{ node.port ? `${node.server}:${node.port}` : node.server }}</span>
      </li>
    </ul>
  </div>
</template>
