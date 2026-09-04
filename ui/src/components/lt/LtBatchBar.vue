<script setup lang="ts">
/** Selection-mode action bar. Esc leaves selection; count stays visible. */
defineProps<{ count: number }>();
const emit = defineEmits<{ (e: "clear"): void }>();
</script>

<template>
  <div v-if="count > 0" class="lt-batchbar" role="toolbar" aria-label="Selection actions" @keydown.esc.stop="emit('clear')">
    <span class="lt-batchbar-count">{{ count }} selected</span>
    <div class="lt-batchbar-actions"><slot /></div>
    <button class="lt-batchbar-clear" type="button" @click="emit('clear')">Clear</button>
  </div>
</template>

<style scoped>
.lt-batchbar {
  /* Not sticky: the host sizes this frame to its content, so there is no
     scrollport for sticky to attach to and the bar simply never floats. It
     sits with the list instead, which is where the selection is. */
  position: relative;
  display: flex;
  align-items: center;
  gap: var(--space-3);
  margin-top: var(--space-3);
  padding: var(--space-2) var(--space-3);
  border: 1px solid var(--lt-border-strong);
  border-radius: var(--radius-md);
  background: var(--card);
  box-shadow: 0 6px 20px color-mix(in oklab, var(--foreground) 14%, transparent);
  z-index: 30;
}
.lt-batchbar-count { font-size: var(--lt-text-sm); font-weight: 600; }
.lt-batchbar-actions { display: flex; gap: var(--space-2); }
.lt-batchbar-clear {
  margin-left: auto;
  border: none;
  background: none;
  color: var(--muted-foreground);
  font-size: var(--lt-text-sm);
  cursor: pointer;
}
.lt-batchbar-clear:hover { color: var(--foreground); text-decoration: underline; }
</style>
