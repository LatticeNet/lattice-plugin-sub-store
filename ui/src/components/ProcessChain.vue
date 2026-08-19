<script setup lang="ts">
import { computed, ref } from "vue";
import { ChevronRight, Copy, Eye, GripVertical, LoaderCircle, Trash2 } from "@lucide/vue";

import { defaultArgs, fromWireArgs, schemaFor, toWireArgs } from "../operatorSchema";
import OperatorArgs from "./OperatorArgs.vue";

/**
 * The ordered operator chain.
 *
 * Three properties are load-bearing and easy to lose:
 *  - order matters, because each step sees the previous step's output;
 *  - a step can be DISABLED rather than deleted, so trying the chain without it
 *    does not destroy its arguments;
 *  - a step can be renamed, because three "Regex Rename" rows in a row tell the
 *    reader nothing about which is which.
 *
 * Steps round-trip whole, so fields this editor does not understand — an `id`
 * from an import, whatever upstream adds next — survive an edit.
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
  /** Operator types the backend accepts, from the catalogue call. */
  catalog: readonly { type: string; scripting?: boolean; response?: boolean }[];
  /**
   * Types edited by the common-settings block above. They stay in the chain —
   * they are ordinary operators — but listing them here too would give one
   * setting two controls that can disagree.
   */
  managedTypes?: readonly string[];
  /**
   * Restricts the palette to the operators that make sense here. A plain-text
   * file runs its chain over the document, where a region filter or a rename
   * has nothing to act on — offering them invites a chain that cannot work.
   */
  /**
   * Which chain this is. A response chain edits a served document; a node chain
   * processes proxies. The engine skips the wrong kind silently, so the palette
   * only ever offers one of them.
   */
  chain?: "nodes" | "response";
  /** Wording for a chain that does not process nodes. */
  heading?: string;
  emptyCopy?: string;
  /**
   * Whether the parent can run a preview cut off after one step. Off by
   * default so a chain rendered where no preview exists shows no dead
   * control.
   */
  canPreviewStep?: boolean;
  /** The step a preview is currently running for, so its control can spin. */
  previewingStep?: number | null;
  /**
   * Whether the catalogue is still arriving. Without it an empty palette can
   * only say "loading", which is what it said forever when the call failed or
   * the bundle exposed no operators at all.
   */
  catalogState?: "idle" | "loading" | "ready" | "error";
}>();

const emit = defineEmits<{
  (e: "update:steps", value: ChainStep[]): void;
  (e: "preview-step", index: number, label: string): void;
}>();

const expanded = ref<number | null>(null);
const dragIndex = ref<number | null>(null);

const managed = computed(() => new Set(props.managedTypes ?? []));

/** Index pairs so the visible list can act on the real array positions. */
const visible = computed(() =>
  props.steps
    .map((step, index) => ({ step, index }))
    .filter((entry) => !managed.value.has(entry.step.type)),
);

/** Every operator, in one flat grid — one click to add, nothing hidden. */
const wantsResponse = computed(() => props.chain === "response");

const addable = computed(() =>
  props.catalog
    .filter((entry) => !managed.value.has(entry.type))
    .filter((entry) => Boolean(entry.response) === wantsResponse.value)
    .map((entry) => ({
      type: entry.type,
      label: schemaFor(entry.type)?.label ?? entry.type,
      scripting: Boolean(entry.scripting),
    })),
);

function label(step: ChainStep, position: number): string {
  const name = step.customName?.trim();
  if (name) return name;
  return `${position}. ${schemaFor(step.type)?.label ?? step.type}`;
}

function commit(next: ChainStep[]): void {
  emit("update:steps", next);
}

function add(type: string): void {
  commit([...props.steps, { type, args: toWireArgs(type, defaultArgs(type)) as ChainStep["args"] }]);
  expanded.value = props.steps.length;
}

function remove(index: number): void {
  commit(props.steps.filter((_, i) => i !== index));
  if (expanded.value === index) expanded.value = null;
}

/** Duplicating a tuned step is how you make a variant without retyping it. */
function duplicate(index: number): void {
  const source = props.steps[index];
  const copy: ChainStep = {
    ...source,
    args: source.args ? { ...source.args } : undefined,
    customName: source.customName ? `${source.customName} copy` : undefined,
  };
  const next = [...props.steps];
  next.splice(index + 1, 0, copy);
  commit(next);
}

function toggleDisabled(index: number): void {
  commit(props.steps.map((step, i) => (i === index ? { ...step, disabled: !step.disabled } : step)));
}

function rename(index: number, name: string): void {
  commit(
    props.steps.map((step, i) => (i === index ? { ...step, customName: name || undefined } : step)),
  );
}

