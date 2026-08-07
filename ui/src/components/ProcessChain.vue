<script setup lang="ts">
import { computed, ref } from "vue";
import { ChevronDown, GripVertical, Plus, Trash2 } from "@lucide/vue";

import { OPERATOR_SCHEMAS, defaultArgs, schemaFor } from "../operatorSchema";
import OperatorArgs from "./OperatorArgs.vue";

/**
 * The ordered operator chain, following the Sub-Store front end's model.
 *
 * Three properties are load-bearing and easy to lose:
 *  - order matters, because each step sees the previous step's output;
 *  - a step can be DISABLED rather than deleted, so trying the chain without it
 *    does not destroy its arguments;
 *  - a step can be renamed, because "Regex Rename Operator" three times in a row
 *    tells the reader nothing about which is which.
 *
 * Steps are held as plain objects and round-tripped whole, so fields this editor
 * does not understand — an `id` from an import, whatever upstream adds next —
 * survive an edit instead of being dropped on save.
 */

export interface ChainStep {
  type: string;
  customName?: string;
  disabled?: boolean;
  args?: Record<string, unknown>;
  [key: string]: unknown;
}

const props = defineProps<{
  steps: ChainStep[];
  /** Operator types the backend actually accepts, from the catalogue call. */
  catalog: readonly { type: string; scripting?: boolean }[];
}>();

const emit = defineEmits<{
  (e: "update:steps", value: ChainStep[]): void;
}>();

const expanded = ref<number | null>(null);
const adding = ref(false);
const dragIndex = ref<number | null>(null);

/** Catalogue entries grouped for the picker; unknown types still appear. */
const grouped = computed(() => {
  const groups: Record<string, { type: string; summary: string; scripting: boolean }[]> = {
    filter: [],
    rewrite: [],
    script: [],
    other: [],
  };
  for (const entry of props.catalog) {
    const schema = schemaFor(entry.type);
    const group = schema?.group ?? "other";
    groups[group].push({
      type: entry.type,
      summary: schema?.summary ?? "No description yet; arguments are edited as JSON.",
      scripting: Boolean(entry.scripting),
    });
  }
  return groups;
});

const GROUP_LABELS: Record<string, string> = {
  filter: "Keep or drop nodes",
  rewrite: "Change nodes",
  script: "Run JavaScript",
  other: "Not described yet",
};

function commit(next: ChainStep[]): void {
  emit("update:steps", next);
}

function add(type: string): void {
  commit([...props.steps, { type, args: defaultArgs(type) }]);
  expanded.value = props.steps.length;
  adding.value = false;
}

function remove(index: number): void {
  commit(props.steps.filter((_, i) => i !== index));
  if (expanded.value === index) expanded.value = null;
}

function toggleDisabled(index: number): void {
  const next = props.steps.map((step, i) =>
    i === index ? { ...step, disabled: !step.disabled } : step,
  );
  commit(next);
}

function rename(index: number, name: string): void {
  const next = props.steps.map((step, i) =>
    i === index ? { ...step, customName: name || undefined } : step,
  );
  commit(next);
}

function setArgs(index: number, args: Record<string, unknown>): void {
  const next = props.steps.map((step, i) => (i === index ? { ...step, args } : step));
  commit(next);
}

function move(from: number, to: number): void {
  if (to < 0 || to >= props.steps.length || from === to) return;
  const next = [...props.steps];
  const [moved] = next.splice(from, 1);
  next.splice(to, 0, moved);
  commit(next);
  if (expanded.value === from) expanded.value = to;
}

// Drag to reorder, with keyboard arrows as the equivalent that does not require
// a pointer — a chain is ordered data, and reordering must not be mouse-only.
function onDrop(index: number): void {
  if (dragIndex.value === null) return;
  move(dragIndex.value, index);
  dragIndex.value = null;
}

function label(step: ChainStep, index: number): string {
  return step.customName?.trim() || `${index + 1}. ${step.type}`;
}
</script>

