<script setup lang="ts">
import { computed, ref } from "vue";
import { PcButton, PcCount, PcPanel, PcStateDot } from "@latticenet/plugin-bridge/chassis";

import type { AttentionItem } from "../pipeline";

/**
 * What needs a hand, worst first. Each line is a claim, the record that
 * proves it, and the one action that clears it. The parent renders this only
 * when there is something to say: an empty attention block is a green light
 * nobody earned.
 *
 * One target per line. The claim opens its record in the side panel only
 * when the action goes somewhere else (Publish, Show them); when the action
 * is "Open" on that same record, the claim is plain text, so a line never
 * offers two controls that do one thing.
 */
const props = defineProps<{ items: AttentionItem[] }>();

function claimOpens(item: AttentionItem): boolean {
  return !!item.recordId && item.action.recordId !== item.recordId;
}

/**
 * Five lines, worst first, then the rest behind one control: the overview
 * must still show its picture on one screen when a bad day produces nine.
 */
const LIMIT = 5;
const expanded = ref(false);
const shown = computed(() => (expanded.value ? props.items : props.items.slice(0, LIMIT)));
const hidden = computed(() => props.items.length - shown.value.length);

const emit = defineEmits<{ open: [recordId: string]; act: [item: AttentionItem] }>();

const TONE = { danger: "error", warning: "warning", neutral: "neutral" } as const;

/** What the action does, by name, for its tooltip. */
function actionTitle(item: AttentionItem): string | undefined {
  const action = item.action;
  if (action.publish) return `Open the console's share form for ${item.recordName || "this file"}`;
  if (action.recordId) return `Show ${item.recordName || "the record"} in the side panel`;
  if (action.search) return `Show /${action.search} in Shares`;
  return undefined;
}
</script>

<template>
  <PcPanel class="attention" label="Attention">
    <header class="attention-head">
      <h2>Attention</h2>
      <PcCount :value="items.length" :tone="items.some((item) => item.tone === 'danger') ? 'error' : 'warning'" :label="`${items.length} things need attention`" />
    </header>
    <ul class="attention-list">
      <li v-for="item in shown" :key="item.key" class="attention-item" :data-tone="item.tone">
        <PcStateDot :tone="TONE[item.tone]" label="" :title="item.tone === 'danger' ? 'Broken now' : item.tone === 'warning' ? 'Needs a hand soon' : 'Worth a look'" />
        <button
          v-if="claimOpens(item)"
          type="button"
          class="attention-claim"
          :title="`Show ${item.recordName || 'the record'} in the side panel`"
          @click="emit('open', item.recordId!)"
        >
          {{ item.claim }}
        </button>
        <span v-else class="attention-claim">{{ item.claim }}</span>
        <PcButton compact :title="actionTitle(item)" @click="emit('act', item)">{{ item.action.label }}</PcButton>
      </li>
    </ul>
    <div v-if="hidden || expanded && items.length > LIMIT" class="attention-more">
      <PcButton compact :aria-expanded="expanded ? 'true' : 'false'" @click="expanded = !expanded">
        {{ expanded ? "Show the first five" : `Show ${hidden} more` }}
      </PcButton>
    </div>
  </PcPanel>
</template>
