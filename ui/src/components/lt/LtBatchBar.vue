<script setup lang="ts">
/** Selection-mode action bar. Esc leaves selection; count stays visible. */
defineProps<{ count: number }>();
const emit = defineEmits<{ (e: "clear"): void }>();
</script>

<template>
  <div v-if="count > 0" class="lt-batchbar" role="toolbar" aria-label="Selection actions" @keydown.esc="emit('clear')">
    <span class="lt-batchbar-count">{{ count }} selected</span>
    <div class="lt-batchbar-actions"><slot /></div>
    <button class="lt-batchbar-clear" type="button" @click="emit('clear')">Clear</button>
  </div>
</template>

<style scoped>
.lt-batchbar {
  position: sticky;
  bottom: var(--lt-space-3);
  display: flex;
  align-items: center;
  gap: var(--lt-space-3);
  margin-top: var(--lt-space-3);
  padding: var(--lt-space-2) var(--lt-space-3);
  border: 1px solid var(--lt-border-strong);
  border-radius: var(--lt-radius);
  background: var(--lt-surface);
  box-shadow: 0 6px 20px color-mix(in oklab, var(--lt-fg) 14%, transparent);
  z-index: 30;
}
.lt-batchbar-count { font-size: var(--lt-text-sm); font-weight: 600; }
.lt-batchbar-actions { display: flex; gap: var(--lt-space-2); }
.lt-batchbar-clear {
  margin-left: auto;
  border: none;
  background: none;
  color: var(--lt-fg-muted);
  font-size: var(--lt-text-sm);
  cursor: pointer;
}
.lt-batchbar-clear:hover { color: var(--lt-fg); text-decoration: underline; }
</style>
