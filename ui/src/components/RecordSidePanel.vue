<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { PcButton, PcKindChip, PcNotice, PcSidePanel, PcStateDot } from "@latticenet/plugin-bridge/chassis";

import { KIND_COLLECTION, KIND_FILE, KIND_SUB } from "../client";
import { t } from "../i18n";
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
import { isFlagged } from "../recordTable";
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

/*
 * A panel restored from the address (a reload onto `?open=`, a pasted link)
 * opened with nothing focused, so the chassis had no opener to give focus
 * back to and Escape left it on <body>, although the record's row or chip
 * was on screen. Read at the moment the panel opens, before it takes focus;
 * on close such a panel hands focus to that row or chip.
 */
let openedWithoutOpener = false;
watch(
  () => !!props.id && !!props.pipe.item(props.id),
  (open, was) => {
    if (!open || was) return;
    const active = typeof document === "undefined" ? null : document.activeElement;
    openedWithoutOpener = !active || active === document.body;
  },
  { flush: "sync" },
);
function close(): void {
  if (openedWithoutOpener && !returnTarget.value) returnTarget.value = rowFor(props.id);
  emit("close");
}
const emit = defineEmits<{
  close: [];
  open: [id: string];
  page: [id: string];
  edit: [id: string];
  /** The record was deleted from the panel's menu; `shares` are the ones it left serving nothing. */
  deleted: [kind: string, text: string, shares: string[]];
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
  if (await copyText(link)) status.value = { text: t.record.copiedLink, tone: "success" };
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
  if (kind.value === KIND_COLLECTION) return t.record.kindCombination;
  if (kind.value === KIND_FILE) return t.record.fileKind[item.file_type === "plain" ? "plain" : item.file_type === "script" ? "script" : "config"];
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
      const label = node?.label ?? reach.share;
      return { key: reach.share, label: node?.share && !node.share.enabled ? t.record.shareDisabled(label) : label, via: reach.via.map(nameOf).join(" → ") };
    }),
);

