<script setup lang="ts">
import { computed, nextTick, ref, watch } from "vue";
import { ArrowDown, ArrowUp, ChevronRight, Copy, Eye, GripVertical, LoaderCircle, Trash2 } from "@lucide/vue";

import type { CatalogueRow } from "../fleet";
import { t } from "../i18n";
import { defaultArgs, fromWireArgs, isFleetStep, schemaFor, toWireArgs } from "../operatorSchema";
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
 * Steps round-trip whole, so fields this editor does not understand. An `id`
 * from an import, whatever upstream adds next, survive an edit.
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
  catalog: readonly { type: string; scripting?: boolean; response?: boolean; fleet?: boolean }[];
  /**
   * The record is fleet-bound (design 28 S2): a fleet sub, or a combination
   * that gathers one. Only then does the palette offer the Lattice-only steps,
   * whose predicates read a fleet line's block and are unknown on any other
   * node; and a script step offers no link mode.
   */
  fleetBound?: boolean;
  /** Catalogue rows for a Structured Filter's typeahead. */
  sampleRows?: readonly CatalogueRow[];
  /** The catalogue lists probe as unavailable. */
  probeUnavailable?: boolean;
  /**
   * Types edited by the common-settings block above. They stay in the chain,
   * they are ordinary operators, but listing them here too would give one
   * setting two controls that can disagree.
   */
  managedTypes?: readonly string[];
  /**
   * Restricts the palette to the operators that make sense here. A plain-text
   * file runs its chain over the document, where a region filter or a rename
   * has nothing to act on, offering them invites a chain that cannot work.
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
  /**
   * A step to open, scroll to and focus, by its index in `steps`, with a
   * ticket so the same step can be asked for twice (a refusal's "Open
   * step").
   */
  openStep?: { index: number; ticket: number } | null;
  /** The step a fleet render's script refusal names (S2-SR-2): marked on its bar. */
  flaggedStep?: number | null;
  /**
   * The flagged step is proven to be the one that asked; otherwise it is
   * the first step that can ask, which is all core can say, and the flag
   * says that instead.
   */
  flaggedCertain?: boolean;
}>();

const emit = defineEmits<{
  (e: "update:steps", value: ChainStep[]): void;
  (e: "preview-step", index: number, label: string): void;
}>();

const expanded = ref<number | null>(null);
const dragIndex = ref<number | null>(null);
const root = ref<HTMLElement | null>(null);

watch(
  () => props.openStep?.ticket,
  async () => {
    const index = props.openStep?.index;
    if (index === undefined || index < 0 || index >= props.steps.length) return;
    expanded.value = index;
    await nextTick();
    const title = root.value?.querySelector<HTMLElement>(`[data-step-index="${index}"] .step-title`);
    title?.scrollIntoView({ block: "center" });
    title?.focus({ preventScroll: true });
  },
  { immediate: true },
);

const managed = computed(() => new Set(props.managedTypes ?? []));

/** Index pairs so the visible list can act on the real array positions. */
const visible = computed(() =>
  props.steps
    .map((step, index) => ({ step, index }))
    .filter((entry) => !managed.value.has(entry.step.type)),
);

/** Every operator, in one flat grid. One click to add, nothing hidden. */
const wantsResponse = computed(() => props.chain === "response");

const addable = computed(() =>
  props.catalog
    .filter((entry) => !managed.value.has(entry.type))
    .filter((entry) => Boolean(entry.response) === wantsResponse.value)
    .filter((entry) => props.fleetBound || !(entry.fleet || isFleetStep(entry.type)))
    .map((entry) => ({
      type: entry.type,
      label: schemaFor(entry.type)?.label ?? entry.type,
      scripting: Boolean(entry.scripting),
      fleet: Boolean(entry.fleet || isFleetStep(entry.type)),
    })),
);

/**
 * A step's bar: its position among the stored steps and its name. The
 * position counts every stored step, disabled and managed ones included,
 * as core's plan lines and refusals number them ("Step 2 added", "step 2
 * (Script Operator)"), so a bar can be matched to the line that names it.
 * A custom name keeps the number: without it a renamed step could not be
 * told apart from the line about it (the lane 6c design review).
 */
