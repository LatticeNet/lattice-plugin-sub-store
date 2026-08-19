<script setup lang="ts">
import { computed, ref } from "vue";
import { X } from "@lucide/vue";

import { parseNumericArg, schemaFor, type OperatorField } from "../operatorSchema";
import { safeErrorMessage } from "../subStoreModel";
import CodeEditor from "./CodeEditor.vue";

/**
 * Renders one operator's arguments from its field schema.
 *
 * An operator the schema does not describe falls back to a raw JSON box rather
 * than to a form that quietly drops what it cannot represent. The catalogue is
 * extracted from the bundled engine by a test, so an engine bump can introduce
 * an operator this table has never seen, and a text box the operator can still
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
 * Two states would be wrong here, these operators force a protocol switch, and
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

/**
 * Pairs are stored as `{expr, now}` objects, which is what the engine reads:
 * `for (const {expr, now} of value)`. They were written as `[from, to]` tuples,
 * so every rename configured here destructured to undefined and renamed
 * nothing. The operator was in the chain, the preview showed it running, and
 * the names came out unchanged.
 *
 * Tuples are still READ, because records written under the old shape exist.
 */
function pairs(field: OperatorField): [string, string][] {
  const raw = props.args[field.key];
  if (!Array.isArray(raw)) return [["", ""]];
  const rows = raw
    .map((entry): [string, string] => {
      if (Array.isArray(entry)) return [String(entry[0] ?? ""), String(entry[1] ?? "")];
      if (entry && typeof entry === "object") {
        const pair = entry as { expr?: unknown; now?: unknown };
        return [String(pair.expr ?? ""), String(pair.now ?? "")];
      }
      return ["", ""];
    });
  return rows.length ? rows : [["", ""]];
}

/** The wire shape. Written on every change so nothing can save a tuple again. */
function toWirePairs(rows: [string, string][]): { expr: string; now: string }[] {
  return rows.map(([expr, now]) => ({ expr, now }));
}

/**
 * Every keystroke is kept, including on a row whose first column is still
 * empty.
 *
 * The old version dropped rows with an empty first column BEFORE committing,
 * which meant typing into the second column of a fresh row committed nothing
 * and the character vanished on the next render, and clearing the first column
 * of an existing rule silently threw away its second. Rows that are still
 * entirely blank are dropped only when the whole field is written out, so an
 * abandoned row does not become a rule.
 */
function setPair(field: OperatorField, index: number, slot: 0 | 1, value: string): void {
  const rows = pairs(field).map((row) => [...row] as [string, string]);
  if (!rows[index]) return;
  rows[index][slot] = value;
  // Every row the operator created is kept while they edit; entirely blank ones
  // are dropped on the way to the wire (toWireArgs), so a half-typed rule never
  // disappears from under the cursor.
  set(field.key, rows.length ? toWirePairs(rows) : undefined);
}

function addPair(field: OperatorField): void {
  const rows = pairs(field).map((row) => [...row] as [string, string]);
  rows.push(["", ""]);
  set(field.key, toWirePairs(rows));
}

function removePair(field: OperatorField, index: number): void {
  const rows = pairs(field).filter((_, i) => i !== index);
  set(field.key, rows.length ? toWirePairs(rows) : undefined);
}

// ── raw JSON fallback ──────────────────────────────────────────────────────

const rawJson = computed(() => JSON.stringify(props.args ?? {}, null, 2));

/**
 * The raw-JSON fallback reports its own syntax errors.
 *
 * It used to swallow them and keep the last value that parsed, so a typo meant
 * Save wrote something different from what was on screen and nothing said so.
 * Reformatting mid-keystroke would fight the person typing, so the text is left
 * exactly as entered and the problem is stated underneath.
 */
const rawError = ref("");

