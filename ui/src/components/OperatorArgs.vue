<script setup lang="ts">
import { computed } from "vue";

import { schemaFor, type OperatorField } from "../operatorSchema";

/**
 * Renders one operator's arguments from its field schema.
 *
 * An operator the schema does not describe falls back to a raw JSON box rather
 * than to a form that quietly drops what it cannot represent. The catalogue is
 * extracted from the bundled engine by a test, so an engine bump can introduce
 * an operator this table has never seen — and a text box the operator can still
 * use beats a form that silently loses their arguments.
 */

const props = defineProps<{
  type: string;
  args: Record<string, unknown>;
}>();

const emit = defineEmits<{
  (e: "update:args", value: Record<string, unknown>): void;
}>();

const schema = computed(() => schemaFor(props.type));

function set(key: string, value: unknown): void {
  const next = { ...props.args };
  if (value === undefined || value === "" || value === null) delete next[key];
  else next[key] = value;
  emit("update:args", next);
}

// ── field accessors ────────────────────────────────────────────────────────

function textValue(field: OperatorField): string {
  const raw = props.args[field.key];
  if (Array.isArray(raw)) return raw.join("\n");
  return raw === undefined || raw === null ? "" : String(raw);
}

/** Line-oriented fields store an array; the textarea shows one entry per line. */
function setLines(field: OperatorField, text: string): void {
  const lines = text
    .split("\n")
    .map((line) => line.trim())
    .filter(Boolean);
  set(field.key, lines.length ? lines : undefined);
}

function selected(field: OperatorField): string[] {
  const raw = props.args[field.key];
  return Array.isArray(raw) ? raw.map(String) : [];
}

function toggleMulti(field: OperatorField, value: string): void {
  const current = new Set(selected(field));
  if (current.has(value)) current.delete(value);
  else current.add(value);
  const next = [...current];
  set(field.key, next.length ? next : undefined);
}

function boolValue(field: OperatorField): boolean {
  const raw = props.args[field.key];
  if (raw === undefined) return field.default === true;
  return Boolean(raw);
}

/**
 * Tri-state: unset / on / off.
 *
 * Two states would be wrong here — these operators force a protocol switch, and
 * "leave it as the node had it" is a different instruction from "turn it off".
 */
function triValue(field: OperatorField): "unset" | "on" | "off" {
  const raw = props.args[field.key];
  if (raw === undefined || raw === null) return "unset";
  return raw ? "on" : "off";
}

function setTri(field: OperatorField, value: "unset" | "on" | "off"): void {
  if (value === "unset") set(field.key, undefined);
  else set(field.key, value === "on");
}

/** Pairs store [[from, to], …]; the editor shows one row per pair. */
function pairs(field: OperatorField): [string, string][] {
  const raw = props.args[field.key];
  if (!Array.isArray(raw)) return [["", ""]];
  const rows = raw
    .map((entry) => (Array.isArray(entry) ? [String(entry[0] ?? ""), String(entry[1] ?? "")] : ["", ""]))
    .map((entry) => entry as [string, string]);
  return rows.length ? rows : [["", ""]];
}

function setPair(field: OperatorField, index: number, slot: 0 | 1, value: string): void {
  const rows = pairs(field).map((row) => [...row] as [string, string]);
  rows[index][slot] = value;
  const kept = rows.filter((row) => row[0].trim() !== "");
  set(field.key, kept.length ? kept : undefined);
}

function addPair(field: OperatorField): void {
  const rows = pairs(field).map((row) => [...row] as [string, string]);
  rows.push(["", ""]);
  set(field.key, rows);
}

function removePair(field: OperatorField, index: number): void {
  const rows = pairs(field).filter((_, i) => i !== index);
  set(field.key, rows.length ? rows : undefined);
}

// ── raw JSON fallback ──────────────────────────────────────────────────────

const rawJson = computed(() => JSON.stringify(props.args ?? {}, null, 2));

function setRaw(text: string): void {
  try {
    const parsed: unknown = JSON.parse(text || "{}");
    if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) {
      emit("update:args", parsed as Record<string, unknown>);
    }
  } catch {
    // Left to the caller's validation: reformatting mid-keystroke would fight
    // the person typing.
  }
}
</script>

