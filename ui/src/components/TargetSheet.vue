<script setup lang="ts">
/**
 * TargetSheet — "preview / copy this subscription for a client".
 *
 * This is the daily path Sub-Store itself puts one click away: open a record,
 * see every client it can produce, and take either the node list (preview) or
 * the actual document (copy) for the one you use. It replaces the invented
 * "Convert" tab, which asked the operator to paste raw text and an operator
 * JSON blob into a scratchpad — something no Sub-Store user has ever had to do
 * to get a config into their client.
 *
 * Honest capability split, mirroring the backend rather than the wish:
 *  - every target can be PREVIEWED (preview takes the engine target directly);
 *  - a target is COPYABLE only when the core can select it from a UA class
 *    (uaClassTargets), because render picks the client that way. A record that
 *    pins its own `target` always renders as that target, so the sheet says so
 *    instead of implying the choice still applies.
 */
import { computed, ref, watch } from "vue";
import { Check, Copy, Eye, LoaderCircle, X } from "@lucide/vue";

import {
  BINDINGS,
  CONVERT_TARGETS,
  callMethod,
  type ConvertTarget,
  type SubscriptionPreviewResponse,
  type SubscriptionRenderResponse,
} from "../client";
import { useHost } from "../host";
import { safeErrorMessage } from "../subStoreModel";

const props = defineProps<{
  open: boolean;
  /** The record being inspected; null closes the sheet. */
  recordId: string;
  recordName: string;
  /** A record that pins its own target ignores the client choice on render. */
  pinnedTarget?: string;
}>();

const emit = defineEmits<{ (e: "close"): void }>();

const host = useHost();

const busyTarget = ref("");
const copiedTarget = ref("");
const error = ref("");
const preview = ref<{ target: string; response: SubscriptionPreviewResponse } | null>(null);
const rendered = ref<{ target: string; content: string; contentType: string } | null>(null);

watch(
  () => props.open,
  (open) => {
    if (!open) return;
    error.value = "";
    preview.value = null;
    rendered.value = null;
    copiedTarget.value = "";
  },
);

const pinned = computed(() => (props.pinnedTarget ?? "").trim());

function renderable(target: ConvertTarget): boolean {
  // A pinned record renders as its own target whatever the client asks for.
  if (pinned.value) return target.id === pinned.value;
  return !!target.uaClass;
}

async function runPreview(target: ConvertTarget): Promise<void> {
  if (!host.bridge || busyTarget.value) return;
  busyTarget.value = `preview:${target.id}`;
  error.value = "";
  rendered.value = null;
  try {
    const response = await callMethod<SubscriptionPreviewResponse>(host.bridge, BINDINGS.subPreview, {
      subscription_id: props.recordId,
      target: target.id,
    }).promise;
    preview.value = { target: target.id, response };
  } catch (cause) {
    preview.value = null;
    error.value = safeErrorMessage(cause, `Preview for ${target.label} failed`);
  } finally {
    busyTarget.value = "";
    await host.resize();
  }
}

async function copyDocument(target: ConvertTarget): Promise<void> {
  if (!host.bridge || busyTarget.value) return;
  busyTarget.value = `copy:${target.id}`;
  error.value = "";
  try {
    const response = await callMethod<SubscriptionRenderResponse>(host.bridge, BINDINGS.subRender, {
      subscription_id: props.recordId,
      format: "plain",
      ua_class: pinned.value ? "" : (target.uaClass ?? ""),
    }).promise;
    const content = response?.content ?? "";
    if (!content) throw new Error("The render returned no document");
    // The document is shown either way. A clipboard write can fail for
    // reasons that have nothing to do with the configuration — an unfocused
    // document, a denied permission — and losing a good render to that would
    // make the operator run it again to get back what is already in hand.
    rendered.value = { target: target.id, content, contentType: response.content_type };
    preview.value = null;
    try {
      await navigator.clipboard.writeText(content);
      copiedTarget.value = target.id;
      window.setTimeout(() => {
        if (copiedTarget.value === target.id) copiedTarget.value = "";
      }, 2000);
    } catch {
      error.value = "The clipboard is unavailable — the configuration is below, select it to copy.";
    }
  } catch (cause) {
    error.value = safeErrorMessage(cause, `Could not render the ${target.label} configuration`);
  } finally {
    busyTarget.value = "";
    await host.resize();
  }
}

