<script setup lang="ts">
/**
 * TargetSheet, "preview / copy this subscription for a client".
 *
 * This is the daily path Sub-Store itself puts one click away: open a record,
 * see every client it can produce, and take either the node list (preview) or
 * the actual document (copy) for the one you use. It replaces the invented
 * "Convert" tab, which asked the operator to paste raw text and an operator
 * JSON blob into a scratchpad, something no Sub-Store user has ever had to do
 * to get a config into their client.
 *
 * Honest capability split, mirroring the backend rather than the wish:
 *  - every target can be PREVIEWED (preview takes the engine target directly);
 *  - a target is COPYABLE only when the core can select it from a UA class
 *    (uaClassTargets), because render picks the client that way. A record that
 *    pins its own `target` always renders as that target, so the sheet says so
 *    instead of implying the choice still applies.
 */
import { computed, nextTick, ref, watch } from "vue";
import { Check, Copy, Eye, Link, LoaderCircle, X } from "@lucide/vue";

import {
  BINDINGS,
  CONVERT_TARGETS,
  buildShareLink,
  callMethod,
  type ConvertTarget,
  type SubStoreShareRow,
  type SubStoreSharesResponse,
  type SubscriptionPreviewResponse,
  type SubscriptionRenderResponse,
} from "../client";
import { useHost } from "../host";
import { safeErrorMessage } from "../subStoreModel";

