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
import { t } from "../i18n";
import { hostOriginFromHash, postNavigate, sharesRoute } from "../navigate";
import { useLensChrome } from "../lensChrome";
import type { AttentionItem } from "../pipeline";
import { decoyShares, isLegacyRecord, mixedBlock, recordName, type PlanShareRow } from "../planFlow";
import { planState } from "../fleet";
import { usePipeline } from "../usePipeline";
import { useOverlayEscape } from "../useOverlayEscape";
import { useSubscriptions } from "../useSubscriptions";

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
const subs = useSubscriptions(host);

/**
 * What design 28 S2 adds to the attention list (s2-plan sections 6 and 11):
 * the records that still serve line owner credentials, counted, with the
 * migration as the fix; a combination core refuses to serve until its
 * legacy members migrate; shares on fleet records that answer the decoy
 * because they name no identity; a plan whose apply failed; and the staged
 * revisions waiting for a publish or a plan. Worst first, before the S1
 * items of the same tone.
 */
const fleetAttention = computed<AttentionItem[]>(() => {
  const items = pipe.items.value;
  const shares = pipe.shareStore.shares.value as PlanShareRow[] | undefined;
  const out: AttentionItem[] = [];
  for (const item of items) {
    const legacy = mixedBlock(item, items);
    if (legacy?.length) {
      out.push({
        key: `mixed:${item.id}`,
        tone: "danger",
        claim: t.plans.attention.mixed(recordName(item), legacy.map((member) => recordName(member)).join(", ")),
        recordId: item.id,
        recordName: recordName(item),
        action: { label: t.attention.open, recordId: item.id },
      });
    }
    for (const plan of subs.plansByRecord.value[item.id] ?? []) {
      if (plan.subscription_id !== item.id || planState(plan) !== "failed") continue;
      out.push({
        key: `plan:${plan.approval_id}`,
        tone: "danger",
        claim: t.plans.attention.failed(recordName(item)),
        recordId: item.id,
        recordName: recordName(item),
        action: { label: t.attention.open, recordId: item.id },
      });
    }
  }
  const owners = items.filter((item) => isLegacyRecord(item));
  if (owners.length) {
    const one = owners.length === 1 ? owners[0]! : undefined;
    out.push({
      key: "owner-credentials",
      tone: "warning",
      claim: one ? t.plans.attention.ownerOne(recordName(one)) : t.plans.attention.owners(owners.length),
      recordId: one?.id,
      recordName: one ? recordName(one) : undefined,
      action: one ? { label: t.attention.open, recordId: one.id } : { label: t.attention.showThem, view: "records", facet: { kind: "source" } },
    });
  }
  const decoys = items.flatMap((item) => decoyShares(item, shares).map((share) => ({ item, share })));
  if (decoys.length) {
    const records = [...new Set(decoys.map((entry) => entry.item.id))];
    const one = records.length === 1 ? items.find((item) => item.id === records[0]) : undefined;
    out.push({
      key: "decoy-shares",
      tone: "warning",
      claim: t.plans.attention.decoys(decoys.length, one ? recordName(one) : ""),
      recordId: one?.id,
      recordName: one ? recordName(one) : undefined,
      action: one ? { label: t.attention.open, recordId: one.id } : { label: t.attention.showThem, view: "fleet" },
    });
  }
  const staged = items.filter((item) => item.staged_revision);
  if (staged.length) {
    const one = staged.length === 1 ? staged[0]! : undefined;
    out.push({
      key: "staged",
      tone: "neutral",
      claim: one ? t.plans.attention.stagedOne(recordName(one)) : t.plans.attention.staged(staged.length),
      recordId: one?.id,
      recordName: one ? recordName(one) : undefined,
      action: one ? { label: t.attention.open, recordId: one.id } : { label: t.attention.showThem, view: "records" },
    });
  }
  return out;
});

// A staged revision's plan is what can have failed; read those plans so a failed apply is said here too.
watch(
  () => pipe.items.value.filter((item) => item.staged_revision).map((item) => item.id).join(","),
  (ids) => {
    if (ids) void subs.loadPlans(ids.split(","));
  },
  { immediate: true },
);

const TONE_ORDER = { danger: 0, warning: 1, neutral: 2 } as const;
const attention = computed<AttentionItem[]>(() =>
  [...fleetAttention.value, ...pipe.attention.value]
    .map((item, index) => ({ item, index }))
    .sort((a, b) => TONE_ORDER[a.item.tone] - TONE_ORDER[b.item.tone] || a.index - b.index)
    .map(({ item }) => item),
);

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
  for (const entry of attention.value) {
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
    <h2 id="overview-title" class="pc-sr-only">{{ t.overview.title }}</h2>

    <PcPanel v-if="!host.init.value || pipe.catalogue.state.value === 'loading' || pipe.catalogue.state.value === 'idle'" :label="t.overview.loadingPanel">
      <PcSkeleton :count="5" :label="t.overview.loading" />
    </PcPanel>

    <template v-else-if="pipe.catalogue.state.value === 'error'">
      <PcNotice tone="danger" :title="t.overview.loadFailed">
        {{ pipe.catalogue.loadError.value }}
        <template #actions><PcButton compact @click="load()">{{ t.common.tryAgain }}</PcButton></template>
      </PcNotice>
      <PcPanel :label="t.overview.pipeline">
        <PcEmptyState kind="error" :title="t.overview.nothingLoaded">
          <p>{{ t.overview.nothingLoadedBody }}</p>
        </PcEmptyState>
      </PcPanel>
    </template>

    <PcPanel v-else-if="!pipe.items.value.length" :label="t.overview.pipeline">
      <PcEmptyState :title="t.overview.emptyTitle">
        <template #icon><Workflow :size="26" aria-hidden="true" /></template>
        <p>{{ t.overview.emptyBody }}</p>
        <template #actions>
          <PcButton @click="chrome.openLens('records', { kind: 'source' }, { focus: true })">{{ t.overview.goToRecords }}</PcButton>
          <PcButton @click="chrome.openLens('settings', undefined, { focus: true })">{{ t.overview.importFromSubStore }}</PcButton>
        </template>
      </PcEmptyState>
    </PcPanel>

    <template v-else>
      <AttentionList
        v-if="attention.length"
        :items="attention"
        @open="(id) => chrome.openRecord(id)"
        @act="act"
      />
      <PcNotice v-if="pipe.shareStore.error.value && !attention.some((i) => i.key === 'shares:unread')" tone="warning" :title="t.overview.sharesUnread">
        {{ pipe.shareStore.error.value }}
      </PcNotice>

      <PcPanel class="overview-map" :label="t.overview.pipeline">
        <PcPanelHeader :title="t.overview.pipeline" :description="t.overview.mapDescription">
          <PcButton v-if="selected" compact @click="selected = ''">{{ t.overview.clearSelection }}</PcButton>
        </PcPanelHeader>
        <LineageMap :lineage="pipe.lineage.value" :facts="pipe.chipFacts" :selected="selected" :attention="attentionNodes" @select="select" />
      </PcPanel>
    </template>
  </section>
</template>
