<script setup lang="ts">
import { computed, nextTick, ref, watch } from "vue";
import { LoaderCircle, Plus, RotateCw, Trash2 } from "@lucide/vue";
import { PcNotice, PcSkeleton } from "@latticenet/plugin-bridge/chassis";

import { ERROR_CODES } from "../client";
import {
  BIND_SHAPE_PROTOCOLS,
  MAX_SNAPSHOT_BYTES,
  STRUCTURED_FILTER_TYPE,
  effectiveGeo,
  hasBindShape,
  leadingRun,
  linesThatFit,
  readFilterArgs,
  readsProbe,
  responseTransformerSteps,
  rowLabel,
  shortVersion,
  withoutLeadingCode,
  type CatalogueRow,
  type FleetPreviewStep,
} from "../fleet";
import { formatBytes, formatCount, formatList, t } from "../i18n";
import { defaultArgs, toWireArgs } from "../operatorSchema";
import type { FleetEditor } from "../useRecordEditor";
import StructuredPredicateField from "./StructuredPredicateField.vue";

/**
 * The selection of a fleet record (s2-plan section 11): the Structured filter
 * steps at the head of the chain, each as a row of conditions, with what core
 * and the plugin made of it beside it. A step core evaluates over the
 * catalogue is marked "pushed to core"; one the plugin evaluates over the
 * lines core returned is marked "evaluated here", and either says how many
 * lines it removed. The proof line states the facts the selection rests on:
 * the catalogue version, its size, the fields core can evaluate, whether
 * probe data exists, and the snapshot the selection takes against core's
 * bound.
 *
 * The steps are the chain's own: this edits `process` in place, and the chain
 * under Operations shows the same steps. Nothing here holds a credential;
 * the catalogue's rows carry none.
 */
const props = defineProps<{
  fleet: FleetEditor;
  process: unknown[];
  readOnly: boolean;
  /** Nodes the latest preview built from this selection, when one ran. */
  previewNodes?: number | null;
}>();

const emit = defineEmits<{
  (e: "update:process", value: unknown[]): void;
  (e: "open-line", lineUUID: string): void;
  (e: "open-operations"): void;
  (e: "jump-identity"): void;
}>();

const fleet = props.fleet;

const run = computed(() => leadingRun(props.process));
const reply = computed(() => fleet.selection.value);
const catalogue = computed(() => fleet.catalogue.value);
const loading = computed(() => fleet.selectionState.value === "loading" || fleet.catalogueState.value === "loading");
const denied = computed(() =>
  fleet.selectionState.value === "denied" || fleet.catalogueState.value === "denied" || fleet.selectionState.value === "unavailable",
);
const failed = computed(() => (fleet.selectionState.value === "error" ? fleet.selectionError.value : null));

/** Steps after the selection: they run at render over the selected lines. */
const laterSteps = computed(() =>
  props.process.slice(run.value.length).filter((step) => !(step as { disabled?: boolean } | null)?.disabled).length,
);

/** What fleet_preview said about one chain index, for the step it belongs to. */
function stepReport(index: number): { pushed: boolean; entry: FleetPreviewStep | null } {
  const r = reply.value;
  if (!r) return { pushed: false, entry: null };
  const pushed = index < r.covered;
  const entry = r.steps.find((step) => step.index === index) ?? null;
  return { pushed, entry };
}

/** The count a step's line states: in → out for an evaluated step, core's answer for the pushed group. */
function stepCount(index: number): string {
  const { pushed, entry } = stepReport(index);
  if (!entry) return "";
  if (entry.in !== undefined) return t.fleetEditor.removed(entry.in, entry.out);
  const total = catalogue.value?.count;
  if (pushed && total !== undefined && total >= entry.out) return t.fleetEditor.removed(total, entry.out);
  return t.fleetEditor.coreKept(entry.out);
}

