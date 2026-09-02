<script setup lang="ts">
import { computed, ref } from "vue";

import { useReveal } from "../reveal";
import { maskUrl } from "../urlMask";

/**
 * A text field for a provider link. The link carries the provider's token,
 * so the field shows the full value only while it is being edited: on blur
 * it prints `https://host/…?…`, and Reveal shows the whole thing for a
 * minute. The record always holds the full value; only the display masks.
 */
const props = defineProps<{
  modelValue: string;
  placeholder?: string;
  /** For the input's own label; the caller wraps it in a <label>. */
  ariaLabel?: string;
}>();
const emit = defineEmits<{ (e: "update:modelValue", value: string): void }>();

const focused = ref(false);
const reveal = useReveal();
const showing = computed(() => focused.value || reveal.on.value);
const shown = computed(() => (showing.value ? props.modelValue : maskUrl(props.modelValue)));

function onInput(event: Event): void {
  emit("update:modelValue", (event.target as HTMLInputElement).value);
}
function onFocus(): void {
  focused.value = true;
}
function onBlur(): void {
  focused.value = false;
}
</script>

<template>
  <span class="masked-url">
    <input
      type="text"
      autocomplete="off"
      spellcheck="false"
      :value="shown"
      :placeholder="placeholder"
      :aria-label="ariaLabel"
      :title="showing ? undefined : 'Masked after the host. Click to edit, or Reveal to read it for a minute.'"
      @focus="onFocus"
      @blur="onBlur"
      @input="onInput"
    />
    <button
      v-if="modelValue"
      type="button"
      class="masked-url-reveal"
      :aria-pressed="reveal.on.value"
      :title="reveal.on.value ? 'Masks itself again after a minute' : 'Show the whole link for a minute'"
      @click="reveal.toggle()"
    >
      {{ reveal.on.value ? "Hide" : "Reveal" }}
    </button>
  </span>
</template>
