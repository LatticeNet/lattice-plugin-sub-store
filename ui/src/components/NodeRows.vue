<script setup lang="ts">
import type { SubscriptionPreviewNode } from "../client";

defineProps<{
  nodes: SubscriptionPreviewNode[];
  /** "dropped" quiets the rows: they are context for the count, not the result. */
  tone?: "kept" | "dropped";
}>();

/** The flags worth surfacing per node, in a fixed order so the eye can scan a
 *  column rather than re-read each row. */
function flags(node: SubscriptionPreviewNode): { label: string; title: string }[] {
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
  <!-- One dense list, the same one the sheet and the row drawer use. This was
       a stack of bordered cards, which turned 50 nodes into 50 boxes. -->
  <ul class="node-list" :data-tone="tone ?? 'kept'">
    <li v-for="(node, index) in nodes" :key="`${node.name}-${index}`" class="node-row">
      <span class="node-name-cell">
        <span class="node-name" :title="node.name">{{ node.name }}</span>
        <!-- The name the chain replaced. Without it a rename is invisible: the
             new name reads as the name the node always had. It sits under the
             name rather than beside it: inline it was the first thing the row
             ellipsed away, leaving "was wa..." to report that something had
             been renamed from something. -->
        <span v-if="node.was" class="node-was" :title="`Renamed from ${node.was}`">
          was {{ node.was }}
        </span>
      </span>
      <span class="node-tags">
        <span class="badge">{{ node.type }}</span>
        <span v-for="flag in flags(node)" :key="flag.label" class="badge" :title="flag.title">
          {{ flag.label }}
        </span>
      </span>
      <!-- Outside the badge box, so a narrow surface can drop it to its own
           line without the badges dragging the node name down with it. -->
      <span v-if="node.server" class="node-meta">
        {{ node.port ? `${node.server}:${node.port}` : node.server }}
      </span>
    </li>
  </ul>
</template>
