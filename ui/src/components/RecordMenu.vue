<script setup lang="ts">
import { CopyPlus, Ellipsis, Eye, Link, Share2, Trash2, Upload } from "@lucide/vue";

import LtIconButton from "./lt/LtIconButton.vue";
import type { ResolvedAction } from "../recordActions";

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
const ICONS = { eye: Eye, share: Share2, link: Link, upload: Upload, copy: CopyPlus, trash: Trash2 } as const;

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
</script>

<template>
  <div class="rec-menu-wrap">
    <LtIconButton
      :label="`More actions for ${name}`"
      :aria-haspopup="true"
      :aria-expanded="open"
      @click="emit('toggle')"
    >
      <Ellipsis :size="15" aria-hidden="true" />
    </LtIconButton>
    <div v-if="open" class="rec-menu" role="menu" @keydown="emit('keydown', $event)">
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
  </div>
</template>