<template>
  <div class="chain">
    <div class="chain-head">
      <div>
        <h3>Processing</h3>
        <p>
          Steps run top to bottom; each one sees what the previous produced.
          <template v-if="steps.length">
            {{ steps.filter((s) => !s.disabled).length }} of {{ steps.length }} active.
          </template>
        </p>
      </div>
      <button type="button" class="chain-add" @click="adding = !adding">
        <Plus :size="15" aria-hidden="true" /> Add step
      </button>
    </div>

    <div v-if="adding" class="picker">
      <template v-for="(entries, group) in grouped" :key="group">
        <template v-if="entries.length">
          <p class="picker-group">{{ GROUP_LABELS[group] }}</p>
          <button
            v-for="entry in entries"
            :key="entry.type"
            type="button"
            class="picker-item"
            @click="add(entry.type)"
          >
            <span class="picker-name">
              {{ entry.type }}
              <span v-if="entry.scripting" class="picker-tag">runs JS</span>
            </span>
            <span class="picker-summary">{{ entry.summary }}</span>
          </button>
        </template>
      </template>
    </div>

    <p v-if="!steps.length" class="chain-empty">
      No processing. The nodes are served exactly as the source provides them.
    </p>

    <ol v-else class="chain-list">
      <li
        v-for="(step, index) in steps"
        :key="index"
        :class="['chain-step', { 'is-off': step.disabled }]"
        draggable="true"
        @dragstart="dragIndex = index"
        @dragover.prevent
        @drop="onDrop(index)"
      >
        <div class="step-bar">
          <span class="step-grip" aria-hidden="true"><GripVertical :size="15" /></span>

          <button type="button" class="step-title" @click="expanded = expanded === index ? null : index">
            <ChevronDown :size="14" :class="['step-caret', { 'is-open': expanded === index }]" />
            {{ label(step, index) }}
          </button>

          <div class="step-actions">
            <button
              type="button"
              class="step-move"
              :disabled="index === 0"
              aria-label="Move up"
              @click="move(index, index - 1)"
            >
              ↑
            </button>
            <button
              type="button"
              class="step-move"
              :disabled="index === steps.length - 1"
              aria-label="Move down"
              @click="move(index, index + 1)"
            >
              ↓
            </button>
            <label class="step-toggle">
              <input type="checkbox" :checked="!step.disabled" @change="toggleDisabled(index)" />
              <span>{{ step.disabled ? "Off" : "On" }}</span>
            </label>
            <button type="button" class="step-drop" aria-label="Remove step" @click="remove(index)">
              <Trash2 :size="15" />
            </button>
          </div>
        </div>

        <div v-if="expanded === index" class="step-body">
          <label class="step-name">
            <span>Label</span>
            <input
              type="text"
              autocomplete="off"
              :placeholder="step.type"
              :value="step.customName ?? ''"
              @input="rename(index, ($event.target as HTMLInputElement).value)"
            />
          </label>

          <OperatorArgs
            :type="step.type"
            :args="(step.args as Record<string, unknown>) ?? {}"
            @update:args="setArgs(index, $event)"
          />
        </div>
      </li>
    </ol>
  </div>
</template>

<style scoped>
.chain {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.chain-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 14px;
}

.chain-head h3 {
  margin: 0 0 4px;
  font-size: 15px;
  font-weight: 700;
}

.chain-head p {
  margin: 0;
  font-size: 12.5px;
  line-height: 1.55;
  color: var(--text-3, #7c8896);
}

.chain-add,
.picker-item,
.step-move,
.step-drop,
.step-title {
  border: 1px solid var(--border, #242d3a);
  border-radius: 8px;
  background: transparent;
  color: inherit;
  cursor: pointer;
}

.chain-add {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 6px 12px;
  font-size: 12.5px;
  font-weight: 650;
  white-space: nowrap;
}

.picker {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 12px;
  border: 1px solid var(--border, #242d3a);
  border-radius: 10px;
  background: var(--surface-2, #0d1117);
  max-height: 320px;
  overflow-y: auto;
}

.picker-group {
  margin: 8px 0 2px;
  font-size: 10.5px;
  font-weight: 700;
  letter-spacing: 0.1em;
  text-transform: uppercase;
  color: var(--text-3, #7c8896);
}

.picker-item {
  display: flex;
  flex-direction: column;
  gap: 3px;
  padding: 8px 10px;
  text-align: left;
  border-color: transparent;
}

.picker-item:hover {
  border-color: var(--border, #242d3a);
  background: var(--surface, #161c26);
}

.picker-name {
  display: flex;
  align-items: center;
  gap: 7px;
  font-size: 13px;
  font-weight: 650;
}

.picker-tag {
  padding: 1px 6px;
  border-radius: 999px;
  background: color-mix(in srgb, var(--accent, #2dd4bf) 18%, transparent);
  color: var(--accent, #2dd4bf);
  font-size: 9.5px;
  font-weight: 700;
  text-transform: uppercase;
  letter-spacing: 0.06em;
}

.picker-summary {
  font-size: 11.5px;
  line-height: 1.45;
  color: var(--text-3, #7c8896);
}

.chain-empty {
  margin: 0;
  padding: 14px;
  border: 1px dashed var(--border, #242d3a);
  border-radius: 10px;
  font-size: 12.5px;
  color: var(--text-3, #7c8896);
}

.chain-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin: 0;
  padding: 0;
  list-style: none;
}

.chain-step {
  border: 1px solid var(--border, #242d3a);
  border-radius: 10px;
  background: var(--surface-2, #0d1117);
}

/* A disabled step stays legible — it is kept precisely so it can be read and
   switched back on, so fading it to near-invisible would defeat the point. */
.chain-step.is-off {
  opacity: 0.62;
  border-style: dashed;
}

.step-bar {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 10px;
}

.step-grip {
  color: var(--text-3, #7c8896);
  cursor: grab;
}

.step-title {
  display: flex;
  align-items: center;
  gap: 7px;
  flex: 1;
  padding: 5px 8px;
  border-color: transparent;
  font-size: 13px;
  font-weight: 600;
  text-align: left;
}

.step-caret {
  transition: transform 0.15s ease;
}

.step-caret.is-open {
  transform: rotate(180deg);
}

.step-actions {
  display: flex;
  align-items: center;
  gap: 5px;
}

.step-move,
.step-drop {
  padding: 4px 8px;
  font-size: 12px;
}

.step-move:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}

.step-toggle {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  font-size: 11.5px;
  color: var(--text-3, #7c8896);
}

.step-body {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 4px 12px 14px;
  border-top: 1px solid var(--border, #242d3a);
}

.step-name {
  display: flex;
  flex-direction: column;
  gap: 6px;
  font-size: 12px;
  font-weight: 650;
  color: var(--text-2, #adb8c6);
}
</style>