const proof = computed<string[]>(() => {
  const r = reply.value ?? catalogue.value;
  if (!r) return [];
  const segments = [t.fleetEditor.proof.catalogue(shortVersion(r.catalogue_version))];
  if (catalogue.value) segments.push(t.fleetEditor.proof.lines(catalogue.value.count));
  segments.push(t.fleetEditor.proof.pushable(r.selector_fields.length));
  if (fleet.probeMissing.value) segments.push(t.fleetEditor.proof.probeUnavailable);
  if (reply.value) {
    segments.push(t.fleetEditor.proof.selected(reply.value.count));
    segments.push(t.fleetEditor.proof.snapshot(formatBytes(reply.value.estimated_envelope_bytes), formatBytes(MAX_SNAPSHOT_BYTES)));
  }
  return segments;
});

const tooLarge = computed(() => {
  const r = reply.value;
  if (!r || r.estimated_envelope_bytes <= MAX_SNAPSHOT_BYTES) return null;
  return { size: formatBytes(r.estimated_envelope_bytes), bound: formatBytes(MAX_SNAPSHOT_BYTES), fit: linesThatFit(r) };
});

/** A selection step names a probe field, which save refuses while probe data is unavailable. */
const usesProbe = computed(() => run.value.filters.some((index) => readsProbe(readFilterArgs((props.process[index] as { args?: unknown }).args))));

const transformerSteps = computed(() => responseTransformerSteps(props.process).map((index) => t.fleetEditor.step(index + 1)));

/**
 * Selected lines core cannot bind (shadowsocks and any protocol without a
 * bind shape): render drops them. Exact when the sample is the whole
 * selection or a preview ran; otherwise what the sample shows.
 */
const dropped = computed(() => {
  const r = reply.value;
  if (!r) return null;
  const sample = r.sample ?? [];
  const missing = sample.filter((row) => !hasBindShape(row));
  const protocols = [...new Set(missing.map((row) => row.template?.protocol ?? row.protocol))];
  let count = missing.length;
  if (sample.length < r.count && typeof props.previewNodes === "number" && r.count > props.previewNodes) count = r.count - props.previewNodes;
  if (count <= 0) return null;
  return { count, protocols: formatList(protocols.length ? protocols : [t.fleetEditor.unknownGeo]) };
});

// ── steps ────────────────────────────────────────────────────────────────────

function setStepArgs(index: number, args: Record<string, unknown>): void {
  emit(
    "update:process",
    props.process.map((step, i) => (i === index ? { ...(step as object), args: toWireArgs(STRUCTURED_FILTER_TYPE, args) } : step)),
  );
}

function addStep(): void {
  const next = [...props.process];
  next.splice(run.value.length, 0, { type: STRUCTURED_FILTER_TYPE, args: toWireArgs(STRUCTURED_FILTER_TYPE, defaultArgs(STRUCTURED_FILTER_TYPE)) });
  emit("update:process", next);
}

/** The step last removed, kept until the next edit so a mistaken delete is one click back. */
const removed = ref<{ index: number; position: number; step: unknown } | null>(null);
const root = ref<HTMLElement | null>(null);

function removeStep(index: number, position: number): void {
  removed.value = { index, position, step: props.process[index] };
  emit("update:process", props.process.filter((_, i) => i !== index));
  // Never onto another delete button: the next step's heading, or the add
  // button when no step is left.
  void nextTick(() => {
    const names = root.value?.querySelectorAll<HTMLElement>("[data-fs-step-name]") ?? [];
    const next = names[Math.min(position, names.length - 1)];
    (next ?? root.value?.querySelector<HTMLElement>("[data-testid=fleet-add-step]"))?.focus();
  });
}

function undoRemove(): void {
  const last = removed.value;
  if (!last) return;
  const next = [...props.process];
  next.splice(last.index, 0, last.step);
  removed.value = null;
  emit("update:process", next);
  void nextTick(() => root.value?.querySelectorAll<HTMLElement>("[data-fs-step-name]")[last.position]?.focus());
}

watch(
  () => props.process,
  (next, previous) => {
    if (removed.value && previous && next.length >= previous.length) removed.value = null;
  },
);

// ── the sample ───────────────────────────────────────────────────────────────

const SAMPLE_FIRST = 8;
const showAll = ref(false);
const sample = computed<CatalogueRow[]>(() => reply.value?.sample ?? []);
const shownRows = computed(() => (showAll.value ? sample.value : sample.value.slice(0, SAMPLE_FIRST)));