function close(): void {
  emit("close");
}
</script>

<template>
  <div v-if="open" class="sheet-scrim" role="presentation" @click.self="close">
    <section
      class="sheet"
      role="dialog"
      aria-modal="true"
      :aria-label="`Preview or copy ${recordName}`"
      @keydown.esc="close"
    >
      <header class="sheet-head">
        <div>
          <h2 class="sheet-title">Preview / copy</h2>
          <p class="sheet-sub">{{ recordName }}</p>
        </div>
        <button type="button" class="sheet-close" aria-label="Close" @click="close">
          <X :size="16" aria-hidden="true" />
        </button>
      </header>

      <p v-if="pinned" class="sheet-note">
        This record pins its target to <strong>{{ pinned }}</strong
        >, so every client receives that format. Clear the target on the record to let the client decide.
      </p>

      <p v-if="error" class="sheet-error" role="alert">{{ error }}</p>

      <ul class="target-list">
        <li v-for="target in CONVERT_TARGETS" :key="target.id" class="target-row">
          <span class="target-name">
            {{ target.label }}
            <span class="target-produces">{{ target.produces }}</span>
          </span>
          <span class="target-actions">
            <button
              type="button"
              class="target-action"
              :disabled="!!busyTarget"
              :title="`Preview the nodes ${target.label} would receive`"
              :aria-label="`Preview ${target.label}`"
              @click="runPreview(target)"
            >
              <LoaderCircle v-if="busyTarget === `preview:${target.id}`" :size="15" class="spin" aria-hidden="true" />
              <Eye v-else :size="15" aria-hidden="true" />
            </button>
            <button
              type="button"
              class="target-action"
              :disabled="!!busyTarget || !renderable(target)"
              :title="
                renderable(target)
                  ? `Copy the ${target.label} configuration`
                  : `The core cannot select ${target.label} on render; preview it instead`
              "
              :aria-label="`Copy ${target.label} configuration`"
              @click="copyDocument(target)"
            >
              <LoaderCircle v-if="busyTarget === `copy:${target.id}`" :size="15" class="spin" aria-hidden="true" />
              <Check v-else-if="copiedTarget === target.id" :size="15" class="ok" aria-hidden="true" />
              <Copy v-else :size="15" aria-hidden="true" />
            </button>
          </span>
        </li>
      </ul>

      <section v-if="preview" class="sheet-result">
        <h3 class="result-title">
          {{ preview.target }} · {{ preview.response.node_count }} node(s)
          <span v-if="preview.response.source_node_count !== undefined" class="result-sub">
            of {{ preview.response.source_node_count }} before the chain
          </span>
        </h3>
        <pre v-if="preview.response.document" class="result-doc">{{ preview.response.document }}</pre>
        <ul v-else class="result-nodes">
          <li v-for="(node, index) in preview.response.nodes.slice(0, 40)" :key="`${node.name}:${index}`">
            <span class="node-name">{{ node.name }}</span>
            <span class="node-meta">{{ node.type }}<template v-if="node.server"> · {{ node.server }}</template></span>
          </li>
        </ul>
        <p v-if="preview.response.nodes.length > 40" class="result-more">
          Showing the first 40 of {{ preview.response.nodes.length }}.
        </p>
      </section>

      <section v-else-if="rendered" class="sheet-result">
        <h3 class="result-title">
          {{ rendered.target }}
          <span class="result-sub">{{ rendered.content.length }} bytes · {{ rendered.contentType }}</span>
        </h3>
        <pre class="result-doc">{{ rendered.content }}</pre>
      </section>
    </section>
  </div>
</template>
