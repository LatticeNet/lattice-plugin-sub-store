<script setup lang="ts">
import { computed, nextTick, ref } from "vue";
import { ArrowDown, ArrowUp, Plus, X } from "@lucide/vue";

import {
  CLOSED_VALUES,
  PREDICATE_FIELDS,
  PREDICATE_FIELD_NAMES,
  newPredicate,
  opArity,
  opsOf,
  predicateProblem,
  problemText,
  readFilterArgs,
  readsGeo,
  sampleValues,
  withField,
  withOp,
  writeFilterArgs,
  type CatalogueRow,
  type Predicate,
  type PredicateOp,
  type StructuredFilterArgs,
} from "../fleet";
import { t } from "../i18n";
import { parseNumericArg } from "../operatorSchema";

/**
 * One Structured Filter step's arguments: keep or drop, all or any, which
 * geo the geo fields read, and the conditions (s2-plan sections 2.4 and 11).
 *
 * It is the renderer of the operator schema's `predicates` kind, used by the
 * chain under Operations and by the selection editor on the Content tab, so
 * the two places a step can be edited behave the same. Every control has a
 * keyboard path: the segments are native radios, a condition is added,
 * removed and moved with buttons that keep focus where the operator's hands
 * are, and a value is typed and committed with Enter.
 *
 * A probe field is offered disabled while the catalogue says probe data does
 * not exist on this server, because save refuses it (fleet_probe_unavailable);
 * a condition that already names one stays and says why it would be refused.
 */
const props = defineProps<{
  args: unknown;
  /** Rows the catalogue answered, for the values' typeahead. */
  sampleRows?: readonly CatalogueRow[];
  probeUnavailable?: boolean;
  readOnly?: boolean;
  /** Unique within the page: radio names and datalist ids hang off it. */
  idPrefix: string;
}>();

const emit = defineEmits<{
  (e: "update:args", value: Record<string, unknown>): void;
}>();

const parsed = computed<StructuredFilterArgs>(() => readFilterArgs(props.args));
const showGeo = computed(() => readsGeo(parsed.value) || parsed.value.geo === "entry");

function commit(next: StructuredFilterArgs): void {
  emit("update:args", writeFilterArgs(next));
}

function setMode(mode: StructuredFilterArgs["mode"]): void {
  commit({ ...parsed.value, mode });
}

function setMatch(match: StructuredFilterArgs["match"]): void {
  commit({ ...parsed.value, match });
}

function setGeo(geo: "exit" | "entry"): void {
  const next = { ...parsed.value };
  if (geo === "entry") next.geo = "entry";
  else delete next.geo;
  commit(next);
}

function replace(index: number, predicate: Predicate): void {
  const predicates = parsed.value.predicates.map((p, i) => (i === index ? predicate : p));
  commit({ ...parsed.value, predicates });
}

// ── focus that follows the operator's hands ───────────────────────────────────

const root = ref<HTMLElement | null>(null);

async function focusRow(index: number, part: "field" | "up" | "down" | "add"): Promise<void> {
  await nextTick();
  const selector = part === "add" ? "[data-sp-add]" : `[data-sp-row="${index}"] [data-sp-${part}]`;
  const target = root.value?.querySelector<HTMLElement>(selector);
  if (target && !(target as HTMLButtonElement).disabled) target.focus();
  else root.value?.querySelector<HTMLElement>(`[data-sp-row="${index}"] [data-sp-field]`)?.focus();
}

function add(): void {
  const field = PREDICATE_FIELD_NAMES.find((name) => !(props.probeUnavailable && PREDICATE_FIELDS[name]?.probe)) ?? "country";
  const predicates = [...parsed.value.predicates, newPredicate(field)];
  commit({ ...parsed.value, predicates });
  void focusRow(predicates.length - 1, "field");
}

