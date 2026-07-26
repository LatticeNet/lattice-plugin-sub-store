<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue";
import {
  CheckCircle2,
  CircleAlert,
  ClipboardCopy,
  FileOutput,
  LoaderCircle,
  Rss,
  ScanSearch,
} from "@lucide/vue";

import { useConvert } from "../useConvert";
import { useHost } from "../host";
import { useSubscriptions } from "../useSubscriptions";
import EngineUnavailable from "../components/EngineUnavailable.vue";

const host = useHost();
const subs = useSubscriptions(host);
const convert = useConvert(host);

const engineReady = computed(() => convert.available.value && subs.available.value);

onMounted(() => {
  void subs.load();
  void convert.loadTargets();
});
watch(host.init, (value) => {
  if (value) {
    void subs.load();
    void convert.loadTargets();
  }
});

// ── output panel ────────────────────────────────────────────────────────────
const outputArea = ref<HTMLTextAreaElement>();
const copyNote = ref("");

async function copyOutput(): Promise<void> {
  const text = convert.output.value?.content ?? "";
  if (!text) return;
  copyNote.value = "";
  try {
    await navigator.clipboard.writeText(text);
    copyNote.value = "Copied";
  } catch {
    // The sandboxed frame usually has no clipboard permission: select instead.
    selectOutput();
    copyNote.value = "Clipboard blocked by the sandbox — text selected, copy it manually";
  }
}

