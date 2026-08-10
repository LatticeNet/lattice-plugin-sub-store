<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from "vue";
import {
  CircleAlert,
  LoaderCircle,
  LockKeyhole,
  Pencil,
  Play,
  Plus,
  Trash2,
} from "@lucide/vue";

import {
  BINDINGS,
  callMethod,
  CONVERT_TARGETS,
  type ConversionResult,
  type OperatorCatalogResponse,
  type OperatorInfo,
  RAW_INPUT_LIMIT_BYTES,
} from "../client";
import { useHost } from "../host";
import { usePipelines } from "../usePipelines";
import { safeErrorMessage } from "../subStoreModel";
import {
  validateOperatorsJson,
  validatePipelineId,
  validatePipelineName,
  validateRawInput,
} from "../pipelinesModel";
import EngineUnavailable from "../components/EngineUnavailable.vue";
import OutputPanel from "../components/OutputPanel.vue";

const host = useHost();
const pipes = usePipelines(host);

// ── editor (create / edit) ──────────────────────────────────────────────────
const editingId = ref<string | null>(null);
const formId = ref("");
const formName = ref("");
const formTarget = ref(CONVERT_TARGETS[0]?.id ?? "Clash");
const formOperators = ref("");
const idInput = ref<HTMLInputElement | null>(null);

const idState = computed(() => validatePipelineId(formId.value));
const nameState = computed(() => validatePipelineName(formName.value));
const operatorsState = computed(() => validateOperatorsJson(formOperators.value));
const formValid = computed(
  () => !!formTarget.value && !operatorsState.value.error && (editingId.value !== null || !!idState.value.value) && !nameState.value.error,
);

function resetForm(): void {
  editingId.value = null;
  formId.value = "";
  formName.value = "";
  formTarget.value = CONVERT_TARGETS[0]?.id ?? "Clash";
  formOperators.value = "";
}

async function savePipeline(): Promise<void> {
  if (!formValid.value) return;
  const id = editingId.value ?? idState.value.value;
  if (!id) return;
  const ok = await pipes.save({
    id,
    name: nameState.value.value,
    target: formTarget.value,
    operators: operatorsState.value.value,
  });
  if (ok) resetForm();
}

async function editPipeline(id: string): Promise<void> {
  const record = await pipes.get(id);
  if (!record) return;
  editingId.value = record.id;
  formId.value = record.id;
  formName.value = record.name === record.id ? "" : record.name;
  formTarget.value = record.target;
  formOperators.value = record.operators?.length ? JSON.stringify(record.operators, null, 2) : "";
  await host.resize();
}

// ── run drawer ──────────────────────────────────────────────────────────────
const runningId = ref<string | null>(null);
const runRaw = ref("");
const runResult = ref<{ id: string; result: ConversionResult }>();

const runRawState = computed(() => validateRawInput(runRaw.value));

function toggleRun(id: string): void {
  if (runningId.value === id) {
    runningId.value = null;
    return;
  }
  runningId.value = id;
  runRaw.value = "";
  runResult.value = undefined;
}

async function runPipeline(id: string): Promise<void> {
  if (!runRawState.value.value || runRawState.value.overLimit) return;
  const result = await pipes.run(id, runRawState.value.value);
  if (result) runResult.value = { id, result };
}

// ── delete ──────────────────────────────────────────────────────────────────
const confirmingDelete = ref<string | null>(null);

async function deletePipeline(id: string): Promise<void> {
  const ok = await pipes.remove(id);
  if (ok && confirmingDelete.value === id) confirmingDelete.value = null;
}

// ── empty state guidance ────────────────────────────────────────────────────

/** The editor is always on this screen above the list, so "create" is a
 *  pointer at it, not another form. */
async function focusEditor(): Promise<void> {
  idInput.value?.scrollIntoView({ behavior: "smooth", block: "center" });
  idInput.value?.focus();
}

/**
 * The pipeline editor takes operators as raw JSON, which asks the operator to
 * already know the vocabulary. The catalog is the answer to "what can I even
 * write here" — loaded on first ask, not on mount, because a screen that
 * manages pipelines does not always need it.
 */
const showCatalog = ref(false);
const catalog = ref<OperatorInfo[]>([]);
const catalogError = ref("");
const catalogLoading = ref(false);
const catalogPanel = ref<HTMLElement | null>(null);

async function browseOperators(): Promise<void> {
  showCatalog.value = !showCatalog.value;
  if (!showCatalog.value) return;
  if (!catalog.value.length && host.bridge && host.available(BINDINGS.subOperators)) {
    catalogLoading.value = true;
    catalogError.value = "";
    try {
      const response = await callMethod<OperatorCatalogResponse>(
        host.bridge,
        BINDINGS.subOperators,
        {},
      ).promise;
      catalog.value = response.operators ?? [];
    } catch (cause) {
      catalogError.value = safeErrorMessage(cause, "The operator catalog could not be loaded");
    } finally {
      catalogLoading.value = false;
    }
  }
  await nextTick();
  catalogPanel.value?.scrollIntoView({ behavior: "smooth", block: "nearest" });
}

