<script setup lang="ts">
import LtEmptyState from "./lt/LtEmptyState.vue";

/**
 * Shown when the signed manifest does not declare the embedded-engine
 * service a screen needs. This is the expected production state until the
 * TASK-0002 engine contract lands. The panel says so honestly instead of
 * rendering dead controls.
 *
 * It used its own dashed panel; the three real "there is nothing here"
 * situations (empty, filtered to zero, failed) already share LtEmptyState, and
 * a fourth panel that looks almost but not quite like them is how a screen
 * stops reading as one product.
 */
defineProps<{
  feature: string;
}>();
</script>

<template>
  <LtEmptyState
    title="Embedded engine not available"
    :detail="`${feature} runs on the Sub-Store engine embedded in the plugin artifact. This build only carries the external-Sub-Store adapter, so the tab activates when an engine-capable build is installed and its methods appear in the signed manifest.`"
  />
</template>
