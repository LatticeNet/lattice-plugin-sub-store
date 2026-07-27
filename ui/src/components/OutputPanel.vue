<script setup lang="ts">
import { ref } from "vue";
import { CheckCircle2, ClipboardCopy } from "@lucide/vue";

/**
 * Copy-first output panel for converted content. Sandboxed frames cannot
 * offer downloads and usually lack clipboard permission, so the fallback is
 * select-all for a manual copy.
 */
const props = defineProps<{
  content: string;
  fileName: string;
  note?: string;
}>();

const outputArea = ref<HTMLTextAreaElement>();
const copyNote = ref("");

async function copyOutput(): Promise<void> {
  if (!props.content) return;
  copyNote.value = "";
  try {
    await navigator.clipboard.writeText(props.content);
    copyNote.value = "Copied";
  } catch {
    selectOutput();
    copyNote.value = "Clipboard blocked by the sandbox — text selected, copy it manually";
  }
}

function selectOutput(): void {
  outputArea.value?.focus();
  outputArea.value?.select();
}
</script>

<template>
  <div class="output-panel">
    <div class="section-heading">
      <div>
        <h2>Output</h2>
        <p><span class="mono">{{ fileName }}</span><span v-if="note"> · {{ note }}</span></p>
      </div>
      <div class="heading-actions">
        <span v-if="copyNote" class="vault-note" aria-live="polite">
          <CheckCircle2 v-if="copyNote === 'Copied'" :size="12" aria-hidden="true" /> {{ copyNote }}
        </span>
        <button class="button button-secondary button-compact" type="button" @click="copyOutput">
          <ClipboardCopy :size="13" aria-hidden="true" />
          Copy
        </button>
      </div>
    </div>
    <textarea
      ref="outputArea"
      class="output-area mono"
      :value="content"
      readonly
      rows="14"
      spellcheck="false"
      aria-label="Converted configuration"
      @focus="selectOutput"
    />
    <p class="vault-note">
      The sandboxed frame cannot offer downloads — copy the text and save it as
      <span class="mono">{{ fileName }}</span> where your client expects it.
    </p>
  </div>
</template>