function remove(index: number): void {
  const predicates = parsed.value.predicates.filter((_, i) => i !== index);
  commit({ ...parsed.value, predicates });
  if (predicates.length === 0) void focusRow(0, "add");
  else void focusRow(Math.min(index, predicates.length - 1), "field");
}

function move(index: number, direction: -1 | 1): void {
  const target = index + direction;
  const predicates = [...parsed.value.predicates];
  if (target < 0 || target >= predicates.length) return;
  const [moved] = predicates.splice(index, 1);
  predicates.splice(target, 0, moved!);
  commit({ ...parsed.value, predicates });
  void focusRow(target, direction < 0 ? "up" : "down");
}

// ── one condition ─────────────────────────────────────────────────────────────

function fieldLabel(field: string): string {
  return (t.structured.fields as Record<string, string>)[field] ?? field;
}

/** A probe field is disabled while probe data is unavailable; the row's problem line says why. */
function fieldOptionLabel(field: string): string {
  return fieldLabel(field);
}

function opLabel(field: string, op: PredicateOp): string {
  const list = PREDICATE_FIELDS[field]?.kind === "list";
  if (list && op === "in") return t.structured.ops.inList;
  if (list && op === "not_in") return t.structured.ops.notInList;
  return t.structured.ops[op];
}

function unitOf(field: string): string {
  return (t.structured.units as Record<string, string>)[field] ?? "";
}

function problemOf(p: Predicate): string {
  const found = predicateProblem(p);
  if (found) return problemText(found, p.field);
  if (props.probeUnavailable && PREDICATE_FIELDS[p.field]?.probe) return t.structured.problems.probeUnavailable;
  return "";
}

function suggestions(p: Predicate): string[] {
  const chosen = new Set(p.values ?? []);
  return sampleValues(props.sampleRows ?? [], p.field, parsed.value.geo ?? "exit").filter((value) => !chosen.has(value)).slice(0, 40);
}

const drafts = ref<Record<number, string>>({});

function addValue(index: number, raw: string): void {
  const p = parsed.value.predicates[index];
  if (!p) return;
  const additions = raw.split(",").map((value) => value.trim()).filter(Boolean);
  drafts.value = { ...drafts.value, [index]: "" };
  if (!additions.length) return;
  const values = [...(p.values ?? [])];
  for (const value of additions) if (!values.includes(value)) values.push(value);
  replace(index, { ...p, values });
}

function removeValue(index: number, value: string, refocus = false): void {
  const p = parsed.value.predicates[index];
  if (!p) return;
  replace(index, { ...p, values: (p.values ?? []).filter((v) => v !== value) });
  // The chip's button is gone with the chip: the condition's value box is
  // where the operator's hands go next.
  if (refocus) void nextTick(() => root.value?.querySelector<HTMLElement>(`[data-sp-row="${index}"] .sp-value-input`)?.focus());
}

function toggleValue(index: number, value: string): void {
  const p = parsed.value.predicates[index];
  if (!p) return;
  const values = p.values ?? [];
  replace(index, { ...p, values: values.includes(value) ? values.filter((v) => v !== value) : [...values, value] });
}

function onValueKey(index: number, event: KeyboardEvent): void {
  const input = event.target as HTMLInputElement;
  if (event.key === "Enter" || event.key === ",") {
    event.preventDefault();
    addValue(index, input.value);
    return;
  }
  if (event.key === "Backspace" && input.value === "") {
    const values = parsed.value.predicates[index]?.values ?? [];
    const last = values[values.length - 1];
    if (last !== undefined) removeValue(index, last);
  }
}

/** A pick from the typeahead list commits at once; typing waits for Enter. */
function onValueInput(index: number, event: Event): void {
  const input = event.target as HTMLInputElement;
  const inputType = (event as InputEvent).inputType;
  drafts.value = { ...drafts.value, [index]: input.value };
  if (inputType === undefined || inputType === "insertReplacementText") {
    const p = parsed.value.predicates[index];
    if (p && suggestions(p).includes(input.value)) addValue(index, input.value);
  }
}