<template>
  <div class="op-args">
    <template v-if="schema">
      <p v-if="schema.fields.length === 0" class="op-none">
        {{ schema.summary }} No settings.
      </p>

      <div v-for="field in schema.fields" :key="field.key" class="op-field">
        <span class="op-label">{{ field.label }}</span>

        <input
          v-if="field.kind === 'text'"
          type="text"
          autocomplete="off"
          spellcheck="false"
          :placeholder="field.placeholder"
          :value="textValue(field)"
          @input="set(field.key, ($event.target as HTMLInputElement).value)"
        />

        <input
          v-else-if="field.kind === 'number'"
          type="number"
          :value="textValue(field)"
          @input="set(field.key, Number(($event.target as HTMLInputElement).value))"
        />

        <textarea
          v-else-if="field.kind === 'textarea'"
          class="code-area"
          rows="3"
          spellcheck="false"
          :placeholder="field.placeholder"
          :value="textValue(field)"
          @input="setLines(field, ($event.target as HTMLTextAreaElement).value)"
        ></textarea>

        <textarea
          v-else-if="field.kind === 'script'"
          class="code-area"
          rows="8"
          spellcheck="false"
          :value="textValue(field)"
          @input="set(field.key, ($event.target as HTMLTextAreaElement).value)"
        ></textarea>

        <select
          v-else-if="field.kind === 'select'"
          class="select"
          :value="textValue(field) || field.default"
          @change="set(field.key, ($event.target as HTMLSelectElement).value)"
        >
          <option v-for="option in field.options" :key="option.value" :value="option.value">
            {{ option.label }}
          </option>
        </select>

        <label v-else-if="field.kind === 'switch'" class="op-switch">
          <input
            type="checkbox"
            :checked="boolValue(field)"
            @change="set(field.key, ($event.target as HTMLInputElement).checked)"
          />
          <span>{{ boolValue(field) ? "Yes" : "No" }}</span>
        </label>

        <div v-else-if="field.kind === 'tristate'" class="op-tri">
          <button
            v-for="choice in (['unset', 'on', 'off'] as const)"
            :key="choice"
            type="button"
            :class="{ 'is-active': triValue(field) === choice }"
            @click="setTri(field, choice)"
          >
            {{ choice === "unset" ? "Leave" : choice === "on" ? "On" : "Off" }}
          </button>
        </div>

        <div v-else-if="field.kind === 'multiselect'" class="op-chips">
          <button
            v-for="option in field.options"
            :key="option.value"
            type="button"
            :class="{ 'is-active': selected(field).includes(option.value) }"
            @click="toggleMulti(field, option.value)"
          >
            {{ option.label }}
          </button>
        </div>

        <div v-else-if="field.kind === 'pairs'" class="op-pairs">
          <div class="op-pair-head">
            <span>{{ field.columns?.[0] }}</span>
            <span>{{ field.columns?.[1] }}</span>
            <span></span>
          </div>
          <div v-for="(row, index) in pairs(field)" :key="index" class="op-pair">
            <input
              type="text"
              spellcheck="false"
              :value="row[0]"
              @input="setPair(field, index, 0, ($event.target as HTMLInputElement).value)"
            />
            <input
              type="text"
              spellcheck="false"
              :value="row[1]"
              @input="setPair(field, index, 1, ($event.target as HTMLInputElement).value)"
            />
            <button type="button" class="op-pair-drop" @click="removePair(field, index)">✕</button>
          </div>
          <button type="button" class="op-pair-add" @click="addPair(field)">Add a rule</button>
        </div>

        <span v-if="field.hint" class="op-hint">{{ field.hint }}</span>
      </div>
    </template>

    <div v-else class="op-field">
      <span class="op-label">Arguments</span>
      <textarea
        class="code-area"
        rows="5"
        spellcheck="false"
        :value="rawJson"
        @input="setRaw(($event.target as HTMLTextAreaElement).value)"
      ></textarea>
      <span class="op-hint">
        This operator has no form yet, so its arguments are edited as JSON. The engine still
        validates the type.
      </span>
    </div>
  </div>
</template>

<style scoped>
.op-args {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.op-field {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.op-label {
  font-size: 12px;
  font-weight: 650;
  color: var(--text-2, #adb8c6);
}

.op-hint {
  font-size: 11.5px;
  line-height: 1.5;
  color: var(--text-3, #7c8896);
}

.op-none {
  margin: 0;
  font-size: 12.5px;
  color: var(--text-3, #7c8896);
}

.op-chips,
.op-tri {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.op-chips button,
.op-tri button {
  padding: 4px 10px;
  border: 1px solid var(--border, #242d3a);
  border-radius: 999px;
  background: transparent;
  color: inherit;
  font-size: 12px;
  cursor: pointer;
}

.op-chips button.is-active,
.op-tri button.is-active {
  border-color: var(--accent, #2dd4bf);
  background: color-mix(in srgb, var(--accent, #2dd4bf) 16%, transparent);
  color: var(--accent, #2dd4bf);
}

.op-switch {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  font-size: 12.5px;
}

.op-pairs {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.op-pair-head,
.op-pair {
  display: grid;
  grid-template-columns: 1fr 1fr 28px;
  gap: 6px;
  align-items: center;
}

.op-pair-head span {
  font-size: 11px;
  color: var(--text-3, #7c8896);
}

.op-pair-drop,
.op-pair-add {
  padding: 4px 8px;
  border: 1px solid var(--border, #242d3a);
  border-radius: 7px;
  background: transparent;
  color: inherit;
  font-size: 12px;
  cursor: pointer;
}

.op-pair-add {
  align-self: flex-start;
}
</style>
