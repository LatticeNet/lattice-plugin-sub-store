<script setup lang="ts">
import { nextTick, ref, toRef, watch } from "vue";
import { X } from "@lucide/vue";

import { trapDialogTab } from "../../dialogFocus";
import { useOverlayRegistration } from "../../useOverlayRegistration";

/**
 * The right-side panel: row-scoped work that is a form or a view, not a
 * question. Preview, upload to a target, publish a share, renew one.
 *
 * It used to be the drawer, positioned beside the row that opened it in
 * document coordinates. The frame is a viewport now (PluginFrameHost), so the
 * panel is fixed to it and the row it belongs to says so by staying lit under
 * the scrim. Geometry answered "which record is this" only until the page
 * scrolled.
 *
 * Escape is not handled here. The panel registers a close with the overlay
 * stack and the visible screen's one document handler closes the top of it,
 * which is what stopped one press from dismissing two things (or nothing).
 *
 * Focus goes to the panel on open, because Escape only reaches a handler on a
 * focused element, and returns to whatever opened it on close: the component
 * is told what that is rather than measuring it from an event, which is the
 * last thing the anchoring model was doing.
 */
const props = withDefaults(
  defineProps<{
    open: boolean;
    title: string;
    /** "record" is a 440px form; "output" is the 960px document panel. */
    size?: "record" | "output";
    /** Focused again when the panel closes. */
    returnFocusTo?: HTMLElement | null;
  }>(),
  { size: "record", returnFocusTo: null },
);
const emit = defineEmits<{ (e: "close"): void }>();

const panel = ref<HTMLElement | null>(null);

useOverlayRegistration(toRef(props, "open"), () => emit("close"));

watch(
  () => props.open,
  async (open, was) => {
    if (open) {
      await nextTick();
      panel.value?.focus();
      return;
    }
    if (was) props.returnFocusTo?.focus();
  },
);

function onTab(event: KeyboardEvent): void {
  if (panel.value) trapDialogTab(event, panel.value);
}
</script>

<template>
  <div v-if="open" class="sheet-scrim" role="presentation" @click.self="emit('close')">
    <aside
      ref="panel"
      class="sheet"
      :data-size="size"
      role="dialog"
      aria-modal="true"
      tabindex="-1"
      :aria-label="title"
      @keydown.tab="onTab"
    >
      <header class="sheet-head">
        <div class="sheet-headings">
          <h3 class="sheet-title">{{ title }}</h3>
          <p v-if="$slots.subtitle" class="sheet-sub"><slot name="subtitle" /></p>
        </div>
        <button class="sheet-close" type="button" aria-label="Close" @click="emit('close')">
          <X :size="16" aria-hidden="true" />
        </button>
      </header>
      <div class="sheet-body"><slot /></div>
      <footer v-if="$slots.foot" class="sheet-foot"><slot name="foot" /></footer>
    </aside>
  </div>
</template>