function selectOutput(): void {
  outputArea.value?.focus();
  outputArea.value?.select();
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KiB`;
  return `${(bytes / (1024 * 1024)).toFixed(2)} MiB`;
}

const targetLabel = computed(
  () => convert.targets.value.find((target) => target.id === convert.targetId.value)?.label ?? convert.targetId.value,
);
</script>

<template>
  <section>
    <EngineUnavailable v-if="!engineReady" feature="Config conversion" />

    <template v-else>
      <section class="configuration" aria-labelledby="convert-sources-title">
        <div class="section-heading">
          <div>
            <h2 id="convert-sources-title">Sources</h2>
            <p>Subscriptions to merge into the output</p>
          </div>
        </div>

        <div v-if="subs.state.value === 'loading'" class="sub-list" aria-label="Loading subscriptions">
          <div v-for="row in 2" :key="row" class="skeleton-row" />
        </div>
        <div v-else-if="subs.state.value === 'error'" class="alert" role="alert">
          <CircleAlert :size="17" aria-hidden="true" />
          <span>{{ subs.loadError.value }}</span>
          <button class="button button-secondary button-compact" type="button" @click="subs.load">Retry</button>
        </div>
        <div v-else-if="!subs.items.value.length" class="panel-empty">
          <Rss :size="22" aria-hidden="true" />
          <div class="panel-empty-copy">
            <h3>No sources to convert</h3>
            <p>Add a subscription in the Subscriptions tab first, then select it here.</p>
          </div>
        </div>
        <ul v-else class="source-list">
          <li v-for="item in subs.items.value" :key="item.name">
            <label class="source-row" :data-checked="convert.selected.value.includes(item.name)">
              <input
                type="checkbox"
                :checked="convert.selected.value.includes(item.name)"
                @change="convert.toggle(item.name)"
              />
              <span class="source-name">{{ item.display_name || item.name }}</span>
              <span v-if="item.node_count !== undefined" class="badge" data-tone="neutral">{{ item.node_count }} nodes</span>
              <span v-if="item.last_error" class="badge" data-tone="warning">stale</span>
            </label>
          </li>
        </ul>
      </section>

      <section class="configuration" aria-labelledby="convert-target-title">
        <div class="section-heading">
          <div>
            <h2 id="convert-target-title">Target format</h2>
            <p>Produced by the embedded engine — no external service involved</p>
          </div>
        </div>

        <div v-if="convert.targetsState.value === 'loading'" class="target-grid">
          <div v-for="row in 3" :key="row" class="skeleton-row skeleton-chip" />
        </div>
        <div v-else-if="convert.targetsState.value === 'error'" class="alert" role="alert">
          <CircleAlert :size="17" aria-hidden="true" />
          <span>{{ convert.targetsError.value }}</span>
          <button class="button button-secondary button-compact" type="button" @click="convert.loadTargets">Retry</button>
        </div>
        <div v-else class="target-grid" role="radiogroup" aria-label="Target format">
          <label
            v-for="target in convert.targets.value"
            :key="target.id"
            class="target-card"
            :data-checked="convert.targetId.value === target.id"
          >
            <input v-model="convert.targetId.value" type="radio" name="convert-target" :value="target.id" />
            <span class="target-label">{{ target.label }}</span>
            <span class="badge" data-tone="neutral">{{ target.produces }}</span>
          </label>
        </div>

        <div class="form-actions">
          <button
            class="button button-secondary"
            type="button"
            :disabled="!convert.canConvert.value"
            @click="convert.runPreview"
          >
            <LoaderCircle v-if="convert.previewing.value" class="spin" :size="15" aria-hidden="true" />
            <ScanSearch v-else :size="15" aria-hidden="true" />
            {{ convert.previewing.value ? "Previewing" : "Preview" }}
          </button>
          <button
            class="button button-primary"
            type="button"
            :disabled="!convert.canConvert.value"
            @click="convert.produce"
          >
            <LoaderCircle v-if="convert.producing.value" class="spin" :size="15" aria-hidden="true" />
            <FileOutput v-else :size="15" aria-hidden="true" />
            {{ convert.producing.value ? "Converting" : `Convert to ${targetLabel}` }}
          </button>
        </div>

        <div v-if="convert.preview.value" class="preview-panel" aria-live="polite">
          <div class="preview-summary">
            <span><strong>{{ convert.preview.value.node_count }}</strong> nodes</span>
            <span v-for="group in convert.preview.value.groups" :key="group" class="badge" data-tone="info">{{ group }}</span>
            <span class="badge" :data-tone="convert.previewOverBudget.value ? 'warning' : 'neutral'">
              ≈ {{ formatBytes(convert.preview.value.size_estimate_bytes) }}
            </span>
            <span v-for="warning in convert.preview.value.warnings" :key="warning" class="badge" data-tone="warning">{{ warning }}</span>
          </div>
          <p v-if="convert.previewOverBudget.value" class="vault-note">
            Estimate crosses the host's per-call output limit — the conversion may arrive truncated.
            Convert fewer subscriptions at a time, or check the plugin's release notes for raised budgets.
          </p>
        </div>

        <div v-if="convert.actionError.value" class="alert" role="alert">
          <CircleAlert :size="17" aria-hidden="true" />
          <span>{{ convert.actionError.value }}</span>
        </div>
      </section>

      <section v-if="convert.output.value" class="configuration" aria-labelledby="convert-output-title">
        <div class="section-heading">
          <div>
            <h2 id="convert-output-title">Output</h2>
            <p>
              <span class="mono">{{ convert.output.value.file_name }}</span>
              · {{ convert.output.value.content_type }} · {{ formatBytes(convert.output.value.size_bytes) }}
            </p>
          </div>
          <div class="heading-actions">
            <span v-if="copyNote" class="vault-note" aria-live="polite">
              <CheckCircle2 v-if="copyNote === 'Copied'" :size="12" aria-hidden="true" /> {{ copyNote }}
            </span>
            <button class="button button-secondary button-compact" type="button" @click="copyOutput">
              <ClipboardCopy :size="13" aria-hidden="true" />
              Copy
            </button>
          </div>
        </div>
        <textarea
          ref="outputArea"
          class="output-area mono"
          :value="convert.output.value.content"
          readonly
          rows="14"
          spellcheck="false"
          aria-label="Converted configuration"
          @focus="selectOutput"
        />
        <p class="vault-note">
          The sandboxed frame cannot offer downloads — copy the text and save it as
          <span class="mono">{{ convert.output.value.file_name }}</span> where your client expects it.
        </p>
      </section>
    </template>
  </section>
</template>