function setRaw(text: string): void {
  try {
    const parsed: unknown = JSON.parse(text || "{}");
    if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
      rawError.value = "Arguments must be a JSON object.";
      return;
    }
    rawError.value = "";
    emit("update:args", parsed as Record<string, unknown>);
  } catch (cause) {
    // V8 quotes the offending input back in a JSON.parse message ("Unexpected
    // token 'v', \"vless://...\" is not valid JSON"), and what an operator
    // pastes into an argument is routinely a node or provider URI whose
    // userinfo IS the credential. Every error this UI shows goes through the
    // redactor, this one included.
    rawError.value = safeErrorMessage(cause, "This is not valid JSON.");
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
          @input="set(field.key, parseNumericArg(($event.target as HTMLInputElement).value))"
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

        <CodeEditor
          v-else-if="field.kind === 'script'"
          :model-value="textValue(field)"
          language="javascript"
          :rows="8"
          @update:model-value="(value: string) => set(field.key, value)"
        />

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
            role="radio"
            :aria-checked="triValue(field) === choice"
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
            :aria-pressed="selected(field).includes(option.value)"
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
            <button
              type="button"
              class="op-pair-drop"
              :aria-label="`Remove rule ${index + 1}`"
              title="Remove this rule"
              @click="removePair(field, index)"
            >
              <X :size="13" aria-hidden="true" />
            </button>
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
      <span v-if="rawError" class="op-hint op-hint-error" role="alert">{{ rawError }}</span>
      <span v-else class="op-hint">
        This operator has no form yet, so its arguments are edited as JSON. The engine still
        validates the type.
      </span>
    </div>
  </div>
</template>

<style scoped>
/* Every control in here used to be a bare element with no rule of its own: an
   operator's text field, its number field and both columns of a rename rule
   rendered as user-agent inputs, which inside the console's dark theme means a
   white box. They are the most-used controls in the plugin. */

.op-args {
  display: flex;
  flex-direction: column;
  gap: var(--lt-space-3);
}

.op-field {
  display: flex;
  flex-direction: column;
  gap: var(--lt-space-1);
  max-width: var(--lt-measure-form);
}

.op-label {
  font-size: var(--lt-text-sm);
  font-weight: 600;
  color: var(--lt-fg);
}

.op-field > input,
.op-pair input {
  min-height: 30px;
  padding: var(--lt-space-1) var(--lt-space-2);
  border: 1px solid var(--lt-border);
  border-radius: var(--lt-radius);
  background: var(--lt-bg);
  color: var(--lt-fg);
  font-size: var(--lt-text-sm);
  outline: none;
  min-width: 0;
}

.op-field > input::placeholder { color: var(--lt-fg-muted); }

.op-field > input:focus-visible,
.op-pair input:focus-visible {
  box-shadow: var(--lt-focus-ring-tight);
  border-color: var(--lt-accent);
}

.op-field > input[type="text"] { max-width: var(--lt-measure-field); }
.op-field > input[type="number"] { max-width: 12ch; }

.op-hint {
  font-size: var(--lt-text-xs);
  line-height: var(--lt-leading);
  color: var(--lt-fg-muted);
  max-width: var(--lt-measure-prose);
}

/* The raw-JSON fallback reports syntax errors here. The class had no rule at
   all, so a typo in an operator's arguments was announced in exactly the same
   muted grey as the help text under it. */
.op-hint-error {
  padding: var(--lt-space-1) var(--lt-space-2);
  border-left: 2px solid var(--lt-danger);
  border-radius: 0 var(--lt-radius-sm) var(--lt-radius-sm) 0;
  background: var(--lt-danger-soft);
  color: var(--lt-fg);
  font-family: var(--lt-mono);
}

.op-none {
  margin: 0;
  font-size: var(--lt-text-sm);
  color: var(--lt-fg-muted);
}

.op-chips,
.op-tri {
  display: flex;
  flex-wrap: wrap;
  gap: var(--lt-space-1);
}

.op-chips button,
.op-tri button {
  height: var(--lt-control-h-sm);
  padding: 0 var(--lt-space-3);
  border: 1px solid var(--lt-border);
  border-radius: 999px;
  background: var(--lt-surface);
  color: var(--lt-fg-muted);
  font-size: var(--lt-text-xs);
}

.op-chips button:hover,
.op-tri button:hover { color: var(--lt-fg); border-color: var(--lt-border-strong); }

.op-chips button:focus-visible,
.op-tri button:focus-visible { outline: none; box-shadow: var(--lt-focus-ring); }

.op-chips button.is-active,
.op-tri button.is-active {
  border-color: var(--lt-accent);
  background: var(--lt-accent-soft);
  color: var(--lt-accent);
}

.op-switch {
  display: inline-flex;
  align-items: center;
  gap: var(--lt-space-2);
  font-size: var(--lt-text-sm);
  cursor: pointer;
}

.op-pairs {
  display: flex;
  flex-direction: column;
  gap: var(--lt-space-1);
  max-width: var(--lt-measure-form);
}

.op-pair-head,
.op-pair {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 1fr) 28px;
  gap: var(--lt-space-1);
  align-items: center;
}

.op-pair-head span {
  font-size: var(--lt-text-xs);
  color: var(--lt-fg-muted);
}

.op-pair-drop {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 26px;
  height: 26px;
  border: 0;
  border-radius: var(--lt-radius-sm);
  background: transparent;
  color: var(--lt-fg-muted);
}
.op-pair-drop:hover { background: var(--lt-danger-soft); color: var(--lt-danger); }
.op-pair-drop:focus-visible { outline: none; box-shadow: var(--lt-focus-ring-tight); }

.op-pair-add {
  align-self: flex-start;
  height: var(--lt-control-h-sm);
  margin-top: var(--lt-space-1);
  padding: 0 var(--lt-space-2);
  border: 1px solid var(--lt-border);
  border-radius: var(--lt-radius-sm);
  background: var(--lt-surface);
  color: var(--lt-fg);
  font-size: var(--lt-text-xs);
}
.op-pair-add:hover { border-color: var(--lt-accent); color: var(--lt-accent); }
.op-pair-add:focus-visible { outline: none; box-shadow: var(--lt-focus-ring); }
</style>
