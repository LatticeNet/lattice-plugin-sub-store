<script setup lang="ts">
/**
 * The honest failure path for a copy.
 *
 * When the clipboard genuinely cannot be written, the operator still came here
 * to get a value out, and telling them it failed does not give them the value.
 * So the value goes on screen in a real form control, pre-selected, with the
 * keystroke named. Selecting it by hand out of a `<code>` block is possible but
 * fiddly for a long URL and hopeless for a document; a focused input where the
 * whole value is already highlighted turns the recovery into one keypress.
 *
 * `readonly` rather than `disabled`: a disabled control cannot be focused or
 * selected, which would defeat the entire point.
 */
import { nextTick, onMounted, ref } from "vue";

const props = withDefaults(
  defineProps<{
    value: string;
    /** What the value is, for the label and the instruction. */
    subject?: string;
    /** A document is many lines and needs a textarea; a link needs one line. */
    multiline?: boolean;
  }>(),
  { subject: "value", multiline: false },
);

const field = ref<HTMLInputElement | HTMLTextAreaElement | null>(null);

/** The shortcut to name. Naming the wrong one is worse than naming none. */
const shortcut = navigator.platform.toLowerCase().includes("mac") ? "⌘C" : "Ctrl+C";

async function selectAll(): Promise<void> {
  await nextTick();
  const element = field.value;
  if (!element) return;
  element.focus({ preventScroll: false });
  element.select();
  // Selecting to the end leaves the field scrolled to the end, so a long
  // subscription URL showed its token and not which link it was. The selection
  // is what matters for the copy; the start is what matters for reading it.
  element.scrollLeft = 0;
  element.scrollTop = 0;
}

onMounted(selectAll);
defineExpose({ selectAll });
</script>

<template>
  <div class="lt-manual-copy" role="group" :aria-label="`Copy the ${props.subject} manually`">
    <p class="lt-manual-copy__note">
      The console could not reach the clipboard. The {{ props.subject }} is selected below, press
      <kbd>{{ shortcut }}</kbd> to copy it.
    </p>
    <textarea
      v-if="props.multiline"
      ref="field"
      class="lt-manual-copy__field is-multiline"
      readonly
      spellcheck="false"
      :aria-label="props.subject"
      :value="props.value"
      @focus="($event.target as HTMLTextAreaElement).select()"
    />
    <input
      v-else
      ref="field"
      class="lt-manual-copy__field"
      type="text"
      readonly
      spellcheck="false"
      :aria-label="props.subject"
      :value="props.value"
      @focus="($event.target as HTMLInputElement).select()"
    />
  </div>
</template>

<style scoped>
.lt-manual-copy {
  display: flex;
  flex-direction: column;
  gap: var(--lt-space-1);
  margin-top: var(--lt-space-2);
  padding: var(--lt-space-2);
  border: 1px solid var(--lt-warn-border);
  border-left-width: 3px;
  border-radius: var(--lt-radius);
  background: var(--lt-warn-soft);
}
.lt-manual-copy__note {
  margin: 0;
  color: var(--lt-fg);
  font-size: var(--lt-text-xs);
  line-height: var(--lt-leading);
}
.lt-manual-copy__note kbd {
  padding: 0 4px;
  border: 1px solid var(--lt-border-strong);
  border-radius: var(--lt-radius-sm);
  background: var(--lt-surface);
  font-family: var(--lt-mono);
  font-size: 11px;
}
.lt-manual-copy__field {
  width: 100%;
  padding: var(--lt-space-1) var(--lt-space-2);
  border: 1px solid var(--lt-border);
  border-radius: var(--lt-radius-sm);
  background: var(--lt-surface);
  color: var(--lt-fg);
  font-family: var(--lt-mono);
  font-size: var(--lt-text-xs);
  line-height: var(--lt-leading);
}
.lt-manual-copy__field.is-multiline {
  min-height: 96px;
  max-height: 40vh;
  resize: vertical;
  white-space: pre;
  overflow-wrap: normal;
  overflow: auto;
}
.lt-manual-copy__field:focus-visible {
  outline: none;
  box-shadow: var(--lt-focus-ring-tight);
}
</style>
