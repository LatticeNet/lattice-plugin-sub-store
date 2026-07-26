<script setup lang="ts">
import { computed } from "vue";

import { BINDINGS } from "../client";
import { useHost } from "../host";
import EngineUnavailable from "../components/EngineUnavailable.vue";

const host = useHost();

// The whole tab gates on the engine's subscriptions service; until the signed
// manifest declares it there is nothing to load.
const engineReady = computed(() => host.available(BINDINGS.subscriptionsList));
</script>

<template>
  <section>
    <EngineUnavailable
      v-if="!engineReady"
      feature="Subscription management"
    />
    <p v-else class="panel-empty">Subscription list goes here.</p>
  </section>
</template>
