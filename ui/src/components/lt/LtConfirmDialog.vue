<script setup lang="ts">
import { computed, nextTick, ref, watch } from "vue";
import LtButton from "./LtButton.vue";

/**
 * Two-step destructive confirmation. The dialog restates every affected
 * resource by name; when more than one is affected the operator must type the
 * count to arm the confirm button, reading the list is the point.
 */
const props = defineProps<{
  open: boolean;
  title: string;
  verb: string;
  names: string[];
  busy?: boolean;
  /** Document-space position; the frame is not a viewport (see overlayAnchor). */
  anchorTop?: number;
}>();
const emit = defineEmits<{ (e: "confirm"): void; (e: "cancel"): void }>();

const typed = ref("");
const dialog = ref<HTMLElement | null>(null);
watch(
  () => props.open,
  async (open) => {
    typed.value = "";
    if (!open) return;
    // Escape only reaches a handler on a focused element, and a destructive
    // dialog the operator cannot dismiss with Escape is the worst one to get
    // wrong.
    await nextTick();
    dialog.value?.focus();
  },
);
const needsTyping = computed(() => props.names.length > 1);
const armed = computed(() => !needsTyping.value || typed.value.trim() === String(props.names.length));
</script>

<template>
  <div v-if="open" class="lt-dialog-backdrop" @click.self="emit('cancel')">
    <div
      ref="dialog"
      class="lt-dialog"
      role="alertdialog"
      aria-modal="true"
      tabindex="-1"
      :aria-label="title"
      :style="{ '--overlay-anchor-top': `${anchorTop ?? 0}px` }"
      @keydown.esc="emit('cancel')"
    >
      <p class="lt-dialog-title">{{ title }}</p>
      <ul class="lt-dialog-names">
        <li v-for="name in names" :key="name" class="mono">{{ name }}</li>
      </ul>
      <label v-if="needsTyping" class="lt-dialog-arm">
        To confirm, type the number of items listed above: {{ names.length }}
        <input v-model="typed" class="lt-dialog-input" inputmode="numeric" autocomplete="off" />
      </label>
      <div class="lt-dialog-actions">
        <LtButton variant="ghost" @click="emit('cancel')">Cancel</LtButton>
        <LtButton variant="danger" :disabled="!armed || busy" @click="emit('confirm')">
          {{ busy ? `${verb}…` : verb }}
        </LtButton>
      </div>
    </div>
  </div>
</template>

<style scoped>
.lt-dialog-backdrop {
  /* Absolute against .workspace, which is the document; see the note on
     .workspace in styles.css for why `inset: 0` alone is not enough. */
  position: absolute;
  inset: 0;
  background: color-mix(in oklab, var(--lt-fg) 32%, transparent);
  display: flex;
  align-items: flex-start;
  justify-content: center;
  /* Anchored to the click rather than to a fraction of a "viewport" that is
     really the frame's own content height. */
  padding-top: var(--overlay-anchor-top, 24px);
  z-index: 60;
}
.lt-dialog {
  width: min(440px, calc(100vw - 32px));
  outline: none;
  background: var(--lt-surface);
  border: 1px solid var(--lt-border);
  border-radius: var(--lt-radius);
  padding: var(--lt-space-4);
  box-shadow: 0 12px 32px color-mix(in oklab, var(--lt-fg) 18%, transparent);
}
.lt-dialog-title { margin: 0 0 var(--lt-space-3); font-size: var(--lt-text-md); font-weight: 600; }
.lt-dialog-names {
  margin: 0 0 var(--lt-space-3);
  padding: var(--lt-space-2) var(--lt-space-3);
  list-style: none;
  max-height: 160px;
  overflow: auto;
  border: 1px solid var(--lt-border);
  border-radius: var(--lt-radius-sm);
  background: var(--lt-surface-2);
  font-size: var(--lt-text-sm);
}
.lt-dialog-names .mono { font-family: var(--lt-mono); }
.lt-dialog-arm { display: block; font-size: var(--lt-text-sm); color: var(--lt-fg-muted); margin-bottom: var(--lt-space-3); }
.lt-dialog-input {
  display: block;
  margin-top: var(--lt-space-1);
  width: 90px;
  font: inherit;
  padding: var(--lt-space-1) var(--lt-space-2);
  border: 1px solid var(--lt-border);
  border-radius: var(--lt-radius-sm);
  background: var(--lt-bg);
  color: var(--lt-fg);
}
.lt-dialog-input:focus-visible { outline: none; box-shadow: var(--lt-focus-ring); }
.lt-dialog-actions { display: flex; justify-content: flex-end; gap: var(--lt-space-2); }
</style>