function setArgs(index: number, args: Record<string, unknown>): void {
  // The editor works in named fields; the engine takes each operator's own
  // shape. Converting here means every write leaves the store in the shape the
  // engine reads, including for records saved before this was understood.
  commit(
    props.steps.map((step, i) =>
      i === index ? { ...step, args: toWireArgs(step.type, args) as ChainStep["args"] } : step,
    ),
  );
}

/**
 * Move a step to the position of its neighbour IN THE VISIBLE LIST.
 *
 * The buttons used to pass raw array indices while their disabled state was
 * computed from visible positions. With a settings-managed step sitting between
 * two visible ones — ordinary in an imported chain — "move down" swapped a step
 * with a row nobody can see, so the order on screen did not change and the
 * click read as broken.
 */
function moveVisible(entryIndex: number, direction: -1 | 1): void {
  const order = visible.value.map((entry) => entry.index);
  const position = order.indexOf(entryIndex);
  const targetPosition = position + direction;
  if (position === -1 || targetPosition < 0 || targetPosition >= order.length) return;
  move(entryIndex, order[targetPosition]!);
}

function move(from: number, to: number): void {
  if (to < 0 || to >= props.steps.length || from === to) return;
  const next = [...props.steps];
  const [moved] = next.splice(from, 1);
  next.splice(to, 0, moved);
  commit(next);
  if (expanded.value === from) expanded.value = to;
}

function onDrop(index: number): void {
  if (dragIndex.value === null) return;
  move(dragIndex.value, index);
  dragIndex.value = null;
}

const activeCount = computed(() => visible.value.filter((entry) => !entry.step.disabled).length);
</script>

<template>
  <section class="chain">
    <div class="chain-head">
      <h3>{{ heading ?? "Node operations" }}</h3>
      <span v-if="visible.length" class="chain-count">
        {{ activeCount }} of {{ visible.length }} active
      </span>
    </div>

    <ol v-if="visible.length" class="chain-list">
      <li
        v-for="(entry, position) in visible"
        :key="entry.index"
        :class="['chain-step', { 'is-off': entry.step.disabled }]"
        draggable="true"
        @dragstart="dragIndex = entry.index"
        @dragover.prevent
        @drop="onDrop(entry.index)"
      >
        <div class="step-bar">
          <span class="step-grip" aria-hidden="true"><GripVertical :size="15" /></span>

          <button
            type="button"
            class="step-title"
            @click="expanded = expanded === entry.index ? null : entry.index"
          >
            <ChevronRight
              :size="14"
              :class="['step-caret', { 'is-open': expanded === entry.index }]"
            />
            {{ label(entry.step, position + 1) }}
          </button>

          <div class="step-actions">
            <label class="step-toggle">
              <input
                type="checkbox"
                :checked="!entry.step.disabled"
                @change="toggleDisabled(entry.index)"
              />
              <span>Enabled</span>
            </label>
            <button
              type="button"
              class="step-icon"
              :disabled="position === 0"
              aria-label="Move up"
              @click="moveVisible(entry.index, -1)"
            >
              ↑
            </button>
            <button
              type="button"
              class="step-icon"
              :disabled="position === visible.length - 1"
              aria-label="Move down"
              @click="moveVisible(entry.index, 1)"
            >
              ↓
            </button>
            <button
              v-if="canPreviewStep"
              type="button"
              class="step-icon"
              :disabled="entry.step.disabled || previewingStep !== null"
              :title="`Preview the nodes as they leave this step`"
              :aria-label="`Preview up to step ${position + 1}`"
              @click="emit('preview-step', entry.index, label(entry.step, position + 1))"
            >
              <LoaderCircle v-if="previewingStep === entry.index" :size="14" class="spin" />
              <Eye v-else :size="14" />
            </button>
            <button
              type="button"
              class="step-icon"
              aria-label="Duplicate step"
              @click="duplicate(entry.index)"
            >
              <Copy :size="14" />
            </button>
            <button
              type="button"
              class="step-icon is-danger"
              aria-label="Remove step"
              @click="remove(entry.index)"
            >
              <Trash2 :size="14" />
            </button>
          </div>
        </div>

        <div v-if="expanded === entry.index" class="step-body">
          <label class="step-name">
            <span>Label</span>
            <input
              type="text"
              autocomplete="off"
              :placeholder="schemaFor(entry.step.type)?.label ?? entry.step.type"
              :value="entry.step.customName ?? ''"
              @input="rename(entry.index, ($event.target as HTMLInputElement).value)"
            />
          </label>

          <OperatorArgs
            :type="entry.step.type"
            :args="fromWireArgs(entry.step.type, entry.step.args)"
            @update:args="setArgs(entry.index, $event)"
          />
        </div>
      </li>
    </ol>

    <p v-else class="chain-empty">
      {{ emptyCopy ?? "No operations. Nodes are served exactly as the source provides them." }}
    </p>

    <!-- Every operator visible at once. A picker that has to be opened turns
         "what can this do" into a question you have to go and ask. -->
    <div class="add-block">
      <p class="add-label">Add an operation</p>
      <div class="add-grid">
        <button
          v-for="entry in addable"
          :key="entry.type"
          type="button"
          class="add-button"
          :title="schemaFor(entry.type)?.summary ?? entry.type"
          @click="add(entry.type)"
        >
          {{ entry.label }}
          <span v-if="entry.scripting" class="add-tag">JS</span>
        </button>
      </div>
      <p v-if="!addable.length" class="add-waiting">
        <template v-if="catalogState === 'loading'">Loading the operator catalogue…</template>
        <template v-else-if="catalogState === 'error'">
          The operator catalogue could not be read, so nothing can be added here.
        </template>
        <template v-else-if="chain === 'response'">
          No response-stage operator is available for this file type.
        </template>
        <template v-else>This bundle exposes no operators to add.</template>
      </p>
    </div>
  </section>
