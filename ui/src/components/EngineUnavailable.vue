<script setup lang="ts">
import { PcEmptyState, PcPanel } from "@latticenet/plugin-bridge/chassis";

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
  <PcPanel label="Sub-Store methods are not available">
    <PcEmptyState kind="permission" title="Sub-Store methods are not available to this session">
      <p>
        {{ feature }} needs the Sub-Store subscription methods, and this session cannot call them.
        Either the installed bundle does not declare them, or your token lacks the scope they
        require. Whoever installed the plugin can tell you which.
      </p>
    </PcEmptyState>
  </PcPanel>
</template>