function exitOf(row: CatalogueRow): string {
  const geo = effectiveGeo(row);
  if (!geo) return t.fleetEditor.unknownGeo;
  return [geo.country, geo.city].filter(Boolean).join(" ") || t.fleetEditor.unknownGeo;
}

function stateTone(state: string | undefined): string {
  if (state === "running") return "ok";
  if (state === "down") return "danger";
  return "warn";
}

const errorIsCatalogue = computed(() => failed.value?.code === ERROR_CODES.catalogueUnavailable || failed.value?.code === "");
</script>

<template>
  <section ref="root" class="fs" data-testid="fleet-selection" :aria-busy="loading ? 'true' : 'false'" aria-labelledby="fleet-selection-title">
    <header class="fs-head">
      <div class="fs-heading">
        <h3 id="fleet-selection-title" class="fs-title">{{ t.fleetEditor.selectionTitle }}</h3>
        <p class="fs-lead">{{ t.fleetEditor.selectionDescription }}</p>
      </div>
      <button
        v-if="!failed"
        type="button"
        class="button button-secondary button-compact"
        :disabled="loading || denied"
        data-testid="fleet-refresh"
        @click="fleet.retry()"
      >
        <LoaderCircle v-if="loading" :size="14" class="spin" aria-hidden="true" />
        <RotateCw v-else :size="14" aria-hidden="true" />
        {{ t.fleetEditor.reread }}
      </button>
    </header>

    <!-- The facts the selection rests on, in mono where they are identifiers. -->
    <p v-if="proof.length" class="fs-proof" data-testid="fleet-proof" aria-live="polite">
      <span v-for="(segment, index) in proof" :key="index" class="fs-proof-part">{{ segment }}</span>
      <button type="button" class="fs-jump" data-testid="fleet-jump-identity" @click="emit('jump-identity')">{{ t.fleetEditor.identity.title }}</button>
    </p>
    <PcSkeleton v-else-if="loading" variant="rows" :count="1" :label="t.fleetEditor.loading" />

    <!-- States, most binding first. -->
    <PcNotice v-if="denied" tone="info" :title="t.fleetEditor.denied.title" data-testid="fleet-denied">
      {{ t.fleetEditor.denied.body }}
    </PcNotice>
    <PcNotice v-else-if="failed" tone="danger" :title="t.fleetEditor.error.title" data-testid="fleet-error">
      <span class="fs-error-text">{{ withoutLeadingCode(failed.message) }}</span>
      <code v-if="failed.code && !errorIsCatalogue" class="fs-code">{{ failed.code }}</code>
      <template #actions>
        <button type="button" class="button button-secondary button-compact" @click="fleet.retry()">{{ t.fleetEditor.retry }}</button>
      </template>
    </PcNotice>
    <p v-if="readOnly" class="permission-note" data-testid="fleet-read-only">{{ t.fleetEditor.readOnly }}</p>
    <PcNotice v-if="fleet.stale.value" tone="info" data-testid="fleet-stale">
      {{ t.fleetEditor.stale(shortVersion(fleet.stale.value.served, 8, false), shortVersion(fleet.stale.value.now, 8, false)) }}
    </PcNotice>
    <PcNotice v-if="tooLarge" tone="danger" :title="t.fleetEditor.tooLarge.title" data-testid="fleet-too-large">
      {{ t.fleetEditor.tooLarge.body(tooLarge.size, tooLarge.bound, tooLarge.fit) }}
    </PcNotice>
    <PcNotice v-if="transformerSteps.length" tone="danger" :title="t.fleetEditor.transformer.title" data-testid="fleet-transformer">
      {{ t.fleetEditor.transformer.body(formatList(transformerSteps)) }}
      <code class="fs-code">{{ ERROR_CODES.fleetResponseChainUnavailable }}</code>
      <template #actions>
        <button type="button" class="button button-secondary button-compact" @click="emit('open-operations')">{{ t.fleetEditor.openOperations }}</button>
      </template>
    </PcNotice>
    <p v-if="fleet.unfinished.value" class="fs-unfinished" role="status" data-testid="fleet-unfinished">
      {{ t.fleetEditor.unfinished(fleet.unfinished.value.step, fleet.unfinished.value.condition) }}
    </p>
    <PcNotice v-if="fleet.probeMissing.value && usesProbe" tone="warning" data-testid="fleet-probe-flagged">
      {{ t.fleetEditor.probeFlagged }}
    </PcNotice>
    <PcNotice v-if="dropped" tone="warning" data-testid="fleet-dropped">
      {{ t.fleetEditor.dropped(dropped.count, dropped.protocols) }}
    </PcNotice>

    <!-- The steps. -->
    <ol v-if="run.filters.length" class="fs-steps">
      <li v-for="(index, position) in run.filters" :key="index" class="fs-step" data-testid="fleet-step">
        <div class="fs-step-bar">
          <span class="fs-step-name" tabindex="-1" data-fs-step-name>{{ t.fleetEditor.step(position + 1) }}</span>
          <span
            v-if="reply"
            :class="['fs-marker', stepReport(index).pushed ? 'is-pushed' : 'is-local']"
            :title="stepReport(index).pushed ? t.fleetEditor.pushedTitle : t.fleetEditor.localTitle"
            data-testid="fleet-step-marker"
          >{{ stepReport(index).pushed ? t.fleetEditor.pushed : t.fleetEditor.local }}</span>
          <span v-if="stepCount(index)" class="fs-count mono" data-testid="fleet-step-count">{{ stepCount(index) }}</span>
          <button
            v-if="!readOnly"
            type="button"
            class="fs-icon is-danger"
            :aria-label="t.fleetEditor.removeStep(position + 1)"
            :title="t.fleetEditor.removeStep(position + 1)"
            @click="removeStep(index, position)"
          >
            <Trash2 :size="14" aria-hidden="true" />
          </button>
        </div>
        <StructuredPredicateField
          :args="(process[index] as { args?: unknown }).args"
          :sample-rows="catalogue?.sample ?? sample"
          :probe-unavailable="fleet.probeMissing.value"
          :read-only="readOnly"
          :id-prefix="`fleet-step-${index}`"
          @update:args="setStepArgs(index, $event)"
        />
      </li>
    </ol>
    <p v-else class="fs-none" data-testid="fleet-no-steps">{{ t.fleetEditor.noSteps }}</p>
    <p v-if="removed" class="fs-removed" role="status" data-testid="fleet-step-removed">
      {{ t.fleetEditor.stepRemoved(removed.position + 1) }}
      <button type="button" class="button button-secondary button-compact" @click="undoRemove()">{{ t.fleetEditor.undo }}</button>
    </p>

    <button v-if="!readOnly" type="button" class="fs-add" data-testid="fleet-add-step" @click="addStep()">
      <Plus :size="14" aria-hidden="true" /> {{ t.fleetEditor.addStep }}
    </button>
    <p v-if="laterSteps" class="fs-later">{{ t.fleetEditor.laterSteps(laterSteps) }}</p>

    <!-- What the selection holds. -->
    <div v-if="reply && reply.count === 0 && !fleet.unfinished.value" class="fs-empty" data-testid="fleet-empty" role="status">
      <strong>{{ t.fleetEditor.empty.title }}</strong>
      <span>{{ t.fleetEditor.empty.body }}</span>
    </div>
    <div v-else-if="sample.length" class="fs-sample" data-testid="fleet-sample">
      <div class="fs-sample-head">
        <h4 class="fs-sample-title">{{ t.fleetEditor.sampleTitle }}</h4>
        <span class="fs-sample-count">{{ t.fleetEditor.sampleShown(shownRows.length, reply?.count ?? sample.length) }}</span>
      </div>
      <ul class="fs-rows" :aria-label="t.fleetEditor.sampleTitle">
        <li class="fs-row fs-row-head" aria-hidden="true">
          <span>{{ t.fleetEditor.sampleColumns.line }}</span>
          <span>{{ t.fleetEditor.sampleColumns.protocol }}</span>
          <span>{{ t.fleetEditor.sampleColumns.where }}</span>
          <span>{{ t.fleetEditor.sampleColumns.state }}</span>
        </li>
        <li v-for="row in shownRows" :key="row.line_uuid" class="fs-row" data-testid="fleet-sample-row">
          <button
            type="button"
            class="fs-row-open"
            :aria-label="t.fleetEditor.showLine(rowLabel(row))"
            @click="emit('open-line', row.line_uuid)"
          >
            <span class="fs-row-label">{{ rowLabel(row) }}</span>
          </button>
          <span class="fs-row-proto mono" :class="{ 'is-unbindable': !BIND_SHAPE_PROTOCOLS.includes(row.template?.protocol ?? row.protocol) }">
            {{ row.protocol }}<template v-if="row.transport">/{{ row.transport }}</template>
          </span>
          <span class="fs-row-where">{{ exitOf(row) }}</span>
          <span :class="['fs-row-state', `is-${stateTone(row.service_state)}`]">{{ row.service_state || t.fleetEditor.unknownGeo }}</span>
        </li>
      </ul>
      <button
        v-if="sample.length > SAMPLE_FIRST"
        type="button"
        class="button button-secondary button-compact fs-more"
        :aria-expanded="showAll"
        @click="showAll = !showAll"
      >
        {{ showAll ? t.fleetEditor.sampleFewer : t.fleetEditor.sampleAll(sample.length) }}
      </button>
    </div>
    <p v-if="reply && reply.count > 0" class="pc-sr-only" role="status">{{ formatCount(reply.count) }}</p>
  </section>