const steps = computed(() => {
  const item = record.value;
  if (!item) return "";
  const n = item.step_count;
  if (!n) return t.record.stepsNone;
  return t.record.stepsCount(n, item.disabled_step_count || 0);
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
    :close-label="t.record.closePanel"
    :return-focus-to="returnTarget"
    @close="close()"
  >
    <div v-if="record" class="peek">
      <PcNotice v-if="status" :tone="status.tone">{{ status.text }}</PcNotice>
      <LtManualCopy v-if="manualLink" :value="manualLink" subject="link" />
      <div class="peek-state">
        <PcStateDot :tone="health.tone" :label="health.label" :title="health.title" />
        <PcKindChip v-if="record.imported" :label="t.records.migratedTag" :title="t.record.importedTitle" />
        <PcStateDot v-if="isFlagged(record)" tone="warning" :label="t.records.flagged" :title="t.records.flaggedTitle" data-testid="record-flagged" />
      </div>
      <!-- The reason behind a broken state; a warning's facts are in the list below. -->
      <p v-if="health.tone === 'error'" class="peek-why">{{ health.title }}</p>
      <!-- The flag's reason in words, with the way out: Edit, below. -->
      <p v-if="isFlagged(record)" class="peek-why">{{ t.records.flaggedTitle }}</p>

      <dl class="peek-facts">
        <template v-if="kind !== KIND_FILE">
          <dt>{{ t.record.nodes }}</dt>
          <dd class="peek-mono" :title="pipe.nodesTitle(id)">{{ pipe.nodes(id) }}<span v-if="pipe.nodes(id).includes('→')" class="peek-note">{{ t.record.inOut }}</span></dd>
          <dt>{{ t.record.steps }}</dt>
          <dd>{{ steps }}</dd>
        </template>

        <template v-if="figures">
          <dt>{{ t.record.provider }}</dt>
          <dd class="peek-usage">
            <UsageBar :figures="figures" />
            <span class="peek-note">{{ t.common.joinFacts([formatUsage(figures), formatExpiry(figures, pipe.now.value)].filter(Boolean)) }}</span>
          </dd>
        </template>
        <template v-if="refresh">
          <dt>{{ t.record.lastRefresh }}</dt>
          <dd><PcStateDot :tone="stateTone(refresh.tone)" :label="refresh.label" :title="refresh.title || refresh.label" /></dd>
        </template>

        <template v-if="kind === KIND_COLLECTION">
          <dt>{{ t.record.members }}</dt>
          <dd>
            <ul class="peek-links">
              <li v-for="member in upstream" :key="member">
                <button type="button" class="peek-link" @click="emit('open', member)">{{ nameOf(member) }}</button>
              </li>
              <li v-for="ref in broken" :key="ref.ref" class="peek-broken">{{ ref.ref }} <span>{{ ref.reason }}</span></li>
              <li v-if="!upstream.length && !broken.length" class="peek-note">{{ t.record.noMembersResolve }}</li>
            </ul>
            <p v-if="record.member_tags?.length" class="peek-note">{{ t.record.plusTagged(record.member_tags.join(", ")) }}</p>
          </dd>
        </template>

        <template v-if="kind === KIND_FILE">
          <dt>{{ t.record.renders }}</dt>
          <dd>
            <button v-if="upstream[0]" type="button" class="peek-link" @click="emit('open', upstream[0]!)">{{ nameOf(upstream[0]!) }}</button>
            <span v-else-if="broken[0]" class="peek-broken">{{ broken[0].ref }} <span>{{ broken[0].reason }}</span></span>
            <span v-else class="peek-note">{{ t.record.servedAsWritten }}</span>
          </dd>
          <dt>{{ t.record.client }}</dt>
          <dd>
            <span v-if="client">{{ client.label }}</span>
            <span v-else class="peek-note" :title="t.record.clientNotNamedTitle">{{ t.record.clientNotNamed }}</span>
          </dd>
        </template>

        <dt>{{ t.record.feeds }}</dt>
        <dd>
          <ul v-if="downstreamRecords.length" class="peek-links">
            <li v-for="child in downstreamRecords" :key="child">
              <button type="button" class="peek-link" @click="emit('open', child)">{{ nameOf(child) }}</button>
            </li>
          </ul>
          <span v-else class="peek-note">{{ kind === KIND_FILE ? t.record.fileEnd : t.record.nothingUses }}</span>
        </dd>

        <dt>{{ t.record.published }}</dt>
        <dd>
          <template v-if="pipe.shares.value === undefined">
            <span class="peek-note">{{ pipe.shareStore.error.value || t.record.sharesUnread }}</span>
          </template>
          <ul v-else-if="shares.length" class="peek-links">
            <li v-for="share in shares" :key="share.share_id" class="peek-share">
              <span class="peek-mono">/{{ share.slug }}</span>
              <span class="peek-note">{{ pipe.liveShares(id).includes(share) ? t.shareState.live : share.enabled ? t.shareState.expired : t.shareState.disabled }}</span>
              <PcButton v-if="shareLinkOf(share)" compact @click="copyShare(shareLinkOf(share))">{{ t.record.copyLink }}</PcButton>
            </li>
          </ul>
          <ul v-else-if="reachedThrough.length" class="peek-links">
            <li v-for="reach in reachedThrough" :key="reach.key" class="peek-share">
              <span class="peek-note">{{ t.record.reachedBefore }} <span class="peek-mono">{{ reach.label }}</span>{{ t.record.reachedAfter(reach.via) }}</span>
            </li>
          </ul>
          <span v-else class="peek-note">{{ t.record.notPublished }}</span>
          <PcButton v-if="shareOrigin && pipe.shares.value !== undefined" compact class="peek-publish" @click="publish()">
            {{ shares.length ? t.records.openInPublishing : t.record.publish }}
          </PcButton>
        </dd>

        <template v-if="record.tags?.length">
          <dt>{{ t.record.tags }}</dt>
          <dd>{{ record.tags.join(", ") }}</dd>
        </template>
        <template v-if="record.remark">
          <dt>{{ t.record.remark }}</dt>
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
        @deleted="(kind, text, broken) => emit('deleted', kind, text, broken)"
      />
      <PcButton :disabled="!canEdit" :title="canEdit ? t.record.editTitle : t.record.editBlocked" @click="emit('edit', id)">{{ t.actions.edit }}</PcButton>
      <PcButton variant="primary" @click="emit('page', id)">{{ t.record.openPage }}</PcButton>
    </template>
  </PcSidePanel>
</template>
