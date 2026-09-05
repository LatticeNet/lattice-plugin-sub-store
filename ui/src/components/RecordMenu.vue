<script setup lang="ts">
import { CopyPlus, Ellipsis, Eye, Link, RefreshCw, Share2, Trash2, Upload } from "@lucide/vue";
import { PcIconButton } from "@latticenet/plugin-bridge/chassis";
import { onBeforeUnmount, ref, watch } from "vue";

import type { ResolvedAction } from "../recordActions";

/**
 * The trigger stays in the actions cell; the menu itself is rendered at the
 * end of <body>. The chassis pins every actions cell (sticky, with a stacking
 * level of its own), which makes each cell its own stacking context, so a menu
 * absolutely positioned inside one was painted over by the cells of every row
 * beneath it: four of seven items were on screen and unpainted on all but the
 * last row. Outside any cell, and outside the card's `overflow: clip`, the
 * menu is painted over the whole table wherever its row is.
 *
 * The consumer's attributes (`data-row-menu`) go on both halves, so its
 * outside-click test and its focus queries keep working across the teleport.
 */
defineOptions({ inheritAttrs: false });

const props = defineProps<{
  /** What this menu belongs to, for the trigger's accessible name. */
  name: string;
  actions: ResolvedAction[];
  open: boolean;
}>();

const emit = defineEmits<{
  toggle: [];
  run: [id: ResolvedAction["id"], event: MouseEvent];
  keydown: [event: KeyboardEvent];
}>();

/** Declarations name an icon; the mapping to a component lives here so the
 *  registry stays free of imports and can be tested without Vue. */
const ICONS = { eye: Eye, share: Share2, link: Link, upload: Upload, copy: CopyPlus, trash: Trash2, refresh: RefreshCw } as const;

function iconFor(name: string) {
  return ICONS[name as keyof typeof ICONS] ?? Eye;
}

/** The destructive actions are rendered apart, after a separator. */
const safe = () => props.actions.filter((action) => !action.danger);
const danger = () => props.actions.filter((action) => action.danger);

/**
 * Each distinct reason once.
 *
 * This printed the first blocked item's reason and stopped, so a menu where
 * Publish is off for a missing method and Delete is off for a missing scope
 * explained the first and silently attributed it to both.
 */
const reasons = () => [...new Set(props.actions.filter((a) => a.disabled).map((a) => a.reason))];

const anchor = ref<HTMLElement | null>(null);
/** Where the menu sits, in document coordinates: under the trigger, right
 *  edges flush. Absolute against the document rather than fixed, so a menu
 *  on the last row still counts towards the height the frame reports. */
const place = ref<{ top: string; right: string } | null>(null);

function position(): void {
  const rect = anchor.value?.getBoundingClientRect();
  if (!rect) return;
  place.value = {
    top: `${rect.bottom + window.scrollY + 4}px`,
    right: `${document.documentElement.clientWidth - rect.right - window.scrollX}px`,
  };
}

// The table wrap scrolls sideways below 720px and the document scrolls
// vertically; either moves the trigger out from under a menu placed once.
function listen(): void {
  window.addEventListener("scroll", position, true);
  window.addEventListener("resize", position);
}
function unlisten(): void {
  window.removeEventListener("scroll", position, true);
  window.removeEventListener("resize", position);
}

watch(
  () => props.open,
  (open) => {
    if (open) {
      position();
      listen();
    } else {
      unlisten();
      place.value = null;
    }
  },
  { immediate: true, flush: "post" },
);
onBeforeUnmount(unlisten);
</script>

<template>
  <div ref="anchor" class="rec-menu-wrap" v-bind="$attrs">
    <PcIconButton
      :label="`More actions for ${name}`"
      bordered
      :aria-haspopup="true"
      :aria-expanded="open"
      @click="emit('toggle')"
    >
      <Ellipsis :size="15" aria-hidden="true" />
    </PcIconButton>
  </div>
  <Teleport to="body">
    <div v-if="open" class="rec-menu" role="menu" v-bind="$attrs" :style="place ?? undefined" @keydown="emit('keydown', $event)">
      <button
        v-for="action in safe()"
        :key="action.id"
        type="button"
        role="menuitem"
        :disabled="action.disabled"
        :title="action.reason || action.title"
        @click="emit('run', action.id, $event)"
      >
        <component :is="iconFor(action.icon)" :size="14" aria-hidden="true" />
        {{ action.label }}
      </button>
      <template v-if="danger().length">
        <span class="rec-menu-sep" role="separator" />
        <button
          v-for="action in danger()"
          :key="action.id"
          type="button"
          role="menuitem"
          class="is-danger"
          :disabled="action.disabled"
          :title="action.reason || action.title"
          @click="emit('run', action.id, $event)"
        >
          <component :is="iconFor(action.icon)" :size="14" aria-hidden="true" />
          {{ action.label }}
        </button>
      </template>
      <!-- A disabled control whose reason lives only in a title is a control
           nobody on a touch device or a screen reader can find out about. -->
      <p v-for="reason in reasons()" :key="reason" class="rec-menu-note">{{ reason }}</p>
    </div>
  </Teleport>
</template>
