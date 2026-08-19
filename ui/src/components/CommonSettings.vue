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
 * — the chain stays the single source of truth.
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
  gap: 14px;
  padding: 16px;
  border: 1px solid var(--border, #d9dde2);
  border-radius: 12px;
  background: var(--background, #f7f8f9);
}

.common h3 {
  margin: 0;
  font-size: 15px;
  font-weight: 700;
}

.common-row {
  display: grid;
  grid-template-columns: minmax(160px, 220px) 1fr;
  grid-template-areas: "label choices" "hint hint";
  gap: 6px 16px;
  align-items: center;
}

.common-label {
  grid-area: label;
  font-size: 13px;
  font-weight: 600;
}

.common-choices {
  grid-area: choices;
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.common-choices button {
  padding: 5px 12px;
  border: 1px solid var(--border, #d9dde2);
  border-radius: 999px;
  background: transparent;
  color: inherit;
  font-size: 12px;
  cursor: pointer;
}

.common-choices button.is-active {
  border-color: var(--primary, #1769aa);
  background: color-mix(in srgb, var(--primary, #1769aa) 14%, transparent);
  color: var(--primary, #1769aa);
}

.common-hint {
  grid-area: hint;
  font-size: 11.5px;
  line-height: 1.5;
  color: var(--muted-foreground, #656d76);
}

@media (max-width: 620px) {
  .common-row {
    grid-template-columns: 1fr;
    grid-template-areas: "label" "choices" "hint";
  }
}
</style>