</template>

<style scoped>
.fs {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  min-width: 0;
}

.fs-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--space-3);
}

.fs-heading { min-width: 0; }

.fs-title {
  margin: 0;
  font-size: var(--text-body);
  font-weight: 650;
}

.fs-lead {
  margin: 2px 0 0;
  max-width: var(--lt-measure-prose);
  font-size: var(--lt-text-xs);
  line-height: var(--lt-leading);
  color: var(--muted-foreground);
}

/* One proof line: the facts, separated by a middle dot, in mono because each
   one is an identifier or a figure an operator matches against another view. */
.fs-proof {
  display: flex;
  flex-wrap: wrap;
  gap: 0 var(--space-2);
  margin: 0;
  font-family: var(--font-mono);
  font-size: var(--lt-text-xs);
  line-height: var(--lt-leading);
  color: var(--muted-foreground);
}

/* The separator ends a segment rather than starting the next, so a wrapped
   line never opens with a dot. */
.fs-proof-part:not(:last-of-type)::after {
  content: "\00a0\00b7";
}

.fs-jump {
  padding: 0;
  border: 0;
  background: transparent;
  color: var(--lt-accent-ink);
  font: inherit;
  text-decoration: underline;
  text-underline-offset: 2px;
  cursor: pointer;
}

.fs-jump:focus-visible { outline: none; box-shadow: var(--lt-focus-ring-tight); border-radius: var(--radius-sm); }