</template>

<style scoped>
.chain {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.chain-head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 12px;
}

.chain-head h3 {
  margin: 0;
  font-size: 15px;
  font-weight: 700;
}

.chain-count {
  font-size: 12px;
  color: var(--muted-foreground, #656d76);
}

.chain-list {
  display: flex;
  flex-direction: column;
  gap: 6px;
  margin: 0;
  padding: 0;
  list-style: none;
}

.chain-step {
  border: 1px solid var(--border, #d9dde2);
  border-radius: 10px;
  background: var(--background, #f7f8f9);
}

/* A disabled step stays legible — it is kept precisely so it can be read and
   switched back on, so fading it out would defeat the point. */
.chain-step.is-off {
  opacity: 0.6;
  border-style: dashed;
}

.step-bar {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 7px 10px;
}

.step-grip {
  color: var(--muted-foreground, #656d76);
  cursor: grab;
}

.step-title {
  display: flex;
  align-items: center;
  gap: 7px;
  flex: 1;
  padding: 4px 6px;
  border: 0;
  border-radius: 7px;
  background: transparent;
  color: inherit;
  font-size: 13px;
  font-weight: 600;
  text-align: left;
  cursor: pointer;
}

.step-caret {
  transition: transform 0.15s ease;
}

.step-caret.is-open {
  transform: rotate(90deg);
}

.step-actions {
  display: flex;
  align-items: center;
  gap: 4px;
}

.step-toggle {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  margin-right: 4px;
  font-size: 11.5px;
  color: var(--muted-foreground, #656d76);
  white-space: nowrap;
}

.step-icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: 26px;
  padding: 4px 6px;
  border: 1px solid var(--border, #d9dde2);
  border-radius: 7px;
  background: transparent;
  color: inherit;
  font-size: 12px;
  cursor: pointer;
}

.step-icon:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}

.step-icon.is-danger {
  color: #f87171;
}

.step-body {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 4px 12px 14px;
  border-top: 1px solid var(--border, #d9dde2);
}

.step-name {
  display: flex;
  flex-direction: column;
  gap: 6px;
  font-size: 12px;
  font-weight: 650;
  color: var(--foreground, #17191c);
}

.chain-empty {
  margin: 0;
  padding: 12px 14px;
  border: 1px dashed var(--border, #d9dde2);
  border-radius: 10px;
  font-size: 12.5px;
  color: var(--muted-foreground, #656d76);
}

.add-block {
  padding: 12px 14px 14px;
  border: 1px solid var(--border, #d9dde2);
  border-radius: 10px;
  background: var(--background, #f7f8f9);
}

.add-label {
  margin: 0 0 10px;
  font-size: 11px;
  font-weight: 700;
  letter-spacing: 0.1em;
  text-transform: uppercase;
  color: var(--muted-foreground, #656d76);
}

.add-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(148px, 1fr));
  gap: 7px;
}

.add-button {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  /* Fixed height so a two-word label does not make its whole row taller than
     the others — the grid reads as a set of equals or it reads as a mess. */
  min-height: 40px;
  padding: 6px 10px;
  border: 1px solid var(--border, #d9dde2);
  border-radius: 8px;
  background: var(--card, #fff);
  color: inherit;
  font-size: 12.5px;
  font-weight: 600;
  line-height: 1.25;
  text-align: center;
  cursor: pointer;
  transition: border-color 0.15s ease, color 0.15s ease;
}

.add-button:hover {
  border-color: var(--primary, #1769aa);
  color: var(--primary, #1769aa);
}

.add-tag {
  padding: 1px 5px;
  border-radius: 999px;
  background: color-mix(in srgb, var(--primary, #1769aa) 20%, transparent);
  color: var(--primary, #1769aa);
  font-size: 9px;
  font-weight: 700;
}

.add-waiting {
  margin: 0;
  font-size: 12px;
  color: var(--muted-foreground, #656d76);
}
</style>
