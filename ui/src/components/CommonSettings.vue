<script setup lang="ts">
import type { CommonSettings, TriState } from "../commonSettings";

/**
 * The handful of operators people reach for constantly, as plain choices.
 *
 * Forcing UDP on for every node is a setting, not a pipeline step, even though
 * it is implemented as one. Making someone add a "Quick Setting Operator" and
 * fill in a form to do it exposes an implementation detail as a task.
 *
 * These write back into the operator chain, so there is no second storage path
 *. The chain stays the single source of truth.
 */

defineProps<{ modelValue: CommonSettings }>();

const emit = defineEmits<{
  (e: "update:modelValue", value: CommonSettings): void;
}>();

const TRI: { value: TriState; label: string }[] = [
  { value: "default", label: "Leave as-is" },
  { value: "on", label: "Force on" },
  { value: "off", label: "Force off" },
];

const SWITCHES: { key: keyof CommonSettings; label: string; hint?: string }[] = [
  { key: "udp", label: "UDP relay", hint: "Needed for QUIC and most game traffic." },
  { key: "skipCertVerify", label: "Skip certificate verification" },
  { key: "tcpFastOpen", label: "TCP Fast Open" },
  { key: "vmessAead", label: "VMess AEAD" },
];

function setTri(props: { modelValue: CommonSettings }, key: keyof CommonSettings, value: TriState) {
  emit("update:modelValue", { ...props.modelValue, [key]: value });
}
</script>

<template>
  <section class="common">
    <h3>Common settings</h3>

    <div class="common-row">
      <span id="common-junk-label" class="common-label">Junk nodes</span>
      <div class="common-choices" role="radiogroup" aria-labelledby="common-junk-label">
        <button type="button" role="radio" :aria-checked="!modelValue.dropUseless" :class="{ 'is-active': !modelValue.dropUseless }"
          @click="emit('update:modelValue', { ...modelValue, dropUseless: false })"
        >
          Keep
        </button>
        <button type="button" role="radio" :aria-checked="modelValue.dropUseless" :class="{ 'is-active': modelValue.dropUseless }"
          @click="emit('update:modelValue', { ...modelValue, dropUseless: true })"
        >
          Drop
        </button>
      </div>
      <span class="common-hint">
        Providers often put traffic and expiry notices in the node list. Dropping them keeps a
        client's server list clean.
      </span>
    </div>

    <div v-for="row in SWITCHES" :key="row.key" class="common-row">
      <span :id="`common-${row.key}-label`" class="common-label">{{ row.label }}</span>
      <div class="common-choices" role="radiogroup" :aria-labelledby="`common-${row.key}-label`">
        <button
          v-for="choice in TRI"
          :key="choice.value"
          type="button"
          :class="{ 'is-active': modelValue[row.key] === choice.value }"
          @click="setTri({ modelValue }, row.key, choice.value)"
        >
          {{ choice.label }}
        </button>
      </div>
      <span v-if="row.hint" class="common-hint">{{ row.hint }}</span>
    </div>
  </section>
</template>

<style scoped>
.common {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  max-width: var(--lt-measure-form);
  padding: var(--space-4);
  /* An inset block of the editor panel, so a --muted step rather than a second
     border inside a bordered panel. */
  border: 0;
  border-radius: var(--radius-lg);
  background: var(--muted);
}

.common h3 {
  margin: 0;
  font-size: var(--lt-text-lg);
  font-weight: 650;
}

.common-row {
  display: grid;
  grid-template-columns: minmax(160px, 220px) 1fr;
  grid-template-areas: "label choices" "hint hint";
  gap: var(--space-1) var(--space-4);
  align-items: center;
}

.common-row + .common-row { padding-top: var(--space-3); border-top: 1px solid var(--border); }

.common-label {
  grid-area: label;
  font-size: var(--text-body);
  font-weight: 600;
}

.common-choices {
  grid-area: choices;
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-1);
}

.common-choices button {
  height: var(--lt-control-h);
  padding: 0 var(--space-3);
  border: 1px solid var(--border);
  border-radius: 999px;
  background: var(--background);
  color: var(--muted-foreground);
  font-size: var(--lt-text-sm);
}

.common-choices button:hover { color: var(--foreground); border-color: var(--lt-border-strong); }
.common-choices button:focus-visible { outline: none; box-shadow: var(--lt-focus-ring); }

.common-choices button.is-active {
  border-color: var(--primary);
  background: var(--lt-accent-soft);
  color: var(--lt-accent-ink);
}

.common-hint {
  grid-area: hint;
  max-width: var(--lt-measure-prose);
  font-size: var(--lt-text-xs);
  line-height: var(--lt-leading);
  color: var(--muted-foreground);
}

@media (max-width: 620px) {
  .common-row {
    grid-template-columns: 1fr;
    grid-template-areas: "label" "choices" "hint";
  }
}
</style>