.fs-unfinished,
.fs-removed {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
  margin: 0;
  font-size: var(--lt-text-sm);
  line-height: var(--lt-leading);
  color: var(--muted-foreground);
}

.fs-code {
  margin-left: var(--space-1);
  font-family: var(--font-mono);
  font-size: var(--lt-text-xs);
}

.fs-error-text { overflow-wrap: anywhere; }

.fs-steps {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  margin: 0;
  padding: 0;
  list-style: none;
}

.fs-step {
  position: relative;
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding: var(--space-3);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  background: var(--card);
  min-width: 0;
}

.fs-step-bar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-1) var(--space-2);
  padding-right: 32px;
}

/* Pinned to the step's corner, so it never wraps onto a line of its own. */
.fs-step-bar .fs-icon {
  position: absolute;
  top: var(--space-2);
  right: var(--space-2);
}

.fs-step-name:focus-visible { outline: none; box-shadow: var(--lt-focus-ring-tight); border-radius: var(--radius-sm); }

.fs-step-name {
  font-size: var(--lt-text-sm);
  font-weight: 650;
}

.fs-marker {
  padding: 0 var(--space-2);
  border: 1px solid var(--border);
  border-radius: 999px;
  font-size: var(--lt-text-xs);
  line-height: 20px;
  white-space: nowrap;
}

