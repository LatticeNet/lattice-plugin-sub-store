<script setup lang="ts">
/**
 * TargetSheet is the client output workspace.
 *
 * Client is an output decision, so changing it must change the evidence on
 * screen. The selected target therefore renders immediately when the session
 * has admin access. Redacted pipeline nodes remain a separate diagnostic view
 * for read-scoped sessions and for operators checking the chain.
 */
import { computed, nextTick, onBeforeUnmount, ref, watch } from "vue";
import { Check, Copy, Link, LoaderCircle, RefreshCw, X } from "@lucide/vue";

import DocumentView from "./DocumentView.vue";
import LtButton from "./lt/LtButton.vue";
import {
  BINDINGS,
  CONVERT_TARGETS,
  KIND_COLLECTION,
  buildShareLink,
  callMethod,
  type SubStoreShareRow,
  type SubStoreSharesResponse,
  type SubscriptionListItem,
  type SubscriptionPreviewResponse,
  type SubscriptionRenderResponse,
} from "../client";
import { trapDialogTab } from "../dialogFocus";
import { isFileRecord } from "../filePreview";
import { useHost } from "../host";
import {
  editorLanguageForFileType,
  editorLanguageForRender,
  editorLanguageLabel,
} from "../previewLanguage";
import { safeErrorMessage } from "../subStoreModel";

const props = defineProps<{
  open: boolean;
  /** Retained while row callers finish removing document-coordinate anchors. */
  anchorTop?: number;
  record: SubscriptionListItem | null;
}>();

const emit = defineEmits<{ (e: "close"): void }>();
const host = useHost();

type ViewMode = "document" | "nodes";
type ResultStatus = "idle" | "loading" | "ready" | "error";
type CopiedAction = "" | "link" | "document";

const lastChosen = ref("");
const chosen = ref(CONVERT_TARGETS[0]!.id);
const viewMode = ref<ViewMode>("document");
const includeUnsupported = ref(false);

const documentStatus = ref<ResultStatus>("idle");
const documentError = ref("");
const rendered = ref<{
  key: string;
  target: string;
  content: string;
  contentType: string;
  nodeCount: number;
  droppedCount: number;
  droppedProtocols: string[];
} | null>(null);

const nodesStatus = ref<ResultStatus>("idle");
const nodesError = ref("");
const preview = ref<{ target: string; response: SubscriptionPreviewResponse } | null>(null);

const copied = ref<CopiedAction>("");
const copyingLink = ref(false);
const copyingDocument = ref(false);
const actionError = ref("");
const shownLink = ref("");

const share = ref<SubStoreShareRow | null>(null);
const shareState = ref<"loading" | "ready" | "unavailable" | "failed">("loading");

const sheet = ref<HTMLElement | null>(null);

let documentGeneration = 0;
let documentCancel: (() => void) | null = null;
let nodesGeneration = 0;
let nodesCancel: (() => void) | null = null;
let shareGeneration = 0;

const recordId = computed(() => props.record?.id ?? "");
const recordName = computed(() => props.record?.display_name || props.record?.name || "");
const isFile = computed(() => isFileRecord(props.record));
const isCollection = computed(() => props.record?.kind === KIND_COLLECTION);
const pinned = computed(() => (props.record?.target ?? "").trim());
const canRender = computed(() => host.available(BINDINGS.subRender));
const canPreview = computed(() => host.available(BINDINGS.subPreview));
const canListShares = computed(() => host.available(BINDINGS.sharesList));

const chosenTarget = computed(
  () => CONVERT_TARGETS.find((target) => target.id === chosen.value) ?? CONVERT_TARGETS[0]!,
);
const renderedTarget = computed(() =>
  CONVERT_TARGETS.find((target) => target.id === rendered.value?.target),
);
const renderedLanguage = computed(() =>
  isFile.value
    ? editorLanguageForFileType(props.record?.file_type)
    : editorLanguageForRender({
        contentType: rendered.value?.contentType ?? "",
        produces: renderedTarget.value?.produces ?? chosenTarget.value.produces,
      }),
);
const renderedLanguageLabel = computed(() => editorLanguageLabel(renderedLanguage.value));