function label(step: ChainStep, position: number): string {
  const name = step.customName?.trim();
  return t.chain.stepLabel(position, name || (schemaFor(step.type)?.label ?? step.type));
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
 * two visible ones, ordinary in an imported chain, "move down" swapped a step
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
  <section ref="root" class="chain">
    <div class="chain-head">
      <h3>{{ heading ?? t.chain.heading }}</h3>
      <span v-if="visible.length" class="chain-count">{{ t.chain.activeCount(activeCount, visible.length) }}</span>
    </div>

    <ol v-if="visible.length" class="chain-list">
      <li
        v-for="(entry, position) in visible"
        :key="entry.index"
        :class="['chain-step', { 'is-off': entry.step.disabled, 'is-flagged': flaggedStep === entry.index }]"
        :data-step-index="entry.index"
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
            :aria-expanded="expanded === entry.index"
            :title="label(entry.step, entry.index + 1)"
            @click="expanded = expanded === entry.index ? null : entry.index"
          >
            <ChevronRight
              :size="14"
              aria-hidden="true"
              :class="['step-caret', { 'is-open': expanded === entry.index }]"
            />
            <span class="step-label">{{ label(entry.step, entry.index + 1) }}</span>
            <span v-if="isFleetStep(entry.step.type)" class="step-fleet" :title="t.fleetEditor.latticeOnlyTitle">{{ t.fleetEditor.latticeOnly }}</span>
            <span v-if="entry.step.disabled" class="badge" data-tone="warning">{{ t.chain.off }}</span>
            <span
              v-if="flaggedStep === entry.index"
              class="badge step-flag"
              data-tone="danger"
              data-testid="step-network-flag"
              :data-certain="flaggedCertain ? 'true' : 'false'"
              :title="flaggedCertain ? t.plans.scriptNetwork.flagTitle : t.plans.scriptNetwork.flagCandidateTitle"
            >{{ flaggedCertain ? t.plans.scriptNetwork.flag : t.plans.scriptNetwork.flagCandidate }}</span>
          </button>

          <div class="step-actions">
            <label class="step-toggle" :title="entry.step.disabled ? t.chain.turnOn : t.chain.turnOff">
              <input
                type="checkbox"
                :checked="!entry.step.disabled"
                :aria-label="t.chain.enable(label(entry.step, entry.index + 1))"
                @change="toggleDisabled(entry.index)"
              />
              <span>{{ t.chain.enabled }}</span>
            </label>
            <button
              type="button"
              class="step-icon"
              :disabled="position === 0"
              :title="t.chain.moveUp"
              :aria-label="t.chain.moveUpLabel(label(entry.step, entry.index + 1))"
              @click="moveVisible(entry.index, -1)"
            >
              <ArrowUp :size="14" aria-hidden="true" />
            </button>
            <button
              type="button"
              class="step-icon"
              :disabled="position === visible.length - 1"
              :title="t.chain.moveDown"
              :aria-label="t.chain.moveDownLabel(label(entry.step, entry.index + 1))"
              @click="moveVisible(entry.index, 1)"
            >
              <ArrowDown :size="14" aria-hidden="true" />
            </button>
            <button
              v-if="canPreviewStep"
              type="button"
              class="step-icon"
              :disabled="entry.step.disabled || previewingStep !== null"
              :title="t.chain.previewStep"
              :aria-label="t.chain.previewStepLabel(entry.index + 1)"
              @click="emit('preview-step', entry.index, label(entry.step, entry.index + 1))"
            >
              <LoaderCircle v-if="previewingStep === entry.index" :size="14" class="spin" aria-hidden="true" />
              <Eye v-else :size="14" aria-hidden="true" />
            </button>
            <button
              type="button"
              class="step-icon"
              :title="t.chain.duplicate"
              :aria-label="t.chain.duplicateLabel(label(entry.step, entry.index + 1))"
              @click="duplicate(entry.index)"
            >
              <Copy :size="14" aria-hidden="true" />
            </button>
            <button
              type="button"
              class="step-icon is-danger"
              :title="t.chain.remove"
              :aria-label="t.chain.removeLabel(label(entry.step, entry.index + 1))"
              @click="remove(entry.index)"
            >
              <Trash2 :size="14" aria-hidden="true" />
            </button>
          </div>
        </div>

        <div v-if="expanded === entry.index" class="step-body">
          <label class="step-name">
            <span>{{ t.chain.label }}</span>
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
            :fleet-bound="fleetBound"
            :sample-rows="sampleRows"
            :probe-unavailable="probeUnavailable"
            :id-prefix="`chain-step-${entry.index}`"
            @update:args="setArgs(entry.index, $event)"
          />
        </div>
      </li>
    </ol>

    <p v-else class="chain-empty">
      {{ emptyCopy ?? t.chain.empty }}
    </p>

    <!-- Every operator visible at once. A picker that has to be opened turns
         "what can this do" into a question you have to go and ask. -->
    <div class="add-block">
      <p class="add-label">{{ t.chain.add }}</p>
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
          <span v-if="entry.fleet" class="add-tag add-tag-fleet" :title="t.fleetEditor.latticeOnlyTitle">{{ t.fleetEditor.latticeOnly }}</span>
        </button>
      </div>
      <p v-if="!addable.length" class="add-waiting">
        <template v-if="catalogState === 'loading'">{{ t.chain.catalogLoading }}</template>
        <template v-else-if="catalogState === 'error'">{{ t.chain.catalogFailed }}</template>
        <template v-else-if="chain === 'response'">{{ t.chain.noResponseOperator }}</template>
        <template v-else>{{ t.chain.noOperators }}</template>
      </p>
    </div>
  </section>
</template>

<style scoped>
/* The chain is the signature interaction of this plugin, so it gets the same
   card language as the editor fieldsets around it rather than a second one. */

.chain {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}

.chain-head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: var(--space-3);
}

