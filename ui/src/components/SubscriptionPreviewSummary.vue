<script setup lang="ts">
import type { SubscriptionPreviewResponse } from "../client";

defineProps<{ preview: SubscriptionPreviewResponse }>();
</script>

<template>
  <div class="preview-summary">
    <p v-if="preview.source_version" class="mono" role="status">
      Source {{ preview.source_version }} · {{ preview.stale ? "stale last-good" : "fresh composition" }}
    </p>
    <p class="mono">
      {{ preview.node_count }} node(s)<span v-if="preview.source_node_count !== preview.node_count"> from {{ preview.source_node_count }}</span><span v-if="preview.truncated"> — truncated</span>
    </p>
    <ul class="sub-list">
      <li v-for="(node, index) in preview.nodes" :key="`${node.name}-${index}`" class="sub-card">
        <span class="sub-title">{{ node.name }}</span>
        <span class="sub-meta mono">{{ node.type }}</span>
      </li>
    </ul>
  </div>
</template>