/**
 * What to say when the chosen client refused some of the record's nodes.
 *
 * The count comes from the client's own producer, so the sentence is about this
 * client and this record rather than a table of protocol support that would
 * drift from the pinned engine. It names the toggle that changes the outcome,
 * because that toggle is a few centimetres away in the same sheet and without
 * the sentence nobody connects the two.
 */
const droppedNotice = computed(() => {
  const dropped = rendered.value?.droppedCount ?? 0;
  if (dropped <= 0) return "";
  const total = rendered.value?.nodeCount ?? 0;
  const protocols = rendered.value?.droppedProtocols ?? [];
  const named = protocols.length > 0 ? ` (${protocols.join(", ")})` : "";
  const all = total > 0 && dropped >= total;
  const scope = all
    ? `any of this record's ${total} nodes`
    : `${dropped} of this record's ${total || dropped} nodes`;
  const outcome = all ? "This document has none of them." : "They are not in this document.";
  return `${chosenTarget.value.label} cannot carry ${scope}${named}. ${outcome}`;
});
const renderedBytes = computed(() =>
  rendered.value ? new TextEncoder().encode(rendered.value.content).byteLength : 0,
);
const filteredNodeCount = computed(() => {
  const source = preview.value?.response.source_node_count;
  return source === undefined ? undefined : Math.max(0, source - preview.value!.response.node_count);
});

const shareBase = computed(() => share.value?.url || share.value?.path || "");
const shareUrl = computed(() =>
  shareBase.value
    ? isFile.value
      ? shareBase.value
      : buildShareLink(shareBase.value, chosen.value, includeUnsupported.value)
    : "",
);

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

function documentKey(target = isFile.value ? "" : chosen.value): string {
  return [
    recordId.value,
    target,
    isFile.value ? "file" : includeUnsupported.value ? "unsupported:on" : "unsupported:off",
  ].join("|");
}

function documentIsCurrent(): boolean {
  return documentStatus.value === "ready" && rendered.value?.key === documentKey();
}

function stopDocumentRequest(): void {
  documentGeneration += 1;
  documentCancel?.();
  documentCancel = null;
}

function stopNodesRequest(): void {
  nodesGeneration += 1;
  nodesCancel?.();
  nodesCancel = null;
}

function stopAllRequests(): void {
  stopDocumentRequest();
  stopNodesRequest();
  shareGeneration += 1;
}

function resetWorkspace(): void {
  stopAllRequests();
  documentStatus.value = "idle";
  documentError.value = "";
  rendered.value = null;
  nodesStatus.value = "idle";
  nodesError.value = "";
  preview.value = null;
  copied.value = "";
  actionError.value = "";
  shownLink.value = "";
}

async function loadShare(): Promise<void> {
  const generation = ++shareGeneration;
  const id = recordId.value;
  share.value = null;
  shareState.value = "loading";
  if (!canListShares.value) {
    shareState.value = "unavailable";
    return;
  }
  if (!host.bridge) {
    shareState.value = "failed";
    return;
  }
  try {
    const response = await callMethod<SubStoreSharesResponse>(
      host.bridge,
      BINDINGS.sharesList,
      {},
    ).promise;
    if (generation !== shareGeneration || !props.open || id !== recordId.value) return;
    const mine = (response?.shares ?? []).filter((row) => row.subscription_id === id);
    share.value = mine.find((row) => row.enabled) ?? null;
    shareState.value = "ready";
  } catch {
    if (generation !== shareGeneration || !props.open || id !== recordId.value) return;
    share.value = null;
    shareState.value = "failed";
  } finally {
    if (generation === shareGeneration) await host.resize();
  }
}

