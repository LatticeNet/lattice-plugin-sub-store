<script setup lang="ts">
import { PcEmptyState, PcPanel } from "@latticenet/plugin-bridge/chassis";

import { t } from "../i18n";

/**
 * Shown when the signed manifest does not declare the embedded-engine
 * service a screen needs. The panel says so honestly instead of rendering
 * dead controls, in the chassis's permission-wall form so it reads the same
 * as every other "there is nothing here" state.
 */
defineProps<{
  feature: string;
}>();
</script>

<template>
  <PcPanel :label="t.engine.panel">
    <PcEmptyState kind="permission" :title="t.engine.title">
      <p>{{ t.engine.body(feature) }}</p>
    </PcEmptyState>
  </PcPanel>
</template>
