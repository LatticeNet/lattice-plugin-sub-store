<script setup lang="ts">
import { computed, onActivated, onMounted, ref, watch } from "vue";
import { Workflow } from "@lucide/vue";
import {
  PcButton,
  PcEmptyState,
  PcNotice,
  PcPanel,
  PcPanelHeader,
  PcSkeleton,
} from "@latticenet/plugin-bridge/chassis";

import AttentionList from "../components/AttentionList.vue";
import LineageMap from "../components/LineageMap.vue";
import { useHost } from "../host";
import { hostOriginFromHash, postNavigate, sharesRoute } from "../navigate";
import { useLensChrome } from "../lensChrome";
import type { AttentionItem } from "../pipeline";
import { usePipeline } from "../usePipeline";
import { useOverlayEscape } from "../useOverlayEscape";

/**
 * L0, the default layer: what is wrong first, then the one picture.
 *
 * The attention list appears only when it has something to say. The picture
 * is the lineage map, which is the page's answer to the two questions the
 * flat lists never could: which link a client actually fetches, and what a
 * source feeds. Selecting a chip lights its path and opens the record in the
 * side panel, the same peek every table row opens.
 */
const host = useHost();
const chrome = useLensChrome();
// Escape closes the side panel a chip opened. The record tables arbitrate the
// key themselves; this screen has no other use for it, like Shares.
useOverlayEscape();
const pipe = usePipeline(host);

const selected = ref("");

function select(key: string): void {
  selected.value = key;
  if (key && pipe.item(key)) chrome.openRecord(key);
}

// A record opened from anywhere else (an attention claim, a table, the
// palette) is the one the map lights.
watch(
  () => chrome.openId.value,
  (id) => {
    if (id) selected.value = id;
  },
  { immediate: true },
);

/** The console's origin, when this frame may ask it to navigate. */
const shareOrigin = computed(() => hostOriginFromHash(typeof window === "undefined" ? "" : window.location.hash));

function act(item: AttentionItem): void {
  if (item.action.publish) {
    // The share form, opened on the file. Without a console to ask, the
    // file's panel, which says where shares are made.
    if (shareOrigin.value) postNavigate(window, sharesRoute(item.action.publish), shareOrigin.value);
    else chrome.openRecord(item.action.publish);
    return;
  }
  if (item.action.recordId) {
    chrome.openRecord(item.action.recordId);
    return;
  }
  if (item.action.view) chrome.openLens(item.action.view, item.action.facet, { search: item.action.search, focus: true });
}

/** The map's nodes the attention list names: records, and shares by their node. */
const attentionNodes = computed(() => {
  const out: string[] = [];
  for (const entry of pipe.attention.value) {
    if (entry.recordId) out.push(entry.recordId);
    if (entry.key.startsWith("share:")) {
      const node = pipe.lineage.value.shareNodes.get(entry.key.slice("share:".length));
      if (node) out.push(node);
    }
  }
  return [...new Set(out)];
});

const countable = computed(() => [...pipe.lineage.value.columns.source, ...pipe.lineage.value.columns.combination]);
watch(countable, (ids) => pipe.requestCounts(ids), { immediate: true });

async function load(): Promise<void> {
  await Promise.all([pipe.catalogue.state.value === "loading" ? Promise.resolve() : pipe.catalogue.reload(), pipe.shareStore.load()]);
}

// The shell reads the catalogue when the handshake lands; this layer re-reads
// on return, because a save on another layer changes the picture.
onActivated(() => {
  if (host.init.value && pipe.catalogue.state.value === "ready") void load();
});
onMounted(() => {
  if (host.init.value && pipe.catalogue.state.value === "idle") void load();
});
watch(host.init, (value) => {
  if (value && pipe.catalogue.state.value === "idle") void load();
});
</script>

<template>
  <section class="lens overview" aria-labelledby="overview-title">
    <h2 id="overview-title" class="pc-sr-only">Overview</h2>

    <PcPanel v-if="!host.init.value || pipe.catalogue.state.value === 'loading' || pipe.catalogue.state.value === 'idle'" label="Loading the overview">
      <PcSkeleton :count="5" label="Reading the record catalogue" />
    </PcPanel>

    <template v-else-if="pipe.catalogue.state.value === 'error'">
      <PcNotice tone="danger" title="The record catalogue could not be read">
        {{ pipe.catalogue.loadError.value }}
        <template #actions><PcButton compact @click="load()">Try again</PcButton></template>
      </PcNotice>
      <PcPanel label="Pipeline">
        <PcEmptyState kind="error" title="Nothing could be loaded">
          <p>This is not an empty store, it is an unanswered question. The map stays away until the list is read.</p>
        </PcEmptyState>
      </PcPanel>
    </template>

    <PcPanel v-else-if="!pipe.items.value.length" label="Pipeline">
      <PcEmptyState title="Nothing in the store yet">
        <template #icon><Workflow :size="26" aria-hidden="true" /></template>
        <p>
          A pipeline starts with a source: this fleet's nodes, a provider link, or nodes you paste. Combine sources,
          render them into a file for each client, and publish the file as a share.
        </p>
        <template #actions>
          <PcButton @click="chrome.openLens('records', { kind: 'source' }, { focus: true })">Go to Records</PcButton>
          <PcButton @click="chrome.openLens('settings', undefined, { focus: true })">Import from a Sub-Store</PcButton>
        </template>
      </PcEmptyState>
    </PcPanel>

    <template v-else>
      <AttentionList
        v-if="pipe.attention.value.length"
        :items="pipe.attention.value"
        @open="(id) => chrome.openRecord(id)"
        @act="act"
      />
      <PcNotice v-if="pipe.shareStore.error.value && !pipe.attention.value.some((i) => i.key === 'shares:unread')" tone="warning" title="The share list could not be read">
        {{ pipe.shareStore.error.value }}
      </PcNotice>

      <PcPanel class="overview-map" label="Pipeline">
        <PcPanelHeader
          title="Pipeline"
          description="Sources feed combinations, both render into client files, and shares publish them. Select a record to light its path."
        >
          <PcButton v-if="selected" compact @click="selected = ''">Clear selection</PcButton>
        </PcPanelHeader>
        <LineageMap :lineage="pipe.lineage.value" :facts="pipe.chipFacts" :selected="selected" :attention="attentionNodes" @select="select" />
      </PcPanel>
    </template>
  </section>
</template>