.fs-marker.is-pushed {
  border-color: var(--lt-accent-border);
  background: var(--lt-accent-soft);
  color: var(--lt-accent-ink);
}

.fs-marker.is-local { color: var(--muted-foreground); }

.fs-count {
  margin-left: auto;
  font-size: var(--lt-text-xs);
  color: var(--foreground);
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}

.fs-icon {
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

.fs-icon:focus-visible { outline: none; box-shadow: var(--lt-focus-ring-tight); }
.fs-icon.is-danger { color: var(--lt-danger-ink); }
.fs-icon.is-danger:hover { background: var(--lt-danger-soft); }

.fs-none,
.fs-later {
  margin: 0;
  font-size: var(--lt-text-sm);
  line-height: var(--lt-leading);
  color: var(--muted-foreground);
}

.fs-none {
  padding: var(--space-3);
  border: 1px dashed var(--border);
  border-radius: var(--radius-md);
}

.fs-add {
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

.fs-add:hover { border-color: var(--primary); color: var(--lt-accent-ink); }
.fs-add:focus-visible { outline: none; box-shadow: var(--lt-focus-ring); }

.fs-empty {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: var(--space-3);
  border: 1px dashed var(--lt-warn-border);
  border-radius: var(--radius-md);
  background: var(--lt-warn-soft);
  color: var(--warning-text);
  font-size: var(--lt-text-sm);
  line-height: var(--lt-leading);
}

.fs-sample {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  min-width: 0;
}

.fs-sample-head {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  justify-content: space-between;
  gap: var(--space-2);
}

.fs-sample-title {
  margin: 0;
  font-size: var(--lt-text-sm);
  font-weight: 650;
}

.fs-sample-count {
  font-size: var(--lt-text-xs);
  color: var(--muted-foreground);
  font-variant-numeric: tabular-nums;
}

.fs-rows {
  margin: 0;
  padding: 0;
  list-style: none;
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  overflow: hidden;
}

.fs-row {
  display: grid;
  grid-template-columns: minmax(0, 2.4fr) minmax(0, 1fr) minmax(0, 1fr) minmax(0, 0.8fr);
  align-items: center;
  gap: var(--space-2);
  min-height: var(--row-h-compact, 32px);
  padding: 0 var(--space-2);
  font-size: var(--lt-text-sm);
}

.fs-row + .fs-row { border-top: 1px solid var(--border); }

.fs-row-head {
  min-height: 28px;
  background: var(--muted);
  color: var(--muted-foreground);
  font-size: var(--lt-text-xs);
  font-weight: 600;
}

.fs-row-open {
  min-width: 0;
  padding: 2px 0;
  border: 0;
  background: transparent;
  color: var(--foreground);
  text-align: left;
  font: inherit;
  cursor: pointer;
}

.fs-row-open:hover .fs-row-label { text-decoration: underline; }
.fs-row-open:focus-visible { outline: none; box-shadow: var(--lt-focus-ring-tight); border-radius: var(--radius-sm); }

.fs-row-label,
.fs-row-where,
.fs-row-proto {
  display: block;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.fs-row-proto { font-size: var(--lt-text-xs); }
.fs-row-proto.is-unbindable { color: var(--warning-text); }
.fs-row-where { color: var(--muted-foreground); font-size: var(--lt-text-xs); }

.fs-row-state {
  font-size: var(--lt-text-xs);
  white-space: nowrap;
}

.fs-row-state.is-ok { color: var(--success-text); }
.fs-row-state.is-warn { color: var(--warning-text); }
.fs-row-state.is-danger { color: var(--lt-danger-ink); }

.fs-more { align-self: flex-start; }

@media (max-width: 480px) {
  .fs-row {
    grid-template-columns: minmax(0, 1fr) 6.5rem;
    grid-template-areas: "label state" "proto where";
    row-gap: 0;
    padding: var(--space-1) var(--space-2);
  }

  .fs-row-head { display: none; }
  .fs-row-open { grid-area: label; }
  .fs-row-state { grid-area: state; text-align: right; }
  .fs-row-proto { grid-area: proto; }
  .fs-row-where { grid-area: where; text-align: right; }
  .fs-count { margin-left: 0; }
}
</style>
