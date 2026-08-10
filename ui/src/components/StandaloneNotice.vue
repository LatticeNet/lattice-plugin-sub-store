<script setup lang="ts">
import { MonitorOff } from "@lucide/vue";

/**
 * What the frame says when it is opened outside the console.
 *
 * The handshake either failed or never came, which outside the console is
 * permanent: this page is a plugin frame, and alone it has no signed identity
 * and no data path. "Loading…" forever was the previous answer, and it read as
 * a slow start rather than as the page being in the wrong place.
 */
defineProps<{
  /** The bridge's own words, when it gave any — useful to whoever debugs this. */
  detail?: string;
}>();
</script>

<template>
  <section class="standalone-notice" aria-labelledby="standalone-title">
    <div class="title-mark" aria-hidden="true"><MonitorOff :size="19" /></div>
    <h2 id="standalone-title">This page runs inside the Lattice console</h2>
    <p>
      It is the Sub-Store extension's frame: the console hands it a signed identity and a data path
      when it embeds it, so on its own it has neither — and no way to reach your subscriptions.
    </p>
    <p class="standalone-where">
      Find it under <strong>Console → Extensions → Sub-Store</strong>.
    </p>
    <p v-if="detail" class="standalone-detail mono">{{ detail }}</p>
  </section>
</template>