async function loadDocument(): Promise<void> {
  if (!host.bridge || !canRender.value) {
    documentStatus.value = "error";
    documentError.value =
      "This session cannot render client documents. Use Node preview for the redacted read view.";
    return;
  }

  documentCancel?.();
  const generation = ++documentGeneration;
  const id = recordId.value;
  const target = isFile.value ? "" : chosen.value;
  const key = documentKey(target);
  const includeUnsupportedAtStart = includeUnsupported.value;
  rendered.value = null;
  documentError.value = "";
  documentStatus.value = "loading";
  actionError.value = "";

  try {
    const call = callMethod<SubscriptionRenderResponse>(host.bridge, BINDINGS.subRender, {
      subscription_id: id,
      format: "plain",
      ...(isFile.value
        ? {}
        : {
            target,
            options: { "include-unsupported-proxy": includeUnsupportedAtStart },
            // Ask why, so a near-empty document can say so. The path that
            // serves a client never sets this.
            explain: true,
          }),
    });
    documentCancel = call.cancel;
    const response = await call.promise;
    if (
      generation !== documentGeneration ||
      !props.open ||
      id !== recordId.value ||
      target !== (isFile.value ? "" : chosen.value) ||
      includeUnsupportedAtStart !== includeUnsupported.value
    ) {
      return;
    }
    if (typeof response?.content !== "string") {
      throw new Error("The render response did not contain a document");
    }
    rendered.value = {
      key,
      target,
      content: response.content,
      contentType: response.content_type ?? "",
      nodeCount: Number(response.node_count ?? 0),
      droppedCount: Number(response.dropped_node_count ?? 0),
      droppedProtocols: Array.isArray(response.dropped_protocols) ? response.dropped_protocols : [],
    };
    documentStatus.value = "ready";
  } catch (cause) {
    if (generation !== documentGeneration || !props.open) return;
    rendered.value = null;
    documentStatus.value = "error";
    documentError.value = safeErrorMessage(
      cause,
      isFile.value
        ? "Could not render this file"
        : `Could not render the ${chosenTarget.value.label} document`,
    );
  } finally {
    if (generation === documentGeneration) {
      documentCancel = null;
      await host.resize();
    }
  }
}

async function loadNodes(): Promise<void> {
  viewMode.value = "nodes";
  if (!host.bridge || !canPreview.value || isFile.value) {
    nodesStatus.value = "error";
    nodesError.value = isFile.value
      ? "Files are documents and do not have a node preview."
      : "This session cannot preview this record's nodes.";
    return;
  }

  nodesCancel?.();
  const generation = ++nodesGeneration;
  const id = recordId.value;
  const target = chosen.value;
  preview.value = null;
  nodesError.value = "";
  nodesStatus.value = "loading";

  try {
    const call = callMethod<SubscriptionPreviewResponse>(host.bridge, BINDINGS.subPreview, {
      subscription_id: id,
      target,
    });
    nodesCancel = call.cancel;
    const response = await call.promise;
    if (
      generation !== nodesGeneration ||
      !props.open ||
      id !== recordId.value ||
      target !== chosen.value
    ) {
      return;
    }
    preview.value = { target, response };
    nodesStatus.value = "ready";
  } catch (cause) {
    if (generation !== nodesGeneration || !props.open) return;
    preview.value = null;
    nodesStatus.value = "error";
    nodesError.value = safeErrorMessage(cause, "Node preview failed");
  } finally {
    if (generation === nodesGeneration) {
      nodesCancel = null;
      await host.resize();
    }
  }
}

function choose(id: string): void {
  if (chosen.value === id) return;
  chosen.value = id;
  lastChosen.value = id;
  copied.value = "";
  actionError.value = "";
  shownLink.value = "";
  stopNodesRequest();
  preview.value = null;
  nodesStatus.value = "idle";
  viewMode.value = canRender.value ? "document" : "nodes";
  if (canRender.value) void loadDocument();
  else void loadNodes();
}

function focusClient(id: string): void {
  void nextTick(() => {
    sheet.value
      ?.querySelector<HTMLElement>(`[data-client-target="${id}"]`)
      ?.focus();
  });
}

function revealChosenClient(): void {
  if (!sheet.value || typeof sheet.value.querySelector !== "function") return;
  sheet.value
    .querySelector<HTMLElement>(`[data-client-target="${chosen.value}"]`)
    ?.scrollIntoView({ block: "nearest", inline: "nearest" });
}

function onClientKeydown(event: KeyboardEvent, id: string): void {
  const index = CONVERT_TARGETS.findIndex((target) => target.id === id);
  if (index < 0) return;
  let nextIndex = index;
  if (event.key === "ArrowRight" || event.key === "ArrowDown") {
    nextIndex = (index + 1) % CONVERT_TARGETS.length;
  } else if (event.key === "ArrowLeft" || event.key === "ArrowUp") {
    nextIndex = (index - 1 + CONVERT_TARGETS.length) % CONVERT_TARGETS.length;
  } else if (event.key === "Home") {
    nextIndex = 0;
  } else if (event.key === "End") {
    nextIndex = CONVERT_TARGETS.length - 1;
  } else {
    return;
  }
  event.preventDefault();
  const next = CONVERT_TARGETS[nextIndex]!;
  choose(next.id);
  focusClient(next.id);
}

