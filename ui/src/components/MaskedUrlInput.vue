<script setup lang="ts">
import { computed, ref } from "vue";

import { t } from "../i18n";
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
  /** A field the session may not edit; the masked value still reads. */
  disabled?: boolean;
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
      :disabled="disabled"
      :title="showing ? undefined : t.maskedUrl.maskedTitle"
      @focus="onFocus"
      @blur="onBlur"
      @input="onInput"
    />
    <button
      v-if="modelValue"
      type="button"
      class="masked-url-reveal"
      :aria-pressed="reveal.on.value"
      :title="reveal.on.value ? t.maskedUrl.revealedTitle : t.maskedUrl.revealTitle"
      @click="reveal.toggle()"
    >
      {{ reveal.on.value ? t.maskedUrl.hide : t.maskedUrl.reveal }}
    </button>
  </span>
</template>