const props = defineProps<{
  open: boolean;
  /**
   * Where the sheet should sit, in document coordinates. The frame has no
   * scrollport, so a sheet centred with `position: fixed` opens at the top of a
   * frame that may be far above what the operator can see; anchoring it to the
   * row that was clicked keeps it beside the thing it is about.
   */
  anchorTop?: number;
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

/**
 * The share core holds for this record, if any. Loaded when the sheet opens;
 * a failure here is NOT fatal, one-off copies still work, so the sheet says
 * "no stable link" rather than blocking on it.
 */
const share = ref<SubStoreShareRow | null>(null);
const shareLoaded = ref(false);
const copiedLink = ref("");
const shownLink = ref<{ target: string; url: string } | null>(null);

const sheet = ref<HTMLElement | null>(null);

watch(
  () => props.open,
  async (open) => {
    if (!open) return;
    error.value = "";
    preview.value = null;
    rendered.value = null;
    copiedTarget.value = "";
    copiedLink.value = "";
    shownLink.value = null;
    void loadShare();
    // Escape is bound to the panel, and a key event only reaches an element
    // that has focus. Without this the shortcut did nothing until the operator
    // had tabbed inside. The opposite of an escape hatch.
    await nextTick();
    sheet.value?.focus();
  },
);

async function loadShare(): Promise<void> {
  share.value = null;
  shareLoaded.value = false;
  if (!host.bridge) return;
  try {
    const response = await callMethod<SubStoreSharesResponse>(host.bridge, BINDINGS.sharesList, {}).promise;
    const mine = (response?.shares ?? []).filter((row) => row.subscription_id === props.recordId);
    share.value = mine.find((row) => row.enabled) ?? null;
  } catch {
    // Core-backed method unavailable (older server, restricted operator):
    // the sheet simply has no stable links to offer.
    share.value = null;
  } finally {
    shareLoaded.value = true;
    await host.resize();
  }
}

/** Absolute when the server knows its public base, path-only otherwise,
 * a path link still works pasted next to the dashboard's own origin. */
const shareBase = computed(() => share.value?.url || share.value?.path || "");

async function copyLink(target: ConvertTarget): Promise<void> {
  if (!shareBase.value || busyTarget.value) return;
  const url = buildShareLink(shareBase.value, target.id, includeUnsupported.value);
  shownLink.value = null;
  try {
    await navigator.clipboard.writeText(url);
    copiedLink.value = target.id;
    window.setTimeout(() => {
      if (copiedLink.value === target.id) copiedLink.value = "";
    }, 2000);
  } catch {
    // Same rule as document copies: never lose the goods to a clipboard
    // denial. Show the link so it can be selected by hand.
    shownLink.value = { target: target.id, url };
    await host.resize();
  }
}

const pinned = computed(() => (props.pinnedTarget ?? "").trim());

/**
 * The produce() flags, mirroring Sub-Store's preview sheet toggles. They ride
 * on both preview and copy so what you see is what a client would get.
 */
const includeUnsupported = ref(false);

async function runPreview(target: ConvertTarget): Promise<void> {
  if (!host.bridge || busyTarget.value) return;
  busyTarget.value = `preview:${target.id}`;
  error.value = "";
  rendered.value = null;
  try {
    // No options here on purpose: preview summarizes the node list after the
    // chain, before produce() runs, so produce flags cannot change what it
    // shows. Sending them anyway would imply they do.
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
      // Explicit target: the caller who names a client means that client,
      // record pin or not. The same contract as Sub-Store's ?target= URLs.
      target: target.id,
      options: { "include-unsupported-proxy": includeUnsupported.value },
    }).promise;
    const content = response?.content ?? "";
    if (!content) throw new Error("The render returned no document");
    // The document is shown either way. A clipboard write can fail for
    // reasons that have nothing to do with the configuration. An unfocused
    // document, a denied permission, and losing a good render to that would
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
      error.value = "The clipboard is unavailable. The configuration is below, select it to copy.";
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
      ref="sheet"
      tabindex="-1"
      class="sheet"
      :style="{ '--overlay-anchor-top': `${anchorTop ?? 32}px` }"
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

      <label class="sheet-toggle">
        <input v-model="includeUnsupported" type="checkbox" />
        Include protocols the client does not support
      </label>

      <p v-if="pinned" class="sheet-note">
        Served URLs for this record always produce <strong>{{ pinned }}</strong> (its pinned target).
        Links and copies below name their client explicitly, which overrides the pin.
      </p>

      <p v-if="share" class="sheet-note sheet-note-share">
        Published as <strong>/{{ share.slug }}</strong>. Each link below is that URL pinned to one client.
      </p>
      <p v-else-if="shareLoaded" class="sheet-note">
        No published share yet, so there are no stable links, copies below are one-off documents.
        Publish this record from the row menu to hand clients a URL.
      </p>

      <p v-if="error" class="sheet-error" role="alert">{{ error }}</p>

      <p v-if="shownLink" class="sheet-note sheet-note-share">
        The clipboard is unavailable, select the {{ shownLink.target }} link by hand:
        <code class="share-link">{{ shownLink.url }}</code>
      </p>

      <ul class="target-list">
        <li v-for="target in CONVERT_TARGETS" :key="target.id" class="target-row">
          <span class="target-name">
            {{ target.label }}
            <span class="target-produces">{{ target.produces }}</span>
          </span>
          <span class="target-actions">
            <button
              v-if="share"
              type="button"
              class="target-action"
              :disabled="!!busyTarget"
              :title="`Copy the subscription link for ${target.label}`"
              :aria-label="`Copy ${target.label} link`"
              @click="copyLink(target)"
            >
              <Check v-if="copiedLink === target.id" :size="15" class="ok" aria-hidden="true" />
              <Link v-else :size="15" aria-hidden="true" />
            </button>
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
              :disabled="!!busyTarget"
              :title="`Copy the ${target.label} configuration`"
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
        <pre v-if="preview.response.document" class="result-doc" tabindex="0">{{ preview.response.document }}</pre>
        <ul v-else class="node-list">
          <li v-for="(node, index) in preview.response.nodes.slice(0, 40)" :key="`${node.name}:${index}`" class="node-row">
            <span class="node-name" :title="node.name">{{ node.name }}</span>
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
        <pre class="result-doc" tabindex="0">{{ rendered.content }}</pre>
      </section>
    </section>
  </div>
</template>