function setNumber(index: number, raw: string): void {
  const p = parsed.value.predicates[index];
  if (!p) return;
  const value = parseNumericArg(raw);
  const next: Predicate = { field: p.field, op: p.op };
  if (value !== undefined) next.value = value;
  replace(index, next);
}

const segments = computed(() => ({
  mode: [
    { value: "keep" as const, label: t.structured.mode.keep },
    { value: "drop" as const, label: t.structured.mode.drop },
  ],
  match: [
    { value: "all" as const, label: t.structured.match.all },
    { value: "any" as const, label: t.structured.match.any },
  ],
  geo: [
    { value: "exit" as const, label: t.structured.geo.exit },
    { value: "entry" as const, label: t.structured.geo.entry },
  ],
}));
</script>

<template>
  <div ref="root" class="sp" :data-read-only="readOnly ? '' : undefined">
    <div class="sp-head">
      <fieldset class="sp-seg" :disabled="readOnly">
        <legend class="sp-seg-legend">{{ t.structured.mode.label }}</legend>
        <div class="sp-seg-row">
          <label v-for="option in segments.mode" :key="option.value" :class="['sp-seg-item', { 'is-on': parsed.mode === option.value }]">
            <input
              type="radio"
              :name="`${idPrefix}-mode`"
              :value="option.value"
              :checked="parsed.mode === option.value"
              @change="setMode(option.value)"
            />
            <span>{{ option.label }}</span>
          </label>
        </div>
      </fieldset>
      <fieldset class="sp-seg" :disabled="readOnly">
        <legend class="sp-seg-legend">{{ t.structured.match.label }}</legend>
        <div class="sp-seg-row">
          <label v-for="option in segments.match" :key="option.value" :class="['sp-seg-item', { 'is-on': parsed.match === option.value }]">
            <input
              type="radio"
              :name="`${idPrefix}-match`"
              :value="option.value"
              :checked="parsed.match === option.value"
              @change="setMatch(option.value)"
            />
            <span>{{ option.label }}</span>
          </label>
        </div>
      </fieldset>
      <fieldset v-if="showGeo" class="sp-seg" :disabled="readOnly" :aria-describedby="`${idPrefix}-geo-hint`">
        <legend class="sp-seg-legend">{{ t.structured.geo.label }}</legend>
        <div class="sp-seg-row">
          <label
            v-for="option in segments.geo"
            :key="option.value"
            :class="['sp-seg-item', { 'is-on': (parsed.geo ?? 'exit') === option.value }]"
          >
            <input
              type="radio"
              :name="`${idPrefix}-geo`"
              :value="option.value"
              :checked="(parsed.geo ?? 'exit') === option.value"
              @change="setGeo(option.value)"
            />
            <span>{{ option.label }}</span>
          </label>
        </div>
        <span :id="`${idPrefix}-geo-hint`" class="sp-hint">{{ t.structured.geo.hint }}</span>
      </fieldset>
    </div>

    <ol v-if="parsed.predicates.length" class="sp-list">
      <li
        v-for="(p, index) in parsed.predicates"
        :key="index"
        class="sp-row"
        data-testid="predicate"
        :data-sp-row="index"
        :aria-label="t.structured.condition(index + 1)"
      >
        <div class="sp-controls">
          <select
            class="select sp-field"
            data-sp-field
            :disabled="readOnly"
            :aria-label="`${t.structured.condition(index + 1)}: ${t.structured.field}`"
            :value="p.field"
            @change="replace(index, withField(p, ($event.target as HTMLSelectElement).value))"
          >
            <option v-if="!PREDICATE_FIELDS[p.field]" :value="p.field">{{ p.field }}</option>
            <option
              v-for="name in PREDICATE_FIELD_NAMES"
              :key="name"
              :value="name"
              :disabled="probeUnavailable && PREDICATE_FIELDS[name]?.probe && name !== p.field"
            >
              {{ fieldOptionLabel(name) }}
            </option>
          </select>
          <select
            class="select sp-op"
            :disabled="readOnly || !PREDICATE_FIELDS[p.field]"
            :aria-label="`${t.structured.condition(index + 1)}: ${t.structured.operator}`"
            :value="p.op"
            @change="replace(index, withOp(p, ($event.target as HTMLSelectElement).value as PredicateOp))"
          >
            <option v-if="!opsOf(p.field).includes(p.op)" :value="p.op">{{ p.op }}</option>
            <option v-for="op in opsOf(p.field)" :key="op" :value="op">{{ opLabel(p.field, op) }}</option>
          </select>

          <div class="sp-row-actions">
            <button
              type="button"
              class="sp-icon"
              data-sp-up
              :disabled="readOnly || index === 0"
              :aria-label="t.structured.moveUp(index + 1)"
              :title="t.structured.moveUp(index + 1)"
              @click="move(index, -1)"
            >
              <ArrowUp :size="14" aria-hidden="true" />
            </button>
            <button
              type="button"
              class="sp-icon"
              data-sp-down
              :disabled="readOnly || index === parsed.predicates.length - 1"
              :aria-label="t.structured.moveDown(index + 1)"
              :title="t.structured.moveDown(index + 1)"
              @click="move(index, 1)"
            >
              <ArrowDown :size="14" aria-hidden="true" />
            </button>
            <button
              type="button"
              class="sp-icon is-danger"
              :disabled="readOnly"
              :aria-label="t.structured.removeCondition(index + 1)"
              :title="t.structured.removeCondition(index + 1)"
              @click="remove(index)"
            >
              <X :size="14" aria-hidden="true" />
            </button>
          </div>
        </div>

        <!-- The values, in the shape the operator takes. -->
        <div v-if="opArity(p.op) === 'values' && CLOSED_VALUES[p.field]" class="sp-chips" role="group" :aria-label="`${t.structured.condition(index + 1)}: ${t.structured.values}`">
          <button
            v-for="value in CLOSED_VALUES[p.field]"
            :key="value"
            type="button"
            class="sp-toggle"
            :disabled="readOnly"
            :aria-pressed="(p.values ?? []).includes(value)"
            @click="toggleValue(index, value)"
          >
            {{ value }}
          </button>
        </div>
        <div v-else-if="opArity(p.op) === 'values'" class="sp-values">
          <span v-for="value in p.values ?? []" :key="value" class="sp-chip">
            <span class="sp-chip-text">{{ value }}</span>
            <button
              type="button"
              class="sp-chip-drop"
              :disabled="readOnly"
              :aria-label="t.structured.removeValue(value)"
              :title="t.structured.removeValue(value)"
              @click="removeValue(index, value, true)"
            >
              <X :size="12" aria-hidden="true" />
            </button>
          </span>
          <input
            v-if="!readOnly"
            type="text"
            class="sp-value-input"
            autocomplete="off"
            spellcheck="false"
            :list="`${idPrefix}-values-${index}`"
            :aria-label="`${t.structured.condition(index + 1)}: ${t.structured.values}`"
            :placeholder="t.structured.valuePlaceholder"
            :value="drafts[index] ?? ''"
            @input="onValueInput(index, $event)"
            @keydown="onValueKey(index, $event)"
            @blur="addValue(index, ($event.target as HTMLInputElement).value)"
          />
          <datalist :id="`${idPrefix}-values-${index}`">
            <option v-for="value in suggestions(p)" :key="value" :value="value"></option>
          </datalist>
        </div>
        <label v-else-if="opArity(p.op) === 'value'" class="sp-number">
          <span class="pc-sr-only">{{ `${t.structured.condition(index + 1)}: ${t.structured.value}` }}</span>
          <input
            type="number"
            inputmode="decimal"
            step="1"
            :min="PREDICATE_FIELDS[p.field]?.min"
            :max="PREDICATE_FIELDS[p.field]?.max"
            :disabled="readOnly"
            :value="p.value ?? ''"
            @input="setNumber(index, ($event.target as HTMLInputElement).value)"
          />
          <span v-if="unitOf(p.field)" class="sp-unit">{{ unitOf(p.field) }}</span>
        </label>

        <p v-if="problemOf(p)" class="sp-problem" data-testid="predicate-problem">{{ problemOf(p) }}</p>
      </li>
    </ol>
    <p v-else class="sp-empty">{{ t.structured.noConditions }}</p>

    <button v-if="!readOnly" type="button" class="sp-add" data-sp-add @click="add()">
      <Plus :size="14" aria-hidden="true" /> {{ t.structured.addCondition }}
    </button>
  </div>
