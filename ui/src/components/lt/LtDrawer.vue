<script setup lang="ts">
import { nextTick, ref, watch } from "vue";
import { X } from "@lucide/vue";

import { trapDialogTab } from "../../dialogFocus";

/**
 * Right-side panel for row-scoped work (preview, publish, share guidance).
 * One drawer at a time; Esc and the scrim both close it.
 *
 * Two things this has to get right that a normal page does not. The frame is a
 * viewport, but this row-scoped drawer stays anchored beside the record that
 * opened it rather than replacing the whole pane. Escape only reaches a
 * handler on an element that has focus, so the panel takes focus when it opens;
 * without that the key did nothing until the operator had tabbed inside,
 * which is the opposite of an escape hatch.
 */
const props = defineProps<{ open: boolean; title: string; anchorTop?: number }>();
const emit = defineEmits<{ (e: "close"): void }>();

const panel = ref<HTMLElement | null>(null);
watch(
  () => props.open,
  async (open) => {
    if (!open) return;
    await nextTick();
    panel.value?.focus();
  },
);

function onTab(event: KeyboardEvent): void {
  if (panel.value) trapDialogTab(event, panel.value);
}
</script>

<template>
  <div v-if="open" class="lt-drawer-scrim" @click.self="emit('close')">
    <aside
      ref="panel"
      class="lt-drawer"
      role="dialog"
      aria-modal="true"
      tabindex="-1"
      :aria-label="title"
      :style="{ '--overlay-anchor-top': `${anchorTop ?? 0}px` }"
      @keydown.esc="emit('close')"
      @keydown.tab="onTab"
    >
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
  /* Absolute against .workspace so row work remains beside its source. The
     target sheet is the full-viewport overlay. */
  position: absolute;
  inset: 0;
  background: color-mix(in oklab, var(--lt-fg) 24%, transparent);
  z-index: 50;
}
.lt-drawer {
  position: absolute;
  top: var(--overlay-anchor-top, 0);
  right: 0;
  width: min(440px, 92vw);
  /* A fixed ceiling keeps a row inspection task bounded. The drawer body is
     its one scroll surface when the rendered document is longer. */
  max-height: 640px;
  overflow: hidden;
  background: var(--lt-surface);
  border: 1px solid var(--lt-border-strong);
  border-right: 0;
  border-radius: var(--lt-radius) 0 0 var(--lt-radius);
  box-shadow: var(--lt-shadow-overlay);
  display: flex;
  flex-direction: column;
  animation: lt-drawer-in var(--lt-dur) var(--lt-ease);
}
.lt-drawer:focus-visible { outline: none; box-shadow: var(--lt-shadow-overlay), var(--lt-focus-ring-tight); }
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
.lt-drawer-body { padding: var(--lt-space-4); overflow-y: auto; overscroll-behavior: contain; flex: 1; }
</style>
