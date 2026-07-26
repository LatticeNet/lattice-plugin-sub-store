<script setup lang="ts">
import { PackageOpen } from "@lucide/vue";

/**
 * Shown when the signed manifest does not declare the embedded-engine
 * service a screen needs. This is the expected production state until the
 * TASK-0002 engine contract lands — the panel says so honestly instead of
 * rendering dead controls.
 */
defineProps<{
  feature: string;
}>();
</script>

<template>
  <div class="panel-empty">
    <PackageOpen :size="22" aria-hidden="true" />
    <div class="panel-empty-copy">
      <h3>Embedded engine not available</h3>
      <p>
        {{ feature }} runs on the Sub-Store engine embedded in the plugin artifact.
        This plugin build only carries the external-Sub-Store adapter, so the tab
        activates when an engine-capable build is installed and its methods appear
        in the signed manifest.
      </p>
    </div>
  </div>
</template>
