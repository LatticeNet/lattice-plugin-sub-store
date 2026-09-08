<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { PcCount } from "@latticenet/plugin-bridge/chassis";

/**
 * Underline section tabs inside an editor. Quieter than the page lens pills:
 * no filled track, a 2px primary bar, optional count, and a warning dot when
 * another section holds the invalid field.
 */
const props = defineProps<{
  modelValue: string;
  label: string;
  tabs: { id: string; label: string; count?: number | null }[];
  errorTab?: string;
  errorTitle?: string;
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

watch(() => [props.modelValue, props.tabs, props.errorTab], () => void nextTick(measure), { deep: true });

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
  <div ref="list" class="ed-tabs" role="tablist" :aria-label="label">
    <button
      v-for="tab in tabs"
      :key="tab.id"
      type="button"
      class="ed-tab"
      role="tab"
      :aria-selected="modelValue === tab.id"
      @click="emit('update:modelValue', tab.id)"
    >
      {{ tab.label }}
      <PcCount v-if="tab.count" :value="tab.count" />
      <span
        v-if="errorTab === tab.id && modelValue !== tab.id"
        class="editor-tab-flag"
        :title="errorTitle"
        aria-label="This section has a problem"
      />
    </button>
    <span class="ed-tabs-ink" :style="{ transform: `translateX(${ink.x}px)`, width: `${ink.w}px` }" aria-hidden="true" />
  </div>
</template>
