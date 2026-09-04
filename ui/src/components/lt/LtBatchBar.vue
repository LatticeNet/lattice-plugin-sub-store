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
  /* Pinned to the foot of the frame, and deliberately out of the flow.
     It used to sit in the flow above the list, which meant ticking the first
     checkbox inserted a 42px block and dropped every row 42px in the same
     frame. The row pitch is 40px, so the point the cursor was aiming at for
     the second row was now occupied by the checkbox just ticked: the second
     click cleared the first selection, the bar vanished, and the count reset
     with nothing said. Selecting two rows by clicking two checkboxes was not
     possible without re-aiming after every click.
     The frame is a viewport now (PluginFrameHost sizes it and both the panel
     and the modals are fixed inside it), so the bar can float instead: rows
     never move, and the count and its actions stay reachable however far down
     a long list the selection is. It sits at the inline level, under the side
     panel and the modals, because while one of those is open the work is
     there. */
  position: fixed;
  left: 50%;
  bottom: var(--space-4);
  transform: translateX(-50%);
  width: max-content;
  max-width: calc(100% - var(--space-6));
  display: flex;
  align-items: center;
  gap: var(--space-3);
  padding: var(--space-2) var(--space-3);
  border: 1px solid var(--lt-border-strong);
  border-radius: var(--radius-lg);
  background: var(--card);
  box-shadow: var(--shadow-overlay);
  z-index: var(--lt-z-inline);
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