</template>

<style scoped>
.sp {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  min-width: 0;
}

.sp-head {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-start;
  gap: var(--space-2) var(--space-4);
}

.sp-seg {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  min-width: 0;
  margin: 0;
  padding: 0;
  border: 0;
}

.sp-seg-legend {
  padding: 0;
  font-size: var(--lt-text-xs);
  font-weight: 600;
  color: var(--muted-foreground);
}

.sp-seg-row {
  display: inline-flex;
  flex-wrap: wrap;
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  background: var(--card);
  overflow: hidden;
}

.sp-seg-item {
  position: relative;
  display: inline-flex;
  align-items: center;
  min-height: var(--lt-control-h-sm);
  padding: 0 var(--space-3);
  font-size: var(--lt-text-xs);
  color: var(--muted-foreground);
  cursor: pointer;
}

.sp-seg-item + .sp-seg-item { border-left: 1px solid var(--border); }

/* The native radio stays in the tab order and answers the arrow keys; only
   its circle is hidden, the segment is what shows the choice. */
.sp-seg-item input {
  position: absolute;
  inset: 0;
  margin: 0;
  opacity: 0;
  cursor: inherit;
}

.sp-seg-item.is-on {
  background: var(--lt-accent-soft);
  color: var(--lt-accent-ink);
  font-weight: 600;
}

