<script setup lang="ts">
import { PcButton } from "@latticenet/plugin-bridge/chassis";

import { formatCount, t } from "../i18n";

/**
 * The card footer, "Records 1 to 50 of 160", Previous, Page 1 of 4, Next, in
 * the active locale.
 *
 * The chassis's PcPagination draws the same footer with its range, its page
 * line and both buttons in English and takes no label for them, and the
 * chassis lives in the bridge package, which this plugin cannot change. So
 * the footer is drawn here with the chassis's own classes and buttons, and
 * looks and behaves as PcPagination does.
 */
const props = defineProps<{
  page: number;
  pages: number;
  from: number;
  to: number;
  total: number;
  noun: string;
  label: string;
}>();

const emit = defineEmits<{ (event: "update:page", page: number): void }>();
</script>

<template>
  <footer class="pc-pagination" :aria-label="props.label">
    <span class="pc-pagination-range">{{ t.pager.range(props.noun, formatCount(props.from), formatCount(props.to), formatCount(props.total)) }}</span>
    <PcButton compact data-testid="page-previous" :disabled="props.page <= 1" @click="emit('update:page', props.page - 1)">{{ t.pager.previous }}</PcButton>
    <span class="pc-pagination-page">{{ t.pager.page(formatCount(props.page), formatCount(props.pages)) }}</span>
    <PcButton compact data-testid="page-next" :disabled="props.page >= props.pages" @click="emit('update:page', props.page + 1)">{{ t.pager.next }}</PcButton>
  </footer>
</template>
