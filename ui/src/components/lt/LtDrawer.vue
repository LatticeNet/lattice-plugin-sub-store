<script setup lang="ts">
import { X } from "@lucide/vue";

/** Right-side panel for row-scoped work (preview, publish, share guidance).
 *  One drawer at a time; Esc and the scrim both close it. */
defineProps<{ open: boolean; title: string }>();
const emit = defineEmits<{ (e: "close"): void }>();
</script>

<template>
  <div v-if="open" class="lt-drawer-scrim" @click.self="emit('close')">
    <aside class="lt-drawer" role="dialog" aria-modal="true" :aria-label="title" @keydown.esc="emit('close')">
      <header class="lt-drawer-head">
        <h3 class="lt-drawer-title">{{ title }}</h3>
        <button class="lt-drawer-close" type="button" aria-label="Close" @click="emit('close')">
          <X :size="16" aria-hidden="true" />
        </button>
      </header>
      <div class="lt-drawer-body"><slot /></div>
    </aside>
  </div>
</template>

<style scoped>
.lt-drawer-scrim {
  position: fixed;
  inset: 0;
  background: color-mix(in oklab, var(--lt-fg) 24%, transparent);
  z-index: 50;
}
.lt-drawer {
  position: absolute;
  top: 0;
  right: 0;
  bottom: 0;
  width: min(420px, 92vw);
  background: var(--lt-surface);
  border-left: 1px solid var(--lt-border);
  display: flex;
  flex-direction: column;
  animation: lt-drawer-in var(--lt-dur) var(--lt-ease);
}
@keyframes lt-drawer-in {
  from { transform: translateX(24px); opacity: 0.6; }
}
.lt-drawer-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: var(--lt-space-3) var(--lt-space-4);
  border-bottom: 1px solid var(--lt-border);
}
.lt-drawer-title { margin: 0; font-size: var(--lt-text-md); font-weight: 600; }
.lt-drawer-close {
  border: none;
  background: none;
  color: var(--lt-fg-muted);
  cursor: pointer;
  display: inline-flex;
  padding: var(--lt-space-1);
  border-radius: var(--lt-radius-sm);
}
.lt-drawer-close:hover { background: var(--lt-surface-2); color: var(--lt-fg); }
.lt-drawer-close:focus-visible { outline: none; box-shadow: var(--lt-focus-ring); }
.lt-drawer-body { padding: var(--lt-space-4); overflow-y: auto; flex: 1; }
</style>
