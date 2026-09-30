<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { PcButton, PcKindChip, PcNotice, PcSidePanel, PcStateDot } from "@latticenet/plugin-bridge/chassis";

import { KIND_COLLECTION, KIND_FILE, KIND_SUB } from "../client";
import { SHARES_LIST_ROUTE, hostOriginFromHash, postNavigate, sharesRoute } from "../navigate";
import {
  clientOfFile,
  formatExpiry,
  formatUsage,
  isProviderLink,
  providerFigures,
  reachingShares,
  sourceKindLabel,
} from "../pipeline";
import type { Pipeline } from "../usePipeline";
import { copyText } from "../hostClipboard";
import { refreshStateFor, shareLinkOf, stateTone } from "../shareState";
import LtManualCopy from "./lt/LtManualCopy.vue";
import RecordActions from "./RecordActions.vue";
import UsageBar from "./UsageBar.vue";

/**
 * L2 peek: one record beside the layer it was opened from, addressable as
 * `?open=<id>`. Identity, state, the figures that matter for its kind, what it
 * draws from and what it feeds (each a link to that record's own peek), and
 * two ways on: the full page, or the editor. Its footer carries the table
 * row's menu (RecordActions), so refreshing, copying, exporting and deleting
 * do not need the table; every other write stays behind Edit.
 */
const props = defineProps<{ id: string; pipe: Pipeline; canEdit: boolean }>();

/*
 * From 768px the panel sits beside the table and the rows stay live, so a
 * click on another row swaps the record without closing the panel. Closing
 * then returns focus to that record's row or map chip, not to whatever opened
 * the panel first. A fresh open leaves it to the chassis, which returns focus
 * to the opener (a row, an attention item, the palette).
 */
const returnTarget = ref<HTMLElement | null>(null);
function rowFor(id: string): HTMLElement | null {
  if (typeof document === "undefined") return null;
  const key = CSS.escape(id);
  return document.querySelector<HTMLElement>(`[data-record-open="${key}"]`) ?? document.querySelector<HTMLElement>(`[data-map-key="${key}"]`);
}
watch(() => props.id, (id, previous) => {
  if (id && previous && id !== previous) returnTarget.value = rowFor(id);
  else if (id && !previous) returnTarget.value = null;
}, { flush: "post" });
const emit = defineEmits<{
  close: [];
  open: [id: string];
  page: [id: string];
  edit: [id: string];
  /** The record was deleted from the panel's menu. */
  deleted: [kind: string, text: string];
}>();

/** What the menu's last action did, until another record is shown. */
const status = ref<{ text: string; tone: "success" | "danger" } | null>(null);
/** A share link the clipboard refused, shown to copy by hand. */
const manualLink = ref("");
watch(
  () => props.id,
  () => {
    status.value = null;
    manualLink.value = "";
  },
);

/** The share-copy path every surface uses: the clipboard, or the link on screen. */
async function copyShare(link: string): Promise<void> {
  manualLink.value = "";
  if (await copyText(link)) status.value = { text: "Copied the link.", tone: "success" };
  else manualLink.value = link;
}

const record = computed(() => (props.id ? props.pipe.item(props.id) : undefined));
const kind = computed(() => record.value?.kind || KIND_SUB);
const health = computed(() => props.pipe.health(props.id));
const figures = computed(() => (record.value && isProviderLink(record.value) ? providerFigures(record.value) : null));

watch(
  () => props.id,
  (id) => {
    if (id) props.pipe.requestCounts([id]);
  },
  { immediate: true },
);

const kindLabel = computed(() => {
  const item = record.value;
  if (!item) return "";
  if (kind.value === KIND_COLLECTION) return "Combination";
  if (kind.value === KIND_FILE) {
    const type = item.file_type === "plain" ? "plain text" : item.file_type === "script" ? "script" : "configuration";
    return `File, ${type}`;
  }
  return sourceKindLabel(item);
});

function nameOf(id: string): string {
  const item = props.pipe.item(id);
  return item ? item.display_name || item.name : id;
}

