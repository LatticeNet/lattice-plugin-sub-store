<script setup lang="ts">
/**
 * CodeEditor, line numbers, highlighting and real editing keys for every
 * content surface that previously made do with a bare <textarea>: file
 * configurations, generator scripts, script operators, pasted node lists.
 *
 * CodeMirror arrives as a lazy chunk on first mount (see codemirror.ts). Two
 * honesty rules govern this component:
 *  - until the chunk lands, and forever if it fails to load, the same old
 *    textarea is shown. An editor upgrade must never be able to take the
 *    ability to edit away;
 *  - v-model semantics are exact: external writes replace the document,
 *    internal edits emit, and neither echoes back into a loop.
 */
import { onBeforeUnmount, onMounted, ref, watch } from "vue";

import type { EditorHandle, EditorLanguage } from "../codemirror";

const props = withDefaults(
  defineProps<{
    modelValue: string;
    language?: EditorLanguage;
    rows?: number;
    placeholder?: string;
    readonly?: boolean;
    /** Read-only rendered output. It grows with its containing scroll surface. */
    preview?: boolean;
    /**
     * The id of the element naming this editor. CodeMirror's contenteditable
     * and the textarea fallback both need it explicitly: a `<span class=
     * "field-label">` sitting above them is not an accessible name, and these
     * are exactly the controls that lost their `<label>` wrapper when they
     * stopped being form elements.
     */
    ariaLabelledby?: string;
  }>(),
  {
    language: "plain",
    rows: 12,
    placeholder: "",
    readonly: false,
    preview: false,
    ariaLabelledby: undefined,
  },
);

const emit = defineEmits<{ (e: "update:modelValue", value: string): void }>();

const hostEl = ref<HTMLElement | null>(null);
const ready = ref(false);
const failed = ref(false);
let handle: EditorHandle | null = null;

function onTextareaInput(event: Event): void {
  emit("update:modelValue", (event.target as HTMLTextAreaElement).value);
}

onMounted(async () => {
  try {
    const cm = await import("../codemirror");
    if (!hostEl.value) return;
    handle = cm.createEditor({
      parent: hostEl.value,
      ariaLabelledby: props.ariaLabelledby,
      value: props.modelValue,
      language: props.language,
      readonly: props.readonly,
      placeholderText: props.placeholder,
      onChange: (value) => {
        if (value !== props.modelValue) emit("update:modelValue", value);
      },
    });
    ready.value = true;
  } catch {
    // The visible textarea is the durable fallback. A failed enhancement must
    // not take an editable document or a read-only render away.
    failed.value = true;
  }
});

watch(
  () => props.modelValue,
  (value) => handle?.setValue(value),
);
watch(
  () => props.language,
  (language) => handle?.setLanguage(language),
);

onBeforeUnmount(() => {
  handle?.destroy();
  handle = null;
});
</script>

<template>
  <div
    class="code-editor"
    :class="{ 'code-editor-preview': preview }"
    :style="{ '--code-editor-rows': String(rows) }"
  >
    <textarea
      v-if="!ready"
      class="code-area"
      :rows="rows"
      spellcheck="false"
      :placeholder="placeholder"
      :readonly="readonly"
      :aria-labelledby="ariaLabelledby"
      :value="modelValue"
      @input="onTextareaInput"
    ></textarea>
    <p v-if="failed" class="code-editor-fallback-note" role="status">
      Syntax highlighting is unavailable. Plain-text {{ preview ? "view" : "editor" }} shown.
    </p>
    <div v-show="ready" ref="hostEl" class="code-editor-host"></div>
  </div>
</template>
