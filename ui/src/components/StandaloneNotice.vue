<script setup lang="ts">
import { MonitorOff } from "@lucide/vue";
import { PcButton, PcEmptyState, PcPanel } from "@latticenet/plugin-bridge/chassis";

import { t } from "../i18n";

/**
 * What the frame says when it is opened outside the console.
 *
 * The handshake either failed or never came, which outside the console is
 * permanent: this page is a plugin frame, and alone it has no signed identity
 * and no data path. "Loading…" forever was the previous answer, and it read as
 * a slow start rather than as the page being in the wrong place.
 */
defineProps<{
  /** The bridge's own words, when it gave any, useful to whoever debugs this. */
  detail?: string;
}>();

function reload(): void {
  window.location.reload();
}
</script>

<template>
  <PcPanel :label="t.standalone.title">
    <PcEmptyState kind="handshake" :title="t.standalone.title">
      <template #icon><MonitorOff :size="26" aria-hidden="true" /></template>
      <p>{{ t.standalone.body }}</p>
      <p>{{ t.standalone.findBefore }} <strong>{{ t.standalone.findPath }}</strong>{{ t.standalone.findAfter }}</p>
      <p v-if="detail" class="pc-mono standalone-detail">{{ detail }}</p>
      <template #actions>
        <PcButton @click="reload()">{{ t.standalone.reload }}</PcButton>
      </template>
    </PcEmptyState>
  </PcPanel>
</template>