.sp-seg-item:has(input:focus-visible) { box-shadow: inset var(--lt-focus-ring-tight); z-index: 1; }
.sp-seg:disabled .sp-seg-item { cursor: default; }

.sp-hint {
  max-width: var(--lt-measure-prose);
  font-size: var(--lt-text-xs);
  line-height: var(--lt-leading);
  color: var(--muted-foreground);
}

.sp-list {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  margin: 0;
  padding: 0;
  list-style: none;
}

.sp-row {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  padding: var(--space-2);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  background: var(--background);
  min-width: 0;
}

.sp-controls {
  display: grid;
  grid-template-columns: minmax(0, 12rem) minmax(0, 10rem) auto;
  align-items: center;
  gap: var(--space-1);
}

.sp-field,
.sp-op {
  min-height: var(--lt-control-h-sm);
  font-size: var(--lt-text-sm);
  min-width: 0;
  width: 100%;
}

.sp-row-actions {
  display: inline-flex;
  align-items: center;
  justify-self: end;
  gap: 2px;
}

.sp-icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  border: 0;
  border-radius: var(--radius-sm);
  background: transparent;
  color: var(--muted-foreground);
}

.sp-icon:hover:not(:disabled) { background: var(--muted); color: var(--foreground); }
.sp-icon:focus-visible { outline: none; box-shadow: var(--lt-focus-ring-tight); }
.sp-icon:disabled { opacity: 0.35; }
.sp-icon.is-danger { color: var(--lt-danger-ink); }
.sp-icon.is-danger:hover:not(:disabled) { background: var(--lt-danger-soft); }

.sp-values {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-1);
  min-height: var(--lt-control-h-sm);
  padding: 2px var(--space-1);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  background: var(--card);
}

.sp-values:focus-within { border-color: var(--primary); box-shadow: var(--lt-focus-ring-tight); }

