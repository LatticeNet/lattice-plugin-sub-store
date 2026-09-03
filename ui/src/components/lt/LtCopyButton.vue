<script setup lang="ts">
/**
 * The one copy control in this plugin.
 *
 * Every surface that offers a value to copy (a share link, a subscription URL,
 * a rendered document, a backup envelope) goes through this, so the failure
 * path is written once and is the same everywhere. That matters more than the
 * saved lines: the failure path is the part that was wrong on every one of them
 * before, each in its own way.
 *
 * The states are copy → copied, or copy → the value on screen and selected.
 * There is no state where the operator is told it failed and left holding
 * nothing.
 */
import { ref } from "vue";
import { Check, Copy, LoaderCircle } from "@lucide/vue";

import { copyText } from "../../hostClipboard";
import LtButton from "./LtButton.vue";
import LtManualCopy from "./LtManualCopy.vue";

const props = withDefaults(
  defineProps<{
    value: string;
    label?: string;
    copiedLabel?: string;
    /** What the value is, named in the fallback ("link", "document", "backup"). */
    subject?: string;
    variant?: "primary" | "ghost" | "danger";
    size?: "md" | "sm";
    disabled?: boolean;
    multiline?: boolean;
    /** Render the reveal somewhere else (a sheet footer, a wider column). */
    revealInline?: boolean;
  }>(),
  {
    label: "Copy",
    copiedLabel: "Copied",
    subject: "value",
    variant: "ghost",
    size: "md",
    multiline: false,
    revealInline: true,
  },
);

const emit = defineEmits<{ (event: "failed", value: string): void; (event: "copied"): void }>();

const busy = ref(false);
const copied = ref(false);
const revealed = ref(false);
let flashTimer: ReturnType<typeof setTimeout> | undefined;

async function run(): Promise<void> {
  if (busy.value || props.disabled || !props.value) return;
  busy.value = true;
  revealed.value = false;
  try {
    const ok = await copyText(props.value);
    if (ok) {
      copied.value = true;
      emit("copied");
      if (flashTimer !== undefined) clearTimeout(flashTimer);
      flashTimer = setTimeout(() => { copied.value = false; }, 1600);
    } else {
      // Not an error state. The operator still gets the value, just by hand.
      revealed.value = true;
      emit("failed", props.value);
    }
  } finally {
    busy.value = false;
  }
}

defineExpose({ run });
</script>

<template>
  <div class="lt-copy" :class="{ 'is-revealed': revealed && props.revealInline }">
    <LtButton
      :variant="props.variant"
      :size="props.size"
      :disabled="props.disabled || busy || !props.value"
      @click="run()"
    >
      <LoaderCircle v-if="busy" :size="14" class="spin" aria-hidden="true" />
      <Check v-else-if="copied" :size="14" aria-hidden="true" />
      <Copy v-else :size="14" aria-hidden="true" />
      <slot>{{ copied ? props.copiedLabel : props.label }}</slot>
    </LtButton>
    <LtManualCopy
      v-if="revealed && props.revealInline"
      :value="props.value"
      :subject="props.subject"
      :multiline="props.multiline"
    />
  </div>
</template>

<style scoped>
/* A column so the reveal stacks under its own button rather than becoming a
 * sibling item in whatever row the button sits in. `align-items: flex-start`
 * keeps the button its natural width while the reveal, which needs room for a
 * URL, is free to fill the column. */
.lt-copy {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  min-width: 0;
}
.lt-copy.is-revealed {
  align-self: stretch;
  width: 100%;
}
</style>
