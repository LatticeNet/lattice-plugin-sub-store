<script setup lang="ts">
import { computed, nextTick, ref, watch } from "vue";
import { CornerDownLeft, Search } from "@lucide/vue";

import type { SubscriptionListItem } from "../client";
import {
  moveSelection,
  paletteActionsFor,
  paletteEntries,
  type PaletteEntry,
} from "../commandPalette";
import { trapDialogTab } from "../dialogFocus";
import type { ActionCapabilities, ActionId } from "../recordActions";

const props = defineProps<{
  open: boolean;
  records: SubscriptionListItem[];
  caps: ActionCapabilities;
}>();

const emit = defineEmits<{
  close: [];
  /** Go to the record's screen and run the action there. */
  run: [record: SubscriptionListItem, action: ActionId];
  command: [id: NonNullable<PaletteEntry["command"]>];
}>();

/** Which actions the palette offers. Editing and deleting are what an operator
 *  comes here for; the drawer-bound ones stay on the row, where the drawer can
 *  anchor to something. */
const OFFERED: readonly ActionId[] = ["edit", "output", "refresh", "duplicate", "delete"];

const query = ref("");
const cursor = ref(0);
/** Level two: the record whose actions are showing. */
const chosen = ref<SubscriptionListItem | null>(null);
const input = ref<HTMLInputElement | null>(null);

const entries = computed(() => paletteEntries(query.value, props.records, props.caps));
const actions = computed(() =>
  chosen.value ? paletteActionsFor(chosen.value, props.caps, OFFERED) : [],
);
const rows = computed<{ key: string; label: string; hint: string; disabled: boolean; reason: string; danger: boolean }[]>(
  () =>
    chosen.value
      ? actions.value.map((a) => ({ key: a.id, label: a.label, hint: a.hint, disabled: a.disabled, reason: a.reason, danger: a.danger }))
      : entries.value.map((e) => ({ key: e.key, label: e.label, hint: e.hint, disabled: e.disabled, reason: e.reason, danger: false })),
);

watch(
  () => props.open,
  async (open) => {
    if (!open) return;
    query.value = "";
    cursor.value = 0;
    chosen.value = null;
    await nextTick();
    input.value?.focus();
  },
);

watch(rows, () => {
  if (cursor.value >= rows.value.length) cursor.value = 0;
});

function choose(index: number): void {
  const row = rows.value[index];
  if (!row || row.disabled) return;
  if (chosen.value) {
    emit("run", chosen.value, row.key as ActionId);
    emit("close");
    return;
  }
  const entry = entries.value[index];
  if (!entry) return;
  if (entry.kind === "command" && entry.command) {
    emit("command", entry.command);
    emit("close");
    return;
  }
  if (entry.record) {
    // Level two, rather than acting: a record has several things you might
    // have come for, and guessing wrong here is a destructive guess.
    chosen.value = entry.record;
    cursor.value = 0;
  }
}

const dialog = ref<HTMLElement | null>(null);

/**
 * The dialog owns these keys, not the input.
 *
 * They were bound to the input alone, which works only while the input has
 * focus — and an `aria-modal` overlay that stops answering Escape the moment a
 * click lands somewhere else is an overlay you can get stuck inside. Tab is
 * kept in with the same helper the drawer and the client sheet use.
 */
function onKeydown(event: KeyboardEvent): void {
  if (event.key === "Tab" && dialog.value) {
    trapDialogTab(event, dialog.value);
    return;
  }
  if (event.key === "ArrowDown" || event.key === "ArrowUp") {
    event.preventDefault();
    cursor.value = moveSelection(cursor.value, event.key === "ArrowDown" ? 1 : -1, rows.value.length);
    return;
  }
  if (event.key === "Enter") {
    event.preventDefault();
    choose(cursor.value);
    return;
  }
  if (event.key === "Escape") {
    event.preventDefault();
    // Escape steps back one level before it closes, so a mis-pick costs one
    // key rather than the whole query.
    if (chosen.value) {
      chosen.value = null;
      cursor.value = 0;
      return;
    }
    emit("close");
  }
}
</script>

<template>
  <div v-if="open" class="palette-scrim" @click="emit('close')">
    <div
      ref="dialog"
      class="palette"
      role="dialog"
      aria-modal="true"
      aria-label="Search records and actions"
      tabindex="-1"
      @click.stop
      @keydown="onKeydown"
    >
      <div class="palette-input">
        <Search :size="15" aria-hidden="true" />
        <input
          ref="input"
          v-model="query"
          type="text"
          role="combobox"
          aria-expanded="true"
          aria-controls="palette-list"
          :aria-activedescendant="rows.length ? `palette-row-${cursor}` : undefined"
          :placeholder="chosen ? `What to do with ${chosen.display_name || chosen.name}` : 'Search records, or type a command'"
          autocomplete="off"
          spellcheck="false"
        />
        <kbd class="palette-hint">esc</kbd>
      </div>

      <ul id="palette-list" class="palette-list" role="listbox">
        <li
          v-for="(row, index) in rows"
          :id="`palette-row-${index}`"
          :key="row.key"
          role="option"
          :aria-selected="index === cursor"
          :aria-disabled="row.disabled"
          class="palette-row"
          :data-active="index === cursor"
          :data-danger="row.danger"
          :data-disabled="row.disabled"
          @mouseenter="cursor = index"
          @click="choose(index)"
        >
          <span class="palette-label">{{ row.label }}</span>
          <span class="palette-meta">{{ row.disabled ? row.reason : row.hint }}</span>
          <CornerDownLeft v-if="index === cursor && !row.disabled" :size="13" aria-hidden="true" />
        </li>
        <li v-if="!rows.length" class="palette-empty">
          Nothing matches “{{ query }}”.
        </li>
      </ul>
    </div>
  </div>
</template>
