<script setup lang="ts">
import { computed } from "vue";

import { formatPercent, t } from "../i18n";
import { formatBytes } from "../rowStatus";
import { USAGE_WARN_RATIO, usageRatio, usedBytes, type ProviderFigures } from "../pipeline";

/**
 * A provider's traffic as a bar: used over total, the percentage beside it in
 * mono. The fill is neutral ink until the attention threshold, warning past
 * it, danger when spent, so the colour only ever says something the number
 * already says. Without a total there is nothing to measure against, and the
 * bar gives way to the used figure alone.
 */
const props = defineProps<{ figures: ProviderFigures }>();

const ratio = computed(() => usageRatio(props.figures));
const tone = computed(() => {
  const value = ratio.value;
  if (value === null) return "neutral";
  if (value >= 1) return "danger";
  if (value > USAGE_WARN_RATIO) return "warning";
  return "neutral";
});
const width = computed(() => Math.min(100, Math.max(0, (ratio.value ?? 0) * 100)));
const label = computed(() => {
  const value = ratio.value;
  if (value === null) return t.usage.noTotal(formatBytes(usedBytes(props.figures)));
  return t.usage.label(formatBytes(usedBytes(props.figures)), formatBytes(props.figures.total ?? 0), formatPercent(Math.round(value * 100) / 100));
});
const figure = computed(() =>
  ratio.value !== null ? formatPercent(Math.round(ratio.value * 100) / 100) : t.usage.usedOnly(formatBytes(usedBytes(props.figures))),
);
</script>

<template>
  <span class="usage" :data-tone="tone" :title="label">
    <svg v-if="ratio !== null" class="usage-bar" role="img" :aria-label="label" viewBox="0 0 100 6" preserveAspectRatio="none">
      <rect class="usage-track" x="0" y="0" width="100" height="6" rx="3" />
      <rect class="usage-fill" x="0" y="0" :width="width" height="6" rx="3" />
    </svg>
    <span class="usage-figure">{{ figure }}</span>
  </span>
</template>