.chain-head h3 {
  margin: 0;
  font-size: var(--lt-text-lg);
  font-weight: 650;
}

.chain-count {
  font-size: var(--lt-text-sm);
  color: var(--muted-foreground);
  font-variant-numeric: tabular-nums;
}

.chain-list {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  margin: 0;
  padding: 0;
  list-style: none;
}

.chain-step {
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  background: var(--card);
}

/* A disabled step stays legible. It is kept precisely so it can be read and
   switched back on, so fading it out would defeat the point. The dashed edge
   and the "off" badge carry the state instead. */
.chain-step.is-off {
  border-style: dashed;
  background: var(--background);
}

.step-bar {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-1) var(--space-2);
}

.step-grip {
  display: inline-flex;
  color: var(--muted-foreground);
  cursor: grab;
}

.step-title {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  flex: 1;
  min-width: 0;
  padding: var(--space-1) var(--space-1);
  border: 0;
  border-radius: var(--radius-sm);
  background: transparent;
  color: inherit;
  font-size: var(--text-body);
  font-weight: 600;
  text-align: left;
}

.step-title:hover { background: var(--muted); }
.step-title:focus-visible { outline: none; box-shadow: var(--lt-focus-ring); }

.step-label {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.step-caret {
  flex: none;
  transition: transform var(--lt-dur) var(--lt-ease);
}

.step-caret.is-open {
  transform: rotate(90deg);
}

.step-actions {
  display: flex;
  align-items: center;
  gap: 2px;
  flex: none;
}

.step-toggle {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  margin-right: var(--space-1);
  font-size: var(--lt-text-xs);
  color: var(--muted-foreground);
  white-space: nowrap;
  cursor: pointer;
}

.step-icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 26px;
  height: 26px;
  border: 0;
  border-radius: var(--radius-sm);
  background: transparent;
  color: var(--muted-foreground);
}

.step-icon:hover:not(:disabled) { background: var(--muted); color: var(--foreground); }
.step-icon:focus-visible { outline: none; box-shadow: var(--lt-focus-ring-tight); }
.step-icon:disabled { opacity: 0.35; }

