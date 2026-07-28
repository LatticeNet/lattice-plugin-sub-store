<script setup lang="ts">
import { computed, ref } from "vue";
import { ArrowLeftRight, CircleAlert, FileOutput, LoaderCircle } from "@lucide/vue";

import { CONVERT_TARGETS, RAW_INPUT_LIMIT_BYTES } from "../client";
import { useConvert } from "../useConvert";
import { useHost } from "../host";
import { validateOperatorsJson, validateRawInput } from "../pipelinesModel";
import EngineUnavailable from "../components/EngineUnavailable.vue";
import OutputPanel from "../components/OutputPanel.vue";

const host = useHost();
const convert = useConvert(host);

// ── form ────────────────────────────────────────────────────────────────────
const raw = ref("");
const targetId = ref(CONVERT_TARGETS[0]?.id ?? "Clash");
const operatorsJson = ref("");

const rawState = computed(() => validateRawInput(raw.value));
const operatorsState = computed(() => validateOperatorsJson(operatorsJson.value));
const canConvert = computed(
  () =>
    !!rawState.value.value &&
    !rawState.value.overLimit &&
    !operatorsState.value.error &&
    !!targetId.value &&
    !convert.producing.value &&
    convert.available.value,
);

async function produce(): Promise<void> {
  if (!canConvert.value || !rawState.value.value) return;
  await convert.produce(rawState.value.value, targetId.value, operatorsState.value.value);
}

const targetLabel = computed(
  () => CONVERT_TARGETS.find((target) => target.id === targetId.value)?.label ?? targetId.value,
);

const resultFileName = computed(() => {
  const ext = CONVERT_TARGETS.find((target) => target.id === targetId.value)?.produces ?? "txt";
  return `lattice-${targetId.value.toLowerCase()}.${ext === "text" ? "txt" : ext === "json" ? "json" : "yaml"}`;
});
</script>

<template>
  <section>
    <EngineUnavailable v-if="!convert.available.value" feature="Config conversion" />

    <template v-else>
      <section class="configuration" aria-labelledby="convert-input-title">
        <div class="section-heading">
          <div>
            <h2 id="convert-input-title">Convert subscription content</h2>
            <p>
              The embedded engine processes content you paste here — neither the frame nor the
              plugin fetches anything on its own.
            </p>
          </div>
        </div>

        <label class="field">
          <span class="field-label">
            Raw subscription content
            <span class="field-optional">{{ rawState.bytes.toLocaleString() }} bytes</span>
          </span>
          <textarea
            v-model="raw"
            class="code-area mono"
            rows="8"
            spellcheck="false"
            placeholder="Paste the subscription body (share links, one per line)"
          />
          <small v-if="raw && rawState.error" class="field-error">{{ rawState.error }}</small>
          <small v-else-if="rawState.overLimit" class="field-error">
            Over the {{ RAW_INPUT_LIMIT_BYTES.toLocaleString() }}-byte input cap — trim the content
          </small>
        </label>

        <div class="form-grid convert-grid">
          <label class="field">
            <span class="field-label">Target format</span>
            <select v-model="targetId" class="select">
              <option v-for="target in CONVERT_TARGETS" :key="target.id" :value="target.id">
                {{ target.label }} ({{ target.produces }})
              </option>
            </select>
          </label>
          <label class="field">
            <span class="field-label">Operators <span class="field-optional">optional — JSON array</span></span>
            <textarea
              v-model="operatorsJson"
              class="code-area mono"
              rows="3"
              spellcheck="false"
              placeholder='[{"type":"quick-sort"}]'
            />
            <small v-if="operatorsJson && operatorsState.error" class="field-error">{{ operatorsState.error }}</small>
          </label>
        </div>

        <div class="form-actions">
          <button class="button button-primary" type="button" :disabled="!canConvert" @click="produce">
            <LoaderCircle v-if="convert.producing.value" class="spin" :size="15" aria-hidden="true" />
            <FileOutput v-else :size="15" aria-hidden="true" />
            {{ convert.producing.value ? "Converting" : `Convert to ${targetLabel}` }}
          </button>
          <span class="vault-note">
            <ArrowLeftRight :size="12" aria-hidden="true" />
            Runs entirely inside the signed plugin artifact.
          </span>
        </div>

        <div v-if="convert.actionError.value" class="alert" role="alert">
          <CircleAlert :size="17" aria-hidden="true" />
          <span>{{ convert.actionError.value }}</span>
        </div>
      </section>

      <section v-if="convert.result.value" class="configuration" aria-labelledby="convert-result-title">
        <h2 id="convert-result-title" class="sr-only">Conversion result</h2>
        <div class="preview-summary">
          <span><strong>{{ convert.result.value.node_count }}</strong> nodes</span>
          <span
            v-if="convert.result.value.source_node_count !== convert.result.value.node_count"
            class="badge"
            data-tone="neutral"
          >
            from {{ convert.result.value.source_node_count }}
          </span>
          <span class="badge" :data-tone="convert.resultNearBudget.value ? 'warning' : 'neutral'">
            {{ convert.result.value.output_bytes.toLocaleString() }} bytes
          </span>
          <span v-if="convert.resultNearBudget.value" class="badge" data-tone="warning">
            near the engine's 6 MiB output limit — larger conversions fail loudly, never truncate
          </span>
        </div>
        <OutputPanel
          :content="convert.result.value.output"
          :file-name="resultFileName"
          :note="`${convert.result.value.output_bytes.toLocaleString()} bytes`"
        />
      </section>
    </template>
  </section>
</template>
