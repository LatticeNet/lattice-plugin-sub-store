<script setup lang="ts">
import { computed } from "vue";

import { BINDINGS } from "../client";
import { useHost } from "../host";
import EngineUnavailable from "../components/EngineUnavailable.vue";

const host = useHost();

// Conversion needs both the convert surface and the subscriptions list to
// select sources from.
const engineReady = computed(
  () => host.available(BINDINGS.convertTargets) && host.available(BINDINGS.subscriptionsList),
);
</script>

<template>
  <section>
    <EngineUnavailable
      v-if="!engineReady"
      feature="Config conversion"
    />
    <p v-else class="panel-empty">Conversion workspace goes here.</p>
  </section>
</template>
