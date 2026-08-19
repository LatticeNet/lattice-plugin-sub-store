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
  margin-top: var(--lt-space-3);
  padding: var(--lt-space-8) var(--lt-space-4);
  text-align: center;
  border: 1px dashed var(--lt-border);
  border-radius: var(--lt-radius);
}
.lt-empty.k-error { border-color: var(--lt-danger-border); background: var(--lt-danger-soft); }
.lt-empty-title { font-size: var(--lt-text-lg); font-weight: 600; color: var(--lt-fg); margin: 0; }
.k-error .lt-empty-title { color: var(--lt-danger); }
.lt-empty-detail {
  font-size: var(--lt-text-md);
  line-height: var(--lt-leading);
  color: var(--lt-fg-muted);
  margin: 0;
  max-width: 62ch;
}
/* Buttons sit side by side; a block that asks for the full width (the import
   form) takes its own line rather than being squeezed next to them. */
.lt-empty-actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: center;
  gap: var(--lt-space-2);
  margin-top: var(--lt-space-2);
  width: 100%;
}
</style>