/* Was a literal #f87171, which is a light-theme red pinned into a frame that
   inherits the console's palette. */
.step-icon.is-danger { color: var(--lt-danger-ink); }
.step-icon.is-danger:hover:not(:disabled) { background: var(--lt-danger-soft); }

/* On a phone the step's name takes the bar's first line and its controls a
   second line under it, still at the right edge. Beside the toggle's word and
   five buttons the name had room for its number alone ("1. ..."), and with
   the word hidden it still had about 70px, short of "1. Regex filter" in
   English and further short in Russian. */
@media (max-width: 480px) {
  .step-bar {
    display: grid;
    grid-template-columns: auto minmax(0, 1fr);
    row-gap: 0;
  }

  .step-actions {
    grid-column: 2;
    justify-self: end;
  }
}

.step-body {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  padding: var(--space-1) var(--space-3) var(--space-3);
  border-top: 1px solid var(--border);
}

.step-name {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  max-width: var(--lt-measure-field);
  font-size: var(--lt-text-sm);
  font-weight: 600;
  color: var(--foreground);
}

.step-name input {
  min-height: 32px;
  padding: var(--space-1) var(--space-2);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  background: var(--background);
  color: var(--foreground);
  font-weight: 400;
  outline: none;
}
.step-name input:focus-visible { box-shadow: var(--lt-focus-ring-tight); border-color: var(--primary); }

.chain-empty {
  margin: 0;
  padding: var(--space-3);
  border: 1px dashed var(--border);
  border-radius: var(--radius-md);
  font-size: var(--lt-text-sm);
  color: var(--muted-foreground);
}

.add-block {
  padding: var(--space-3);
  /* Inset in the editor panel: a surface step, not a second border. */
  border: 0;
  border-radius: var(--radius-lg);
  background: var(--muted);
}

.add-label {
  margin: 0 0 var(--space-2);
  font-size: var(--lt-text-xs);
  font-weight: 700;
  letter-spacing: 0.08em;
  text-transform: uppercase;
  color: var(--muted-foreground);
}

.add-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(160px, 1fr));
  gap: var(--space-1);
}

.add-button {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: var(--space-1);
  /* Fixed height so a two-word label does not make its whole row taller than
     the others. The grid reads as a set of equals or it reads as a mess. */
  min-height: 38px;
  padding: var(--space-1) var(--space-2);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  background: var(--card);
  color: inherit;
  font-size: var(--lt-text-sm);
  font-weight: 600;
  line-height: 1.25;
  text-align: center;
  transition: border-color var(--lt-dur) var(--lt-ease), color var(--lt-dur) var(--lt-ease);
}

.add-button:hover {
  border-color: var(--primary);
  color: var(--lt-accent-ink);
}

.add-button:focus-visible { outline: none; box-shadow: var(--lt-focus-ring); }

.add-tag {
  padding: 0 var(--space-1);
  border-radius: 999px;
  background: var(--lt-accent-soft);
  color: var(--lt-accent-ink);
  font-size: 9px;
  font-weight: 700;
}

/* "Lattice only": the step reads a fleet line's block, so it is offered on
   fleet-bound records alone. Neutral ink, not the accent: it is a fact about
   the step, not a call to act. */
.add-tag-fleet {
  border: 1px solid var(--border);
  background: var(--card);
  color: var(--muted-foreground);
  font-weight: 600;
  white-space: nowrap;
}

.chain-step.is-flagged {
  border-color: var(--destructive);
}
.step-flag {
  flex: none;
  white-space: nowrap;
}
.step-fleet {
  flex: none;
  padding: 0 var(--space-1);
  border: 1px solid var(--border);
  border-radius: 999px;
  color: var(--muted-foreground);
  font-size: var(--lt-text-xs);
  font-weight: 500;
  white-space: nowrap;
}

.add-waiting {
  margin: 0;
  font-size: var(--lt-text-sm);
  color: var(--muted-foreground);
}
</style>
