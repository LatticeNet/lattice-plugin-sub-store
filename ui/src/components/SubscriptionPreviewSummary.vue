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

/** The flags worth surfacing per node, in a fixed order so the eye can scan a
 *  column rather than re-read each row. */
function flags(node: SubscriptionPreviewResponse["nodes"][number]): { label: string; title: string }[] {
  const out: { label: string; title: string }[] = [];
  if (node.network) out.push({ label: node.network, title: "Transport" });
  if (node.security) out.push({ label: node.security, title: "Security" });
  if (node.udp) out.push({ label: "UDP", title: "UDP relay" });
  if (node.tfo) out.push({ label: "TFO", title: "TCP Fast Open" });
  if (node.skip_cert_verify) out.push({ label: "skip-cert", title: "Skips TLS certificate verification" });
  if (node.aead) out.push({ label: "AEAD", title: "VMess AEAD" });
  return out;
}
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
    <!-- One dense list, the same one the sheet and the row drawer use. This was
         a stack of bordered cards, which turned 50 nodes into 50 boxes. -->
    <ul class="node-list">
      <li v-for="(node, index) in preview.nodes" :key="`${node.name}-${index}`" class="node-row">
        <span class="node-name" :title="node.name">{{ node.name }}</span>
        <span class="node-tags">
          <span class="badge">{{ node.type }}</span>
          <span v-for="flag in flags(node)" :key="flag.label" class="badge" :title="flag.title">
            {{ flag.label }}
          </span>
          <span v-if="node.server" class="node-meta">
            {{ node.port ? `${node.server}:${node.port}` : node.server }}
          </span>
        </span>
      </li>
    </ul>
  </div>
</template>
