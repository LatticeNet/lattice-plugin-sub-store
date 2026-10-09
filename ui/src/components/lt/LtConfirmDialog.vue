<script setup lang="ts">
import { computed, nextTick, ref, toRef, watch } from "vue";
import LtButton from "./LtButton.vue";
import { t } from "../../i18n";

import { trapDialogTab } from "../../dialogFocus";
import { useOverlayRegistration } from "../../useOverlayRegistration";

/**
 * Two-step destructive confirmation. The dialog restates every affected
 * resource by name; when more than one is affected the operator must type the
 * count to arm the confirm button, reading the list is the point. When one
 * record's delete breaks something outside Lattice (a live share), the
 * operator types its name instead (design 23, 3.8).
 */
const props = withDefaults(
  defineProps<{
    open: boolean;
    title: string;
    verb: string;
    /** What the verb will be applied to. This is what the operator types the count of. */
    names: string[];
    /**
     * What else breaks as a consequence, if anything. Kept apart from `names`
     * deliberately: these are not being deleted, and folding them in inflated
     * the arming count so a one-record delete asked the operator to type the
     * number of records it would damage. Listed, not counted.
     */
    consequences?: string[];
    /** What changes outside Lattice: live shares, by path. */
    served?: string[];
    /** Typed to arm the confirm instead of the count; "" when the count rule applies. */
    confirmText?: string;
    busy?: boolean;
    /**
     * The record whose row takes focus back when nothing had focus as the
     * dialog opened: a delete chosen in the command palette, which closed
     * before the dialog appeared.
     */
    focusRecord?: string;
  }>(),
  { consequences: () => [], served: () => [], confirmText: "", focusRecord: "" },
);
const emit = defineEmits<{ (e: "confirm"): void; (e: "cancel"): void }>();

// A modal opens over a panel and Escape closes exactly this one; the stack
// says so, so nothing here has to stop the key from travelling.
useOverlayRegistration(toRef(props, "open"), () => emit("cancel"));

const typed = ref("");
const dialog = ref<HTMLElement | null>(null);
/*
 * What had focus when the dialog took it: a row menu's trigger (the menu has
 * already handed focus back by then), a panel's Delete, the batch bar. Escape
 * and Cancel give it back, so the keyboard is never dropped on <body>. After a
 * confirmed delete that control has usually gone with its row, and the screen
 * moves focus to the next row instead; nothing here competes with that,
 * because focus is only restored while nothing else holds it.
 */
let opener: HTMLElement | null = null;
watch(
  () => props.open,
  async (open, was) => {
    typed.value = "";
    if (!open) {
      if (!was) return;
      const target = opener;
      opener = null;
      await nextTick();
      const active = document.activeElement;
      const free = !active || active === document.body;
      if (free && target?.isConnected) target.focus();
      return;
    }
    // Escape only reaches a handler on a focused element, and a destructive
    // dialog the operator cannot dismiss with Escape is the worst one to get
    // wrong.
    await nextTick();
    const active = document.activeElement;
    opener = active instanceof HTMLElement && active !== document.body && !dialog.value?.contains(active) ? active : null;
    if (!opener && props.focusRecord) opener = document.querySelector<HTMLElement>(`[data-record-open="${CSS.escape(props.focusRecord)}"]`);
    dialog.value?.focus();
  },
);

/*
 * The dialog is aria-modal, so Tab stays inside it: past the last control it
 * went to <body>, then to the page behind the scrim, and in the console out
 * of the frame altogether.
 */
function onKeydown(event: KeyboardEvent): void {
  if (dialog.value) trapDialogTab(event, dialog.value);
}
const needsTyping = computed(() => props.names.length > 1 || !!props.confirmText);
const armed = computed(() => {
  if (props.confirmText) return typed.value.trim() === props.confirmText;
  return !needsTyping.value || typed.value.trim() === String(props.names.length);
});
</script>