function showDocument(): void {
  if (!canRender.value) return;
  viewMode.value = "document";
  if (!documentIsCurrent()) void loadDocument();
}

function showNodes(): void {
  if (isFile.value || !canPreview.value) return;
  viewMode.value = "nodes";
  if (preview.value?.target !== chosen.value || nodesStatus.value !== "ready") {
    void loadNodes();
  }
}

function onViewTabKeydown(event: KeyboardEvent): void {
  if (!["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key)) return;
  event.preventDefault();
  const next: ViewMode =
    event.key === "ArrowLeft" || event.key === "Home" ? "document" : "nodes";
  if (next === "document") {
    if (!canRender.value) return;
    showDocument();
  } else {
    if (!canPreview.value || isFile.value) return;
    showNodes();
  }
  void nextTick(() => {
    sheet.value?.querySelector<HTMLElement>(`#target-${next}-tab`)?.focus();
  });
}

function flash(action: CopiedAction): void {
  copied.value = action;
  window.setTimeout(() => {
    if (copied.value === action) copied.value = "";
  }, 2000);
}

async function copyLink(): Promise<void> {
  if (!shareUrl.value || copyingLink.value) return;
  copyingLink.value = true;
  shownLink.value = "";
  actionError.value = "";
  try {
    await navigator.clipboard.writeText(shareUrl.value);
    flash("link");
  } catch {
    shownLink.value = shareUrl.value;
    await host.resize();
  } finally {
    copyingLink.value = false;
  }
}

async function copyDocument(): Promise<void> {
  if (!documentIsCurrent() || !rendered.value || copyingDocument.value) return;
  copyingDocument.value = true;
  actionError.value = "";
  try {
    await navigator.clipboard.writeText(rendered.value.content);
    flash("document");
  } catch {
    actionError.value =
      "The clipboard is unavailable. The document remains below, select it to copy.";
  } finally {
    copyingDocument.value = false;
  }
}

function retryCurrent(): void {
  if (viewMode.value === "document") void loadDocument();
  else void loadNodes();
}

function close(): void {
  stopAllRequests();
  emit("close");
}

function onTab(event: KeyboardEvent): void {
  if (sheet.value) trapDialogTab(event, sheet.value);
}

watch(
  [() => props.open, recordId],
  async ([open]) => {
    if (!open) {
      stopAllRequests();
      return;
    }
    resetWorkspace();
    chosen.value =
      CONVERT_TARGETS.find((target) => target.id === pinned.value)?.id ??
      CONVERT_TARGETS.find((target) => target.id === lastChosen.value)?.id ??
      CONVERT_TARGETS[0]!.id;
    viewMode.value = canRender.value ? "document" : "nodes";
    void loadShare();
    if (canRender.value) void loadDocument();
    else if (!isFile.value) void loadNodes();
    await nextTick();
    sheet.value?.focus();
    revealChosenClient();
  },
  { immediate: true },
);

watch(includeUnsupported, () => {
  if (!props.open || isFile.value || !canRender.value) return;
  viewMode.value = "document";
  void loadDocument();
});

onBeforeUnmount(stopAllRequests);
</script>

<template>
  <div v-if="open" class="sheet-scrim" role="presentation" @click.self="close">
    <section
      ref="sheet"
      tabindex="-1"
      class="sheet target-workspace"
      role="dialog"
      aria-modal="true"
      :aria-label="`${isFile ? 'Document preview' : 'Client output'} for ${recordName}`"
      :style="{ '--overlay-anchor-top': `${anchorTop ?? 0}px` }"
      @keydown.esc="close"
      @keydown.tab="onTab"
    >
      <header class="sheet-head">
        <div class="sheet-headings">
          <h2 class="sheet-title">{{ isFile ? "Document preview" : "Client output" }}</h2>
          <p class="sheet-sub" :title="recordName">
            <span>{{ recordName }}</span>
            <code v-if="recordId !== recordName">{{ recordId }}</code>
          </p>
        </div>
        <button type="button" class="sheet-close" aria-label="Close" @click="close">
          <X :size="16" aria-hidden="true" />
        </button>
      </header>

      <div class="target-workspace-body" :class="{ 'is-file': isFile }">
        <aside class="target-controls" aria-label="Output controls">
          <section v-if="!isFile" class="target-control-section">
            <h3 class="control-eyebrow">
              Client <span class="control-count">{{ CONVERT_TARGETS.length }}</span>
            </h3>
            <div class="target-grid" role="radiogroup" aria-label="Client">
              <button
                v-for="target in CONVERT_TARGETS"
                :key="target.id"
                type="button"
                role="radio"
                :aria-checked="chosen === target.id"
                :tabindex="chosen === target.id ? 0 : -1"
                :data-client-target="target.id"
                class="target-chip"
                :class="{ 'is-active': chosen === target.id }"
                @click="choose(target.id)"
                @keydown="onClientKeydown($event, target.id)"
              >
                <span class="target-chip-name">{{ target.label }}</span>
                <span class="target-chip-produces">{{ target.produces }}</span>
                <Check
                  v-if="chosen === target.id"
                  class="target-chip-check"
                  :size="13"
                  aria-hidden="true"
                />
              </button>
            </div>
            <p v-if="pinned" class="control-note">
              Record default: <code>{{ pinned }}</code>. This selection overrides it for preview
              and copy.
            </p>
          </section>

          <section v-else class="target-control-section">
            <h3 class="control-eyebrow">Document</h3>
            <p class="control-note">
              A file is delivered as the document it is. Client selection does not change it.
            </p>
          </section>

          <section v-if="!isFile" class="target-control-section">
            <h3 class="control-eyebrow">Output options</h3>
            <label class="sheet-toggle">
              <input
                v-model="includeUnsupported"
                type="checkbox"
                name="include-unsupported-proxy"
              />
              Include protocols the selected client does not support
            </label>
          </section>

          <section class="target-control-section delivery-section">
            <h3 class="control-eyebrow">Delivery</h3>
            <template v-if="shareState === 'loading'">
              <p class="delivery-state">Checking publication…</p>
            </template>
            <template v-else-if="shareState === 'unavailable'">
              <p class="delivery-state is-unknown">Publication status requires admin access</p>
              <p class="control-note">Document generation is unaffected.</p>
            </template>
            <template v-else-if="shareState === 'failed'">
              <p class="delivery-state is-unknown">Could not check publication status</p>
              <p class="control-note">Document generation is unaffected.</p>
            </template>
            <template v-else-if="share">
              <p class="delivery-state is-published">Published as /{{ share.slug }}</p>
              <LtButton :disabled="copyingLink" @click="copyLink()">
                <LoaderCircle v-if="copyingLink" :size="14" class="spin" aria-hidden="true" />
                <Check v-else-if="copied === 'link'" :size="14" aria-hidden="true" />
                <Link v-else :size="14" aria-hidden="true" />
                {{ copied === "link" ? "Link copied" : "Copy stable link" }}
              </LtButton>
            </template>
            <template v-else>
              <p class="delivery-state">Not published</p>
              <p class="control-note">
                Copy document still works. Publish under Networking to create a stable URL.
              </p>
            </template>
            <p v-if="shownLink" class="manual-copy">
              Clipboard unavailable. Select the link:
              <code>{{ shownLink }}</code>
            </p>
          </section>

          <p v-if="!canRender" class="permission-strip">
            Client documents require admin access. This session can preview redacted nodes
            only.
          </p>
          <p v-if="actionError" class="sheet-error" role="alert">{{ actionError }}</p>
        </aside>

        <main class="target-output">
          <div class="target-output-toolbar">
            <div class="evidence-rail" role="status" aria-live="polite">
              <template v-if="viewMode === 'document'">
                <span class="evidence-step">
                  <small>{{ isFile ? "DOCUMENT" : "CLIENT" }}</small>
                  <strong>{{ isFile ? "FILE" : chosenTarget.label }}</strong>
                </span>
                <span class="evidence-arrow" aria-hidden="true">→</span>
                <span class="evidence-step">
                  <small>OUTPUT</small>
                  <strong>{{ renderedLanguageLabel }}</strong>
                </span>
                <span class="evidence-arrow" aria-hidden="true">→</span>
                <span class="evidence-step">
                  <small>RECORD</small>
                  <strong>{{ recordId }}</strong>
                </span>
                <template v-if="documentStatus === 'ready'">
                  <span class="evidence-arrow" aria-hidden="true">→</span>
                  <span class="evidence-step">
                    <small>SIZE</small>
                    <strong>{{ formatBytes(renderedBytes) }}</strong>
                  </span>
                </template>
              </template>
              <template v-else>
                <span class="evidence-step">
                  <small>AFTER OPERATIONS</small>
                  <strong>NODES</strong>
                </span>
                <span class="evidence-arrow" aria-hidden="true">→</span>
                <span class="evidence-step">
                  <small>RECORD</small>
                  <strong>{{ recordId }}</strong>
                </span>
                <template v-if="nodesStatus === 'ready' && preview">
                  <span class="evidence-arrow" aria-hidden="true">→</span>
                  <span class="evidence-step">
                    <small>KEPT</small>
                    <strong>
                      {{ preview.response.node_count }}
                      <template v-if="preview.response.source_node_count !== undefined">
                        / {{ preview.response.source_node_count }}
                      </template>
                    </strong>
                  </span>
                </template>
              </template>
            </div>

            <div v-if="!isFile" class="output-tabs" role="tablist" aria-label="Preview evidence">
              <button
                id="target-document-tab"
                type="button"
                role="tab"
                :aria-selected="viewMode === 'document'"
                :tabindex="viewMode === 'document' ? 0 : -1"
                :disabled="!canRender"
                @click="showDocument()"
                @keydown="onViewTabKeydown"
              >
                Document
              </button>
              <button
                id="target-nodes-tab"
                type="button"
                role="tab"
                :aria-selected="viewMode === 'nodes'"
                :tabindex="viewMode === 'nodes' ? 0 : -1"
                :disabled="!canPreview"
                @click="showNodes()"
                @keydown="onViewTabKeydown"
              >
                Node preview
              </button>
            </div>

            <div class="output-heading">
              <div>
                <h3 :id="viewMode === 'document' ? 'target-document-label' : 'target-nodes-label'">
                  <template v-if="viewMode === 'document'">
                    {{ isFile ? "Rendered document" : `What ${chosenTarget.label} receives` }}
                  </template>
                  <template v-else>
                    {{ isCollection ? "Merged nodes after operations" : "Nodes after operations" }}
                  </template>
                </h3>
                <p v-if="viewMode === 'document'" class="output-description">
                  <template v-if="documentStatus === 'loading'">
                    Generating {{ isFile ? "document" : chosenTarget.label }} output…
                  </template>
                  <template v-else-if="documentStatus === 'ready'">
                    {{ renderedLanguageLabel }} · {{ formatBytes(renderedBytes) }} ·
                    {{ rendered?.content.length ?? 0 }} characters
                  </template>
                  <template v-else>The exact document for the selected output target.</template>
                </p>
                <p v-else class="output-description">
                  Redacted node evidence from the chain, separate from the client document.
                </p>
              </div>
              <div class="output-actions">
                <LtButton
                  v-if="viewMode === 'document'"
                  variant="primary"
                  :disabled="!documentIsCurrent() || !rendered?.content.length || copyingDocument"
                  @click="copyDocument()"
                >
                  <LoaderCircle
                    v-if="copyingDocument"
                    :size="14"
                    class="spin"
                    aria-hidden="true"
                  />
                  <Check v-else-if="copied === 'document'" :size="14" aria-hidden="true" />
                  <Copy v-else :size="14" aria-hidden="true" />
                  {{ copied === "document" ? "Document copied" : "Copy document" }}
                </LtButton>
                <LtButton
                  v-else-if="nodesStatus === 'error'"
                  @click="retryCurrent()"
                >
                  <RefreshCw :size="14" aria-hidden="true" /> Retry
                </LtButton>
              </div>
            </div>
          </div>

          <section
            v-if="viewMode === 'document'"
            class="output-panel document-panel"
            role="tabpanel"
            :aria-labelledby="isFile ? 'target-document-label' : 'target-document-tab target-document-label'"
          >
            <!-- A client that refuses a protocol produces a document with
                 nothing of those nodes in it, which reads as a broken render.
                 The toggle that changes it is in this same sheet, so the notice
                 names it rather than leaving the operator to guess. Its own
                 v-if, so the states below stay one chain. -->
            <div
              v-if="documentStatus === 'ready' && droppedNotice"
              class="output-dropped"
              role="status"
            >
              <p>{{ droppedNotice }}</p>
              <!-- The remedy, not directions to it. This named the toggle and
                   left the operator to find it in the other column, which is
                   the same as not offering it: the notice was read twice and
                   the document stayed empty both times. -->
              <button
                v-if="!includeUnsupported"
                type="button"
                class="button button-secondary button-compact"
                @click="includeUnsupported = true"
              >
                Send them anyway
              </button>
            </div>
            <div v-if="documentStatus === 'loading'" class="output-state" role="status">
              <LoaderCircle :size="18" class="spin" aria-hidden="true" />
              <strong>Generating {{ isFile ? "document" : chosenTarget.label }} output…</strong>
              <span>The previous target cannot be mistaken for this result.</span>
            </div>
            <div v-else-if="documentStatus === 'error'" class="output-state is-error" role="alert">
              <strong>{{ documentError }}</strong>
              <LtButton @click="retryCurrent()">
                <RefreshCw :size="14" aria-hidden="true" /> Retry render
              </LtButton>
            </div>
            <div
              v-else-if="documentStatus === 'ready' && rendered && !rendered.content.length"
              class="output-state is-empty"
              role="status"
            >
              <strong>The render completed with an empty document.</strong>
              <span>Nothing is available to copy for this client target.</span>
            </div>
            <DocumentView
              v-else-if="documentStatus === 'ready' && rendered"
              class="result-doc"
              :text="rendered.content"
              :language="renderedLanguage"
              :rows="24"
              :aria-labelledby="'target-document-label'"
            />
            <div v-else class="output-state">
              <strong>No document generated yet.</strong>
              <span>Choose a client or retry the render.</span>
            </div>
          </section>

          <section
            v-else
            class="output-panel nodes-panel"
            role="tabpanel"
            aria-labelledby="target-nodes-tab target-nodes-label"
          >
            <div v-if="nodesStatus === 'loading'" class="output-state" role="status">
              <LoaderCircle :size="18" class="spin" aria-hidden="true" />
              <strong>Previewing nodes…</strong>
            </div>
            <div v-else-if="nodesStatus === 'error'" class="output-state is-error" role="alert">
              <strong>{{ nodesError }}</strong>
              <LtButton @click="retryCurrent()">
                <RefreshCw :size="14" aria-hidden="true" /> Retry preview
              </LtButton>
            </div>
            <template v-else-if="nodesStatus === 'ready' && preview">
              <div class="nodes-summary">
                <strong>Kept {{ preview.response.node_count }}</strong>
                <span v-if="preview.response.source_node_count !== undefined">
                  of {{ preview.response.source_node_count }} source nodes
                </span>
                <span v-if="filteredNodeCount">Filtered {{ filteredNodeCount }}</span>
                <span v-if="preview.response.truncated">Result truncated</span>
              </div>
              <table v-if="preview.response.nodes.length" class="preview-node-table">
                <thead>
                  <tr>
                    <th scope="col">Name</th>
                    <th scope="col">Type</th>
                    <th scope="col">Endpoint</th>
                  </tr>
                </thead>
                <tbody>
                  <tr
                    v-for="(node, index) in preview.response.nodes.slice(0, 40)"
                    :key="`${node.name}:${index}`"
                  >
                    <td :title="node.name">{{ node.name }}</td>
                    <td><code>{{ node.type }}</code></td>
                    <td>
                      <code>
                        {{ node.server || "Unknown" }}<template v-if="node.port">:{{ node.port }}</template>
                      </code>
                    </td>
                  </tr>
                </tbody>
              </table>
              <div v-else class="output-state is-empty" role="status">
                <strong>The chain kept no nodes.</strong>
                <span>A client subscribing now receives an empty node list.</span>
              </div>
              <p v-if="preview.response.nodes.length > 40" class="result-more">
                Showing the first 40 of {{ preview.response.nodes.length }}.
              </p>
            </template>
            <div v-else class="output-state">
              <strong>No node evidence loaded.</strong>
              <LtButton @click="showNodes()">Preview nodes</LtButton>
            </div>
          </section>
        </main>
      </div>
    </section>
  </div>
</template>