// ── lifecycle ───────────────────────────────────────────────────────────────
onMounted(() => {
  void pipes.load();
});
watch(host.init, (value) => {
  if (value) void pipes.load();
});
</script>

<template>
  <section>
    <EngineUnavailable v-if="!pipes.available.value" feature="Conversion pipelines" />

    <template v-else>
      <section class="configuration" aria-labelledby="pipeline-editor-title">
        <div class="section-heading">
          <div>
            <h2 id="pipeline-editor-title">{{ editingId ? `Edit ${editingId}` : "New pipeline" }}</h2>
            <p>A pipeline is a saved conversion recipe: target format plus an optional operator chain.</p>
          </div>
          <button v-if="editingId" class="button button-secondary button-compact" type="button" @click="resetForm">
            Cancel edit
          </button>
        </div>

        <div class="form-grid">
          <label class="field">
            <span class="field-label">Id</span>
            <input ref="idInput" v-model="formId" type="text" autocomplete="off" spellcheck="false" placeholder="hk-daily" :disabled="!!editingId" />
            <small v-if="formId && idState.error" class="field-error">{{ idState.error }}</small>
          </label>
          <label class="field">
            <span class="field-label">Name <span class="field-optional">optional</span></span>
            <input v-model="formName" type="text" autocomplete="off" spellcheck="false" placeholder="HK daily" />
            <small v-if="formName && nameState.error" class="field-error">{{ nameState.error }}</small>
          </label>
          <label class="field field-wide">
            <span class="field-label">Target format</span>
            <select v-model="formTarget" class="select">
              <option v-for="target in CONVERT_TARGETS" :key="target.id" :value="target.id">
                {{ target.label }} ({{ target.produces }})
              </option>
            </select>
          </label>
          <label class="field field-wide">
            <span class="field-label">Operators <span class="field-optional">optional — JSON array</span></span>
            <textarea
              v-model="formOperators"
              class="code-area mono"
              rows="4"
              spellcheck="false"
              placeholder='[{"type":"quick-sort"}]'
            />
            <small v-if="formOperators && operatorsState.error" class="field-error">{{ operatorsState.error }}</small>
          </label>
        </div>

        <div class="form-actions">
          <button
            class="button button-primary"
            type="button"
            :disabled="!formValid || pipes.saving.value || !pipes.canMutate.value"
            @click="savePipeline"
          >
            <LoaderCircle v-if="pipes.saving.value" class="spin" :size="15" aria-hidden="true" />
            <Plus v-else :size="15" aria-hidden="true" />
            {{ pipes.saving.value ? "Saving" : editingId ? "Save changes" : "Create pipeline" }}
          </button>
          <span v-if="!pipes.canMutate.value" class="permission-note">
            <LockKeyhole :size="14" aria-hidden="true" />
            Administrator access required
          </span>
        </div>
      </section>

      <section class="configuration" aria-labelledby="pipelines-title">
        <div class="section-heading">
          <div>
            <h2 id="pipelines-title">Pipelines</h2>
            <p>{{ pipes.items.value.length ? `${pipes.items.value.length} saved` : "Saved conversion recipes" }}</p>
          </div>
          <button
            class="button button-secondary button-compact"
            type="button"
            :disabled="pipes.state.value === 'loading'"
            @click="pipes.load"
          >
            Reload
          </button>
        </div>

        <div v-if="pipes.state.value === 'loading'" class="sub-list" aria-label="Loading pipelines">
          <div v-for="row in 3" :key="row" class="skeleton-row" />
        </div>

        <div v-else-if="pipes.state.value === 'error'" class="alert" role="alert">
          <CircleAlert :size="17" aria-hidden="true" />
          <span>{{ pipes.loadError.value }}</span>
          <button class="button button-secondary button-compact" type="button" @click="pipes.load">Retry</button>
        </div>

        <div v-else-if="!pipes.items.value.length" class="panel-empty panel-empty-stack">
          <div class="panel-empty-copy">
            <h3>No pipelines yet</h3>
            <p>A pipeline is a saved conversion recipe: run it over raw subscription content whenever you need a fresh config.</p>
          </div>
          <div class="empty-actions">
            <button
              class="button button-primary"
              type="button"
              :disabled="!pipes.canMutate.value"
              @click="focusEditor"
            >
              <Plus :size="15" aria-hidden="true" /> Create a pipeline
            </button>
            <button class="button button-secondary" type="button" @click="browseOperators">
              {{ showCatalog ? "Hide the operator catalog" : "Browse the operators" }}
            </button>
          </div>
          <div v-if="showCatalog" ref="catalogPanel" class="empty-secondary">
            <p v-if="catalogLoading" class="row-popover-note">
              <LoaderCircle :size="13" class="spin" aria-hidden="true" /> Loading…
            </p>
            <p v-else-if="catalogError" class="row-popover-error" role="alert">{{ catalogError }}</p>
            <ul v-else class="row-popover-list">
              <li v-for="op in catalog" :key="op.type">
                <span class="mono">{{ op.type }}</span>
                <span v-if="op.summary" class="row-popover-note"> — {{ op.summary }}</span>
              </li>
            </ul>
          </div>
        </div>

        <ul v-else class="sub-list">
          <li v-for="item in pipes.items.value" :key="item.id" class="sub-card sub-card-column">
            <div class="sub-card-row">
              <div class="sub-card-main">
                <div class="sub-title">
                  <strong>{{ item.name }}</strong>
                  <span class="badge" data-tone="neutral">{{ item.id }}</span>
                </div>
                <div class="sub-meta">
                  <span class="badge" data-tone="info">{{ item.target }}</span>
                  <span v-if="item.operator_count" class="badge" data-tone="neutral">{{ item.operator_count }} operators</span>
                </div>
              </div>
              <div class="sub-actions">
                <template v-if="confirmingDelete === item.id">
                  <button
                    class="button button-secondary button-compact destructive"
                    type="button"
                    :disabled="pipes.busyId.value === item.id"
                    @click="deletePipeline(item.id)"
                  >
                    Confirm delete
                  </button>
                  <button class="button button-secondary button-compact" type="button" @click="confirmingDelete = null">
                    Cancel
                  </button>
                </template>
                <template v-else>
                  <button
                    class="button button-secondary button-compact"
                    type="button"
                    :disabled="!pipes.canRun.value"
                    @click="toggleRun(item.id)"
                  >
                    <Play :size="13" aria-hidden="true" />
                    {{ runningId === item.id ? "Close" : "Run" }}
                  </button>
                  <button
                    class="button button-secondary button-compact"
                    type="button"
                    :disabled="!pipes.canMutate.value"
                    @click="editPipeline(item.id)"
                  >
                    <Pencil :size="13" aria-hidden="true" />
                    Edit
                  </button>
                  <button
                    v-if="pipes.canMutate.value"
                    class="icon-button"
                    type="button"
                    :aria-label="`Delete ${item.id}`"
                    :title="`Delete ${item.id}`"
                    :disabled="!!pipes.busyId.value"
                    @click="confirmingDelete = item.id"
                  >
                    <Trash2 :size="15" aria-hidden="true" />
                  </button>
                </template>
              </div>
            </div>

            <div v-if="runningId === item.id" class="run-drawer">
              <label class="field">
                <span class="field-label">
                  Raw subscription content
                  <span class="field-optional">{{ runRawState.bytes }} bytes</span>
                </span>
                <textarea
                  v-model="runRaw"
                  class="code-area mono"
                  rows="5"
                  spellcheck="false"
                  placeholder="Paste the subscription body (share links, one per line)"
                />
                <small v-if="runRaw && runRawState.error" class="field-error">{{ runRawState.error }}</small>
                <small v-else-if="runRawState.overLimit" class="field-error">
                  Over the {{ RAW_INPUT_LIMIT_BYTES.toLocaleString() }}-byte input cap — trim the content
                </small>
              </label>
              <div class="form-actions">
                <button
                  class="button button-primary button-compact"
                  type="button"
                  :disabled="!runRawState.value || runRawState.overLimit || pipes.busyId.value === item.id"
                  @click="runPipeline(item.id)"
                >
                  <LoaderCircle v-if="pipes.busyId.value === item.id" class="spin" :size="13" aria-hidden="true" />
                  <Play v-else :size="13" aria-hidden="true" />
                  {{ pipes.busyId.value === item.id ? "Running" : `Run to ${item.target}` }}
                </button>
              </div>
              <template v-if="runResult && runResult.id === item.id">
                <div class="preview-summary">
                  <span><strong>{{ runResult.result.node_count }}</strong> nodes</span>
                  <span v-if="runResult.result.source_node_count !== runResult.result.node_count" class="badge" data-tone="neutral">
                    from {{ runResult.result.source_node_count }}
                  </span>
                  <span class="badge" data-tone="neutral">{{ runResult.result.output_bytes.toLocaleString() }} bytes</span>
                </div>
                <OutputPanel
                  :content="runResult.result.output"
                  :file-name="`${item.id}-${item.target.toLowerCase()}.txt`"
                  :note="`${runResult.result.output_bytes.toLocaleString()} bytes`"
                />
              </template>
            </div>
          </li>
        </ul>

        <div v-if="pipes.actionError.value" class="alert" role="alert">
          <CircleAlert :size="17" aria-hidden="true" />
          <span>{{ pipes.actionError.value }}</span>
        </div>
      </section>
    </template>
  </section>
</template>