<template>
  <!--
    No Escape handler here on purpose. One press used to cancel this dialog AND
    then reach the screen, which saw a dirty draft and raised the same dialog
    again, so every overlay grew a `.stop` and the screens grew hand-written
    lists of what counted as open. The overlay stack replaced both: this dialog
    registers while it is open and the screen's one document handler closes the
    top of the stack.
  -->
  <div v-if="open" class="lt-dialog-backdrop" @click.self="emit('cancel')">
    <div
      ref="dialog"
      class="lt-dialog"
      role="alertdialog"
      aria-modal="true"
      tabindex="-1"
      :aria-label="title"
      @keydown="onKeydown"
    >
      <p class="lt-dialog-title">{{ title }}</p>
      <ul class="lt-dialog-names">
        <li v-for="name in names" :key="name" class="mono">{{ name }}</li>
      </ul>
      <template v-if="consequences.length">
        <p class="lt-dialog-subtitle">
          {{ consequences.length === 1 ? t.confirm.alsoBreaksOne : t.confirm.alsoBreaks(consequences.length) }}
        </p>
        <ul class="lt-dialog-names is-consequence">
          <li v-for="note in consequences" :key="note" class="mono">{{ note }}</li>
        </ul>
      </template>
      <template v-if="served.length">
        <p class="lt-dialog-subtitle">{{ served.length === 1 ? t.confirm.servedOne : t.confirm.servedMany }}</p>
        <ul class="lt-dialog-names is-consequence">
          <li v-for="line in served" :key="line" class="mono">{{ line }}</li>
        </ul>
      </template>
      <label v-if="confirmText" class="lt-dialog-arm">
        {{ t.confirm.typeName(confirmText) }}
        <input v-model="typed" class="lt-dialog-input is-name" autocomplete="off" spellcheck="false" />
      </label>
      <label v-else-if="needsTyping" class="lt-dialog-arm">
        {{ t.confirm.typeCount(names.length) }}
        <input v-model="typed" class="lt-dialog-input" inputmode="numeric" autocomplete="off" />
      </label>
      <div class="lt-dialog-actions">
        <LtButton variant="ghost" @click="emit('cancel')">{{ t.common.cancel }}</LtButton>
        <LtButton variant="danger" :disabled="!armed || busy" @click="emit('confirm')">
          {{ busy ? t.confirm.busy(verb) : verb }}
        </LtButton>
      </div>
    </div>
  </div>
</template>

<style scoped>
.lt-dialog-backdrop {
  /* Fixed to the frame, which is the visible window. It was absolute against
     the document and pushed down by the click's Y, because a content-sized
     frame had no viewport to centre in; it has one now. */
  position: fixed;
  inset: 0;
  /* Darkens, in both schemes. The foreground mix lightened the page on the
     dark theme, so the dialog rendered darker than what it covered. */
  background: var(--lt-scrim-strong);
  display: flex;
  align-items: flex-start;
  justify-content: center;
  padding: 12vh var(--space-4) var(--space-4);
  overflow-y: auto;
  z-index: var(--lt-z-modal);
}
.lt-dialog {
  width: min(440px, calc(100vw - 32px));
  outline: none;
  background: var(--card);
  border: 1px solid var(--border);
  /* A modal takes the host dialog's step, which is one below a page panel. */
  border-radius: var(--radius-lg);
  padding: var(--space-4);
  box-shadow: var(--shadow-overlay);
}
.lt-dialog-title { margin: 0 0 var(--space-3); font-size: var(--text-body); font-weight: 600; }
.lt-dialog-names {
  margin: 0 0 var(--space-3);
  padding: var(--space-2) var(--space-3);
  list-style: none;
  max-height: 160px;
  overflow: auto;
  border: 0;
  border-radius: var(--radius-lg);
  background: var(--muted);
  font-size: var(--lt-text-sm);
}
.lt-dialog-names .mono { font-family: var(--font-mono); }
/* Consequences are a warning, not a kill list, and must not read as things
   about to be deleted. */
.lt-dialog-names.is-consequence {
  background: var(--lt-warn-soft);
}
.lt-dialog-subtitle {
  margin: 0 0 var(--space-1);
  color: var(--muted-foreground);
  font-size: var(--lt-text-xs);
  font-weight: 600;
}
.lt-dialog-arm { display: block; font-size: var(--lt-text-sm); color: var(--muted-foreground); margin-bottom: var(--space-3); }
.lt-dialog-input {
  display: block;
  margin-top: var(--space-1);
  width: 90px;
  font: inherit;
  padding: var(--space-1) var(--space-2);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  background: var(--background);
  color: var(--foreground);
}
/* A name is typed whole, so its field is as wide as the dialog; a count fits in 90px. */
.lt-dialog-input.is-name { width: 100%; font-family: var(--font-mono); }
.lt-dialog-input:focus-visible { outline: none; box-shadow: var(--lt-focus-ring); }
.lt-dialog-actions { display: flex; justify-content: flex-end; gap: var(--space-2); }
</style>