const upstream = computed(() => props.pipe.lineage.value.upstream.get(props.id) ?? []);
const downstreamRecords = computed(() =>
  (props.pipe.lineage.value.downstream.get(props.id) ?? []).filter((id) => props.pipe.lineage.value.nodes.get(id)?.stage !== "share"),
);
const broken = computed(() => props.pipe.lineage.value.broken.filter((ref) => ref.owner === props.id));
const shares = computed(() => (props.pipe.shares.value ?? []).filter((share) => share.subscription_id === props.id));
/* Shares that serve this record through what it feeds, for a record no share
 * names itself: "/cdcd, via merge-cd-openjobs and for-cdcd-loon". */
const reachedThrough = computed(() =>
  reachingShares(props.pipe.lineage.value, props.id)
    .filter((reach) => reach.via.length)
    .map((reach) => {
      const node = props.pipe.lineage.value.nodes.get(reach.share);
      const off = node?.share && !node.share.enabled ? " (disabled)" : "";
      return { key: reach.share, label: `${node?.label ?? reach.share}${off}`, via: reach.via.map(nameOf).join(" → ") };
    }),
);

const steps = computed(() => {
  const item = record.value;
  if (!item) return "";
  const n = item.step_count;
  if (!n) return "none";
  return `${n} operation${n === 1 ? "" : "s"}${item.disabled_step_count ? `, ${item.disabled_step_count} off` : ""}`;
});

const refresh = computed(() => (record.value && isProviderLink(record.value) ? refreshStateFor(record.value, props.pipe.now.value) : null));
const client = computed(() => (record.value && kind.value === KIND_FILE ? clientOfFile(record.value.name) : null));

const shareOrigin = computed(() => hostOriginFromHash(typeof window === "undefined" ? "" : window.location.hash));
function publish(): void {
  if (!shareOrigin.value || !record.value) return;
  postNavigate(window, shares.value.length ? SHARES_LIST_ROUTE : sharesRoute(record.value.id), shareOrigin.value);
}
</script>

