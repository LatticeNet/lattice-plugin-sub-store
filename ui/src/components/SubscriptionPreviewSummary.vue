<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { ChevronLeft, ChevronRight } from "@lucide/vue";

import { describeDelta, nodeKey, type StepDelta } from "../chainExplain";
import type { SubscriptionPreviewNode, SubscriptionPreviewResponse } from "../client";

/**
 * The compare panel: source nodes on the left, what the chain made of each
 * on the right, the way the official compare table reads. A kept node shows
 * its result beside it (with the name it had, when the chain renamed it); a
 * removed node shows the operation that removed it. The per-operation strip
 * sits on top when the chain has been explained.
 *
 * Paged rather than scrolled: the document is the only vertical scroller,
 * so a long set is cut to what fits a screen and walked page by page.
 */
const props = withDefaults(
  defineProps<{
    preview: SubscriptionPreviewResponse;
    /** Set when the preview was cut off partway down the chain. */
    stepLabel?: string;
    /** What each operation kept, from an explained run. */
    deltas?: StepDelta[];
    /** Node key to the label of the operation that removed it. */
    droppedBy?: Map<string, string>;
    pageSize?: number;
  }>(),
  { stepLabel: "", deltas: () => [], droppedBy: undefined, pageSize: 12 },
);

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
const sourceCount = computed(() => props.preview.source_node_count ?? props.preview.node_count);

/** Protocol breakdown of the result, most common first. */
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

interface CompareRow {
  key: string;
  /** The node as the source had it. */
  source: { name: string; endpoint: string };
  /** The node as the chain left it, or null when the chain removed it. */
  result: SubscriptionPreviewNode | null;
  /** The operation that removed it, when known. */
  by: string;
}

function endpointOf(node: SubscriptionPreviewNode): string {
  if (!node.server) return "";
  return node.port ? `${node.server}:${node.port}` : node.server;
}

/** Kept nodes first, in result order, then the removed ones. */
const rows = computed<CompareRow[]>(() => [
  ...(props.preview.nodes ?? []).map((node, index) => ({
    key: `k-${nodeKey(node)}-${index}`,
    source: { name: node.was ?? node.name, endpoint: endpointOf(node) },
    result: node,
    by: "",
  })),
  ...dropped.value.map((node, index) => ({
    key: `d-${nodeKey(node)}-${index}`,
    source: { name: node.name, endpoint: endpointOf(node) },
    result: null,
    by: props.droppedBy?.get(nodeKey(node)) ?? "",
  })),
]);

// ── paging ──────────────────────────────────────────────────────────────────
const page = ref(0);
const pageCount = computed(() => Math.max(1, Math.ceil(rows.value.length / props.pageSize)));
const pageRows = computed(() => rows.value.slice(page.value * props.pageSize, (page.value + 1) * props.pageSize));
const pageFrom = computed(() => (rows.value.length ? page.value * props.pageSize + 1 : 0));
const pageTo = computed(() => Math.min(rows.value.length, (page.value + 1) * props.pageSize));
// A new preview starts on its first page; a page past the end of a shorter
// result would be an empty table.
watch(() => props.preview, () => { page.value = 0; });

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
  <div class="preview-summary">
    <p v-if="stepLabel" class="preview-cut" role="status">
      Partial run, stopped after "{{ stepLabel }}". Operations below it did not run.
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

    <!-- The per-operation account: one line per enabled operation, the ones
         that removed nodes marked. -->
    <ol v-if="deltas.length" class="chain-deltas" aria-label="What each operation kept">
      <li v-for="delta in deltas" :key="delta.index" :class="{ 'is-cut': delta.after < delta.before }">
        {{ describeDelta(delta) }}
      </li>
    </ol>

    <table v-if="rows.length" class="compare-table">
      <thead>
        <tr>
          <th scope="col">Source <span class="compare-count">{{ sourceCount }}</span></th>
          <th scope="col">Result <span class="compare-count">{{ preview.node_count }}</span></th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="row in pageRows" :key="row.key" :class="{ 'is-dropped': !row.result }">
          <td class="compare-source">
            <span class="node-name" :title="row.source.name">{{ row.source.name }}</span>
            <span v-if="row.source.endpoint" class="node-meta">{{ row.source.endpoint }}</span>
          </td>
          <td class="compare-result">
            <template v-if="row.result">
              <span class="node-name" :title="row.result.name">{{ row.result.name }}</span>
              <!-- The name the chain replaced. Without it a rename is
                   invisible: the new name reads as the name the node always
                   had. -->
              <span v-if="row.result.was" class="node-was" :title="`Renamed from ${row.result.was}`">was {{ row.result.was }}</span>
              <span class="node-tags">
                <span class="badge">{{ row.result.type }}</span>
                <span v-for="flag in flags(row.result)" :key="flag.label" class="badge" :title="flag.title">{{ flag.label }}</span>
              </span>
            </template>
            <span v-else class="compare-dropped">removed by {{ row.by || "the chain" }}</span>
          </td>
        </tr>
      </tbody>
    </table>

    <p v-if="preview.dropped_truncated" class="node-group-note">
      Naming the first {{ dropped.length }} of {{ droppedCount }} removed.
    </p>

    <nav v-if="pageCount > 1" class="compare-pager" aria-label="Pages of nodes">
      <button type="button" class="button button-secondary button-compact" :disabled="page === 0" aria-label="Previous page" @click="page -= 1">
        <ChevronLeft :size="13" aria-hidden="true" />
      </button>
      <span class="mono" role="status">Rows {{ pageFrom }}–{{ pageTo }} of {{ rows.length }}</span>
      <button type="button" class="button button-secondary button-compact" :disabled="page >= pageCount - 1" aria-label="Next page" @click="page += 1">
        <ChevronRight :size="13" aria-hidden="true" />
      </button>
    </nav>
  </div>
</template>
