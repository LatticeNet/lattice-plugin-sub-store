<script setup lang="ts">
import { MonitorOff } from "@lucide/vue";
import { PcButton, PcEmptyState, PcPanel } from "@latticenet/plugin-bridge/chassis";

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
  <PcPanel label="Waiting for the Lattice console">
    <PcEmptyState kind="handshake" title="Waiting for the Lattice console">
      <template #icon><MonitorOff :size="26" aria-hidden="true" /></template>
      <p>
        This page is the Sub-Store extension's frame. The console hands it a session when it embeds
        it, and nothing has arrived yet. If you opened this address directly, that will not change,
        because the frame has no way to reach your subscriptions on its own. If you opened it from
        the console, this clears the moment the console answers.
      </p>
      <p>Find it under <strong>Console, then Extensions, then Sub-Store</strong>.</p>
      <p v-if="detail" class="pc-mono standalone-detail">{{ detail }}</p>
      <template #actions>
        <PcButton @click="reload()">Reload the page</PcButton>
      </template>
    </PcEmptyState>
  </PcPanel>
</template>