<template>
  <PcSidePanel
    :open="!!id && !!record"
    :title="record ? record.display_name || record.name : ''"
    :description="kindLabel"
    close-label="Close the side panel"
    :return-focus-to="returnTarget"
    @close="emit('close')"
  >
    <div v-if="record" class="peek">
      <PcNotice v-if="status" :tone="status.tone">{{ status.text }}</PcNotice>
      <LtManualCopy v-if="manualLink" :value="manualLink" subject="link" />
      <div class="peek-state">
        <PcStateDot :tone="health.tone" :label="health.label" :title="health.title" />
        <PcKindChip v-if="record.imported" label="migrated" title="Imported from a standalone Sub-Store" />
      </div>
      <!-- The reason behind a broken state; a warning's facts are in the list below. -->
      <p v-if="health.tone === 'error'" class="peek-why">{{ health.title }}</p>

      <dl class="peek-facts">
        <template v-if="kind !== KIND_FILE">
          <dt>Nodes</dt>
          <dd class="peek-mono" :title="pipe.nodesTitle(id)">{{ pipe.nodes(id) }}<span v-if="pipe.nodes(id).includes('→')" class="peek-note">in → out</span></dd>
          <dt>Operations</dt>
          <dd>{{ steps }}</dd>
        </template>

        <template v-if="figures">
          <dt>Provider</dt>
          <dd class="peek-usage">
            <UsageBar :figures="figures" />
            <span class="peek-note">{{ [formatUsage(figures), formatExpiry(figures, pipe.now.value)].filter(Boolean).join(", ") }}</span>
          </dd>
        </template>
        <template v-if="refresh">
          <dt>Last refresh</dt>
          <dd><PcStateDot :tone="stateTone(refresh.tone)" :label="refresh.label" :title="refresh.title || refresh.label" /></dd>
        </template>

        <template v-if="kind === KIND_COLLECTION">
          <dt>Members</dt>
          <dd>
            <ul class="peek-links">
              <li v-for="member in upstream" :key="member">
                <button type="button" class="peek-link" @click="emit('open', member)">{{ nameOf(member) }}</button>
              </li>
              <li v-for="ref in broken" :key="ref.ref" class="peek-broken">{{ ref.ref }} <span>{{ ref.reason }}</span></li>
              <li v-if="!upstream.length && !broken.length" class="peek-note">No members resolve</li>
            </ul>
            <p v-if="record.member_tags?.length" class="peek-note">Plus every source tagged {{ record.member_tags.join(", ") }}</p>
          </dd>
        </template>

        <template v-if="kind === KIND_FILE">
          <dt>Renders</dt>
          <dd>
            <button v-if="upstream[0]" type="button" class="peek-link" @click="emit('open', upstream[0]!)">{{ nameOf(upstream[0]!) }}</button>
            <span v-else-if="broken[0]" class="peek-broken">{{ broken[0].ref }} <span>{{ broken[0].reason }}</span></span>
            <span v-else class="peek-note">Nothing: the document is served as written</span>
          </dd>
          <dt>Client</dt>
          <dd>
            <span v-if="client">{{ client.label }}</span>
            <span v-else class="peek-note" title="The file's name does not name a client app.">Not named</span>
          </dd>
        </template>

        <dt>Feeds</dt>
        <dd>
          <ul v-if="downstreamRecords.length" class="peek-links">
            <li v-for="child in downstreamRecords" :key="child">
              <button type="button" class="peek-link" @click="emit('open', child)">{{ nameOf(child) }}</button>
            </li>
          </ul>
          <span v-else class="peek-note">{{ kind === KIND_FILE ? "A file is the end of the chain" : "Nothing uses this record" }}</span>
        </dd>

        <dt>Published</dt>
        <dd>
          <template v-if="pipe.shares.value === undefined">
            <span class="peek-note">{{ pipe.shareStore.error.value || "The share list has not been read" }}</span>
          </template>
          <ul v-else-if="shares.length" class="peek-links">
            <li v-for="share in shares" :key="share.share_id" class="peek-share">
              <span class="peek-mono">/{{ share.slug }}</span>
              <span class="peek-note">{{ pipe.liveShares(id).includes(share) ? "live" : share.enabled ? "expired" : "disabled" }}</span>
              <PcButton v-if="shareLinkOf(share)" compact @click="copyShare(shareLinkOf(share))">Copy link</PcButton>
            </li>
          </ul>
          <ul v-else-if="reachedThrough.length" class="peek-links">
            <li v-for="reach in reachedThrough" :key="reach.key" class="peek-share">
              <span class="peek-note">Not published itself; clients get it through <span class="peek-mono">{{ reach.label }}</span>, via {{ reach.via }}</span>
            </li>
          </ul>
          <span v-else class="peek-note">Not published, so no client can fetch it</span>
          <PcButton v-if="shareOrigin && pipe.shares.value !== undefined" compact class="peek-publish" @click="publish()">
            {{ shares.length ? "Open in Publishing" : "Publish" }}
          </PcButton>
        </dd>

        <template v-if="record.tags?.length">
          <dt>Tags</dt>
          <dd>{{ record.tags.join(", ") }}</dd>
        </template>
        <template v-if="record.remark">
          <dt>Remark</dt>
          <dd>{{ record.remark }}</dd>
        </template>
      </dl>
    </div>

    <template #footer>
      <RecordActions
        v-if="record"
        :id="id"
        :pipe="pipe"
        @status="(text, tone) => (status = { text, tone })"
        @deleted="(kind, text) => emit('deleted', kind, text)"
      />
      <PcButton :disabled="!canEdit" :title="canEdit ? 'Change this record' : 'This session cannot change records here.'" @click="emit('edit', id)">Edit</PcButton>
      <PcButton variant="primary" @click="emit('page', id)">Open page</PcButton>
    </template>
  </PcSidePanel>
</template>
