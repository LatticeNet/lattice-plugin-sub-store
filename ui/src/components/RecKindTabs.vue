<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { PcCount } from "@latticenet/plugin-bridge/chassis";

/**
 * Underline kind tabs inside a resource list. One 2px primary bar moves to
 * the selected tab; the page lens tabs stay pills.
 */
const props = defineProps<{
  modelValue: string;
  label: string;
  tabs: { id: string; label: string; count: number }[];
}>();
const emit = defineEmits<{ "update:modelValue": [string] }>();

const list = ref<HTMLElement | null>(null);
const ink = ref({ x: 0, w: 0 });

function measure(): void {
  const selected = list.value?.querySelector<HTMLElement>('[role="tab"][aria-selected="true"]');
  if (!selected) {
    ink.value = { x: 0, w: 0 };
    return;
  }
  ink.value = { x: selected.offsetLeft, w: selected.offsetWidth };
}

watch(() => [props.modelValue, props.tabs], () => void nextTick(measure), { deep: true });

let ro: ResizeObserver | undefined;
onMounted(() => {
  measure();
  if (list.value && typeof ResizeObserver !== "undefined") {
    ro = new ResizeObserver(() => measure());
    ro.observe(list.value);
  }
});
onBeforeUnmount(() => ro?.disconnect());
</script>

<template>
  <div ref="list" class="rec-kind" role="tablist" :aria-label="label">
    <button
      v-for="tab in tabs"
      :key="tab.id"
      type="button"
      role="tab"
      :aria-selected="modelValue === tab.id"
      @click="emit('update:modelValue', tab.id)"
    >
      {{ tab.label }}
      <PcCount :value="tab.count" />
    </button>
    <span class="rec-kind-ink" :style="{ transform: `translateX(${ink.x}px)`, width: `${ink.w}px` }" aria-hidden="true" />
  </div>
</template>
