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
    title="Sub-Store methods are not available to this session"
    :detail="`${feature} needs the Sub-Store subscription methods, and this session cannot call them. Either the installed bundle does not declare them, or your token lacks the scope they require. Whoever installed the plugin can tell you which.`"
  />
</template>
