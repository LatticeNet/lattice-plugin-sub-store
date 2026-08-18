<script setup lang="ts">
/** One component, three honest situations: truly empty, filtered to zero,
 *  failed to load. The kind decides copy posture; actions come from slots. */
defineProps<{
  title: string;
  detail?: string;
  kind?: "empty" | "no-results" | "error";
}>();
</script>

<template>
  <div class="lt-empty" :class="`k-${kind ?? 'empty'}`" :role="kind === 'error' ? 'alert' : 'status'">
    <p class="lt-empty-title">{{ title }}</p>
    <p v-if="detail" class="lt-empty-detail">{{ detail }}</p>
    <div v-if="$slots.default" class="lt-empty-actions"><slot /></div>
  </div>
</template>

<style scoped>
.lt-empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: var(--lt-space-2);
  padding: var(--lt-space-6) var(--lt-space-4);
  text-align: center;
  border: 1px dashed var(--lt-border);
  border-radius: var(--lt-radius);
}
.lt-empty.k-error { border-color: color-mix(in oklab, var(--lt-danger) 40%, var(--lt-border) 60%); }
.lt-empty-title { font-size: var(--lt-text-md); color: var(--lt-fg); margin: 0; }
.k-error .lt-empty-title { color: var(--lt-danger); }
.lt-empty-detail { font-size: var(--lt-text-sm); color: var(--lt-fg-muted); margin: 0; max-width: 48ch; }
.lt-empty-actions { display: flex; gap: var(--lt-space-2); margin-top: var(--lt-space-1); }
</style>
