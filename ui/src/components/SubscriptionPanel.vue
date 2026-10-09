<script setup lang="ts">
import { LoaderCircle, SquareArrowOutUpRight } from "@lucide/vue";
import { PcButton, PcSidePanel } from "@latticenet/plugin-bridge/chassis";

import NodeRows from "./NodeRows.vue";
import SubscriptionPublishControl from "./SubscriptionPublishControl";
import type { SubscriptionListItem } from "../client";
import { t } from "../i18n";
import type { PublishState } from "../shareState";
import type { UseSubscriptions } from "../useSubscriptions";

/**
 * The row panel: everything opened beside a record that is a view or a small
 * form rather than a question. Preview what its chain produces, upload the
 * rendered document to an operator target, or find out where its share lives.
 *
 * One component for the three because they are one surface with three bodies,
 * and as three v-else-if branches inside the list screen the only way to see
 * what a panel could contain was to read the screen. Each mode is what it
 * shows and nothing else; the record, the store and the published state are
 * handed in. The surface itself is the chassis's side panel, the same one every
 * plugin frame docks a record form into.
 */
defineProps<{
  mode: "preview" | "publish" | "share" | null;
  open: boolean;
  title: string;
  item: SubscriptionListItem | null;
  subs: UseSubscriptions;
  busyId: string | null;
  /** What the shares lens says about this record, if anything. */
  published: PublishState | null;
  /** Absent when the frame cannot ask the console to navigate. */
  shareOrigin: string | null;
  returnFocusTo: HTMLElement | null;
}>();

const emit = defineEmits<{
  close: [];
  publish: [destination: string, method: string, format: string];
  openShares: [item: SubscriptionListItem];
}>();
</script>

<template>
  <PcSidePanel :open="open" :title="title" size="record" :close-label="t.record.closePanel" :return-focus-to="returnFocusTo" @close="emit('close')">
    <template v-if="mode === 'preview'">
      <p v-if="subs.rowPreview.value?.loading" class="row-popover-note">
        <LoaderCircle :size="13" class="spin" aria-hidden="true" /> {{ t.panel.loading }}
      </p>
      <p v-else-if="subs.rowPreview.value?.error" class="row-popover-error" role="alert">
        {{ subs.rowPreview.value.error }}
      </p>
      <template v-else-if="subs.rowPreview.value">
        <p class="row-popover-note">{{ t.panel.nodesOnce(subs.rowPreview.value.count) }}</p>
        <NodeRows :nodes="subs.rowPreview.value.nodes" />
        <p v-if="subs.rowPreview.value.count > subs.rowPreview.value.nodes.length" class="row-popover-note">
          {{ t.panel.andMore(subs.rowPreview.value.count - subs.rowPreview.value.nodes.length) }}
        </p>
      </template>
    </template>

    <SubscriptionPublishControl
      v-else-if="mode === 'publish'"
      :saved="true"
      :read-only="!subs.canMutate.value"
      :busy="busyId === item?.id"
      :error="subs.actionError.value"
      @publish="(destination: string, method: string, format: string) => emit('publish', destination, method, format)"
    />

    <template v-else-if="mode === 'share'">
      <p v-if="item && published?.tone === 'warn'" class="row-popover-copy">
        {{ t.panel.renewBefore(published.title) }} <strong>{{ t.panel.publishingPath }}</strong>{{ t.panel.renewAfter }}
      </p>
      <template v-else>
        <p class="row-popover-copy">
          {{ t.panel.unreachableBefore }} <strong>{{ t.panel.publishingPath }}</strong>{{ t.panel.renewAfter }}
        </p>
        <p class="row-popover-note">{{ t.panel.alreadyPublished }}</p>
      </template>
      <div v-if="shareOrigin && item" class="empty-actions">
        <PcButton variant="primary" @click="emit('openShares', item)">
          <template #icon><SquareArrowOutUpRight :size="15" aria-hidden="true" /></template>
          {{ t.panel.openShares }}
        </PcButton>
      </div>
      <p v-else class="row-popover-note">{{ t.panel.noNavigate }}</p>
    </template>
  </PcSidePanel>
</template>