.sp-chip {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  max-width: 100%;
  min-height: 22px;
  padding: 0 2px 0 var(--space-2);
  border: 1px solid var(--lt-accent-border);
  border-radius: 999px;
  background: var(--lt-accent-soft);
  color: var(--lt-accent-ink);
  font-family: var(--font-mono);
  font-size: var(--lt-text-xs);
}

.sp-chip-text {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.sp-chip-drop {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  margin: -2px 0;
  border: 0;
  border-radius: 999px;
  background: transparent;
  color: inherit;
}

.sp-chip-drop:hover:not(:disabled) { background: var(--card); }
.sp-chip-drop:focus-visible { outline: none; box-shadow: var(--lt-focus-ring-tight); }

/* Wide enough for its own hint; past the chips it takes a line of its own. */
.sp-value-input {
  flex: 1 1 18ch;
  min-width: min(18ch, 100%);
  min-height: 24px;
  padding: 0 var(--space-1);
  border: 0;
  background: transparent;
  color: var(--foreground);
  font-family: var(--font-mono);
  font-size: var(--lt-text-sm);
  outline: none;
}

.sp-value-input::placeholder { color: var(--muted-foreground); font-family: var(--lt-font); }

.sp-chips {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-1);
}

.sp-toggle {
  min-height: var(--lt-control-h-sm);
  padding: 0 var(--space-3);
  border: 1px solid var(--border);
  border-radius: 999px;
  background: var(--card);
  color: var(--muted-foreground);
  font-family: var(--font-mono);
  font-size: var(--lt-text-xs);
}

.sp-toggle:hover:not(:disabled) { color: var(--foreground); border-color: var(--lt-border-strong); }
.sp-toggle:focus-visible { outline: none; box-shadow: var(--lt-focus-ring); }
.sp-toggle[aria-pressed="true"] { border-color: var(--primary); background: var(--lt-accent-soft); color: var(--lt-accent-ink); }

.sp-number {
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
}

.sp-number input {
  width: 12ch;
  min-height: var(--lt-control-h-sm);
  padding: 0 var(--space-2);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  background: var(--card);
  color: var(--foreground);
  font-family: var(--font-mono);
  font-size: var(--lt-text-sm);
  outline: none;
}

.sp-number input:focus-visible { border-color: var(--primary); box-shadow: var(--lt-focus-ring-tight); }

.sp-unit {
  font-size: var(--lt-text-xs);
  color: var(--muted-foreground);
}

.sp-problem {
  margin: 0;
  padding: 2px var(--space-2);
  border-left: 2px solid var(--warning);
  background: var(--lt-warn-soft);
  color: var(--warning-text);
  font-size: var(--lt-text-xs);
  line-height: var(--lt-leading);
}

.sp-empty {
  margin: 0;
  padding: var(--space-2);
  border: 1px dashed var(--border);
  border-radius: var(--radius-md);
  font-size: var(--lt-text-sm);
  color: var(--muted-foreground);
}

.sp-add {
  display: inline-flex;
  align-items: center;
  align-self: flex-start;
  gap: var(--space-1);
  min-height: var(--lt-control-h-sm);
  padding: 0 var(--space-2);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: var(--card);
  color: var(--foreground);
  font-size: var(--lt-text-xs);
}

.sp-add:hover { border-color: var(--primary); color: var(--lt-accent-ink); }
.sp-add:focus-visible { outline: none; box-shadow: var(--lt-focus-ring); }

/* At phone width the field takes its own line, so a long field name reads
   whole ("Состояние службы", "Has a verified DDNS name"), and the operator
   shares the next line with the row's buttons. */
@media (max-width: 480px) {
  .sp-controls {
    grid-template-columns: minmax(0, 1fr) auto;
    grid-template-areas: "field field" "op actions";
  }

  .sp-field { grid-area: field; }
  .sp-op { grid-area: op; }
  .sp-row-actions { grid-area: actions; }
}
</style>
