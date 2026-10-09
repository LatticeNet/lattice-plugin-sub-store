<script setup lang="ts">
/**
 * TargetSheet is the client output workspace.
 *
 * Client is an output decision, so changing it must change the evidence on
 * screen. The selected target therefore renders immediately when the session
 * has admin access. Redacted pipeline nodes remain a separate diagnostic view
 * for read-scoped sessions and for operators checking the chain.
 */
import { computed, nextTick, onBeforeUnmount, ref, toRef, watch } from "vue";
import { Check, Copy, Link, LoaderCircle, RefreshCw, X } from "@lucide/vue";

import DocumentView from "./DocumentView.vue";
import LtButton from "./lt/LtButton.vue";
import LtManualCopy from "./lt/LtManualCopy.vue";
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
import { publishStateFor, type PublishState } from "../shareState";
import { trapDialogTab } from "../dialogFocus";
import { useOverlayRegistration } from "../useOverlayRegistration";
import { isFileRecord } from "../filePreview";
import { useHost } from "../host";
import { t } from "../i18n";
import { copyText } from "../hostClipboard";
import {
  editorLanguageForFileType,
  editorLanguageForRender,
  editorLanguageLabel,
} from "../previewLanguage";
import { safeErrorMessage } from "../subStoreModel";

const props = defineProps<{
  open: boolean;
  record: SubscriptionListItem | null;
}>();

const emit = defineEmits<{ (e: "close"): void }>();

// The panel registers while it is open; the visible screen's one document
// handler closes the top of the stack. Escape is not answered in here.
useOverlayRegistration(toRef(props, "open"), () => emit("close"));
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

/**
 * The rendered document, when the clipboard refused it.
 *
 * A config document is thousands of characters, so "select it to copy" was not
 * a recovery: the document below is a syntax-highlighted read-only view an
 * operator cannot reliably drag-select to the end. This puts the whole text in
 * a real textarea, already selected, so the copy is one keystroke.
 */
const shownDocument = ref("");

const share = ref<SubStoreShareRow | null>(null);
const shareState = ref<"loading" | "ready" | "unavailable" | "failed">("loading");
/** Whether the share found actually serves anyone: a disabled or expired
 *  share has a slug and a link and returns nothing to a client. */
const shareVerdict = ref<PublishState | null>(null);

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
  return t.sheet.dropped(chosenTarget.value.label, dropped, total, protocols.join(", "));
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
  shownDocument.value = "";
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
    // The same verdict the record list prints, so the sheet never calls a
    // share "published" that the table calls expired.
    const verdict = publishStateFor(response?.shares ?? [], id);
    shareVerdict.value = verdict;
    share.value = verdict.shares.find((row) => row.slug === verdict.slug) ?? verdict.shares[0] ?? null;
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
    documentError.value = t.sheet.cannotRender;
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
      throw new Error(t.sheet.noDocument);
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
      isFile.value ? t.sheet.renderFileFailed : t.sheet.renderFailed(chosenTarget.value.label),
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
    nodesError.value = isFile.value ? t.sheet.filesNoNodes : t.sheet.cannotPreview;
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
    nodesError.value = safeErrorMessage(cause, t.sheet.nodesFailed);
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
    if (await copyText(shareUrl.value)) flash("link");
    else {
      shownLink.value = shareUrl.value;
      await host.resize();
    }
  } finally {
    copyingLink.value = false;
  }
}

async function copyDocument(): Promise<void> {
  if (!documentIsCurrent() || !rendered.value || copyingDocument.value) return;
  copyingDocument.value = true;
  actionError.value = "";
  shownDocument.value = "";
  try {
    if (await copyText(rendered.value.content)) flash("document");
    else {
      shownDocument.value = rendered.value.content;
      await host.resize();
    }
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
      data-size="output"
      role="dialog"
      aria-modal="true"
      :aria-label="isFile ? t.sheet.dialogFile(recordName) : t.sheet.dialogClient(recordName)"
      @keydown.tab="onTab"
    >
      <header class="sheet-head">
        <div class="sheet-headings">
          <h2 class="sheet-title">{{ isFile ? t.sheet.titleFile : t.sheet.titleClient }}</h2>
          <p class="sheet-sub" :title="recordName">
            <span>{{ recordName }}</span>
            <code v-if="recordId !== recordName">{{ recordId }}</code>
          </p>
        </div>
        <button type="button" class="sheet-close" :aria-label="t.sheet.close" @click="close">
          <X :size="16" aria-hidden="true" />
        </button>
      </header>

      <div class="target-workspace-body" :class="{ 'is-file': isFile }">
        <aside class="target-controls" :aria-label="t.sheet.controls">
          <!-- Delivery first. It is two lines and the reason most operators
               open this sheet ("is it published, and where"), and it sat
               under fourteen client chips, below the fold on a wide frame. -->
          <section class="target-control-section delivery-section">
            <h3 class="control-eyebrow">{{ t.sheet.delivery }}</h3>
            <template v-if="shareState === 'loading'">
              <p class="delivery-state">{{ t.sheet.checking }}</p>
            </template>
            <template v-else-if="shareState === 'unavailable'">
              <p class="delivery-state is-unknown">{{ t.sheet.needsAdmin }}</p>
              <p class="control-note">{{ t.sheet.unaffected }}</p>
            </template>
            <template v-else-if="shareState === 'failed'">
              <p class="delivery-state is-unknown">{{ t.sheet.checkFailed }}</p>
              <p class="control-note">{{ t.sheet.unaffected }}</p>
            </template>
            <template v-else-if="share">
              <p v-if="shareVerdict?.tone === 'ok'" class="delivery-state is-published">{{ t.sheet.publishedAs(share.slug) }}</p>
              <template v-else>
                <p class="delivery-state is-unknown">{{ t.sheet.shareState(shareVerdict?.label ?? "") }}</p>
                <p class="control-note">{{ t.sheet.renew(shareVerdict?.title ?? "") }}</p>
              </template>
              <LtButton :disabled="copyingLink" @click="copyLink()">
                <LoaderCircle v-if="copyingLink" :size="14" class="spin" aria-hidden="true" />
                <Check v-else-if="copied === 'link'" :size="14" aria-hidden="true" />
                <Link v-else :size="14" aria-hidden="true" />
                {{ copied === "link" ? t.sheet.linkCopied : t.sheet.copyLink }}
              </LtButton>
            </template>
            <template v-else>
              <p class="delivery-state">{{ t.sheet.notPublished }}</p>
              <p class="control-note">{{ t.sheet.notPublishedNote }}</p>
            </template>
            <LtManualCopy v-if="shownLink" :value="shownLink" subject="link" />
          </section>

          <section v-if="!isFile" class="target-control-section">
            <h3 class="control-eyebrow">
              {{ t.sheet.client }} <span class="control-count">{{ CONVERT_TARGETS.length }}</span>
            </h3>
            <div class="target-grid" role="radiogroup" :aria-label="t.sheet.client">
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
              {{ t.sheet.pinnedBefore }} <code>{{ pinned }}</code>{{ t.sheet.pinnedAfter }}
            </p>
          </section>

          <section v-else class="target-control-section">
            <h3 class="control-eyebrow">{{ t.sheet.document }}</h3>
            <p class="control-note">{{ t.sheet.documentNote }}</p>
          </section>

          <section v-if="!isFile" class="target-control-section">
            <h3 class="control-eyebrow">{{ t.sheet.options }}</h3>
            <label class="sheet-toggle">
              <input
                v-model="includeUnsupported"
                type="checkbox"
                name="include-unsupported-proxy"
              />
              {{ t.sheet.includeUnsupported }}
            </label>
          </section>

          <p v-if="!canRender" class="permission-strip">{{ t.sheet.needsAdminRender }}</p>
          <p v-if="actionError" class="sheet-error" role="alert">{{ actionError }}</p>
        </aside>

        <main class="target-output">
          <div class="target-output-toolbar">
            <div class="evidence-rail" role="status" aria-live="polite">
              <template v-if="viewMode === 'document'">
                <span class="evidence-step">
                  <small>{{ isFile ? t.sheet.railDocument : t.sheet.railClient }}</small>
                  <strong>{{ isFile ? t.sheet.railFile : chosenTarget.label }}</strong>
                </span>
                <span class="evidence-arrow" aria-hidden="true">→</span>
                <span class="evidence-step">
                  <small>{{ t.sheet.railOutput }}</small>
                  <strong>{{ renderedLanguageLabel }}</strong>
                </span>
                <span class="evidence-arrow" aria-hidden="true">→</span>
                <span class="evidence-step">
                  <small>{{ t.sheet.railRecord }}</small>
                  <strong>{{ recordId }}</strong>
                </span>
                <template v-if="documentStatus === 'ready'">
                  <span class="evidence-arrow" aria-hidden="true">→</span>
                  <span class="evidence-step">
                    <small>{{ t.sheet.railSize }}</small>
                    <strong>{{ formatBytes(renderedBytes) }}</strong>
                  </span>
                </template>
              </template>
              <template v-else>
                <span class="evidence-step">
                  <small>{{ t.sheet.railAfter }}</small>
                  <strong>{{ t.sheet.railNodes }}</strong>
                </span>
                <span class="evidence-arrow" aria-hidden="true">→</span>
                <span class="evidence-step">
                  <small>{{ t.sheet.railRecord }}</small>
                  <strong>{{ recordId }}</strong>
                </span>
                <template v-if="nodesStatus === 'ready' && preview">
                  <span class="evidence-arrow" aria-hidden="true">→</span>
                  <span class="evidence-step">
                    <small>{{ t.sheet.railKept }}</small>
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

            <div v-if="!isFile" class="output-tabs" role="tablist" :aria-label="t.sheet.evidence">
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
                {{ t.sheet.tabDocument }}
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
                {{ t.sheet.tabNodes }}
              </button>
            </div>

          </div>

          <!-- The one pinned strip: the document's title and its copy action
               stay in view while the page scrolls a long document under
               them. A direct child of the pane, because sticky is bounded by
               its parent and inside the toolbar it had nothing to stick
               along. -->
          <div class="output-heading">
              <div>
                <h3 :id="viewMode === 'document' ? 'target-document-label' : 'target-nodes-label'">
                  <template v-if="viewMode === 'document'">
                    {{ isFile ? t.sheet.renderedDocument : t.sheet.receives(chosenTarget.label) }}
                  </template>
                  <template v-else>
                    {{ isCollection ? t.sheet.mergedNodes : t.sheet.nodesAfter }}
                  </template>
                </h3>
                <p v-if="viewMode === 'document'" class="output-description">
                  <template v-if="documentStatus === 'loading'">
                    {{ t.sheet.generating(isFile ? t.sheet.generatingDocument : chosenTarget.label) }}
                  </template>
                  <template v-else-if="documentStatus === 'ready'">
                    {{ t.sheet.readyMeta(renderedLanguageLabel, formatBytes(renderedBytes), rendered?.content.length ?? 0) }}
                  </template>
                  <template v-else>{{ t.sheet.exactDocument }}</template>
                </p>
                <p v-else class="output-description">{{ t.sheet.redacted }}</p>
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
                  {{ copied === "document" ? t.sheet.documentCopied : t.sheet.copyDocument }}
                </LtButton>
                <LtButton
                  v-else-if="nodesStatus === 'error'"
                  @click="retryCurrent()"
                >
                  <RefreshCw :size="14" aria-hidden="true" /> {{ t.sheet.retry }}
                </LtButton>
              </div>
          </div>

          <!--
            Full width, above the document rather than beside the button: the
            operator is about to press a copy shortcut, and the field holding
            what they are copying should not be a 200px sliver in a header.
          -->
          <LtManualCopy
            v-if="shownDocument"
            :value="shownDocument"
            subject="document"
            multiline
          />

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
                {{ t.sheet.sendAnyway }}
              </button>
            </div>
            <div v-if="documentStatus === 'loading'" class="output-state" role="status">
              <LoaderCircle :size="18" class="spin" aria-hidden="true" />
              <strong>{{ t.sheet.generating(isFile ? t.sheet.generatingDocument : chosenTarget.label) }}</strong>
              <span>{{ t.sheet.notMistaken }}</span>
            </div>
            <div v-else-if="documentStatus === 'error'" class="output-state is-error" role="alert">
              <strong>{{ documentError }}</strong>
              <LtButton @click="retryCurrent()">
                <RefreshCw :size="14" aria-hidden="true" /> {{ t.sheet.retryRender }}
              </LtButton>
            </div>
            <div
              v-else-if="documentStatus === 'ready' && rendered && !rendered.content.length"
              class="output-state is-empty"
              role="status"
            >
              <strong>{{ t.sheet.emptyRender }}</strong>
              <span>{{ t.sheet.nothingToCopy }}</span>
            </div>
            <DocumentView
              v-else-if="documentStatus === 'ready' && rendered"
              class="result-doc"
              :text="rendered.content"
              :language="renderedLanguage"
              :aria-labelledby="'target-document-label'"
            />
            <div v-else class="output-state">
              <strong>{{ t.sheet.noDocumentYet }}</strong>
              <span>{{ t.sheet.chooseClient }}</span>
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
              <strong>{{ t.sheet.previewing }}</strong>
            </div>
            <div v-else-if="nodesStatus === 'error'" class="output-state is-error" role="alert">
              <strong>{{ nodesError }}</strong>
              <LtButton @click="retryCurrent()">
                <RefreshCw :size="14" aria-hidden="true" /> {{ t.sheet.retryPreview }}
              </LtButton>
            </div>
            <template v-else-if="nodesStatus === 'ready' && preview">
              <div class="nodes-summary">
                <strong>{{ t.sheet.kept(preview.response.node_count) }}</strong>
                <span v-if="preview.response.source_node_count !== undefined">
                  {{ t.sheet.ofSource(preview.response.source_node_count) }}
                </span>
                <span v-if="filteredNodeCount">{{ t.sheet.filtered(filteredNodeCount) }}</span>
                <span v-if="preview.response.truncated">{{ t.sheet.truncated }}</span>
              </div>
              <table v-if="preview.response.nodes.length" class="preview-node-table">
                <thead>
                  <tr>
                    <th scope="col">{{ t.sheet.colName }}</th>
                    <th scope="col">{{ t.sheet.colType }}</th>
                    <th scope="col">{{ t.sheet.colEndpoint }}</th>
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
                        {{ node.server || t.sheet.unknownServer }}<template v-if="node.port">:{{ node.port }}</template>
                      </code>
                    </td>
                  </tr>
                </tbody>
              </table>
              <div v-else class="output-state is-empty" role="status">
                <strong>{{ t.sheet.keptNone }}</strong>
                <span>{{ t.sheet.keptNoneNote }}</span>
              </div>
              <p v-if="preview.response.nodes.length > 40" class="result-more">
                {{ t.sheet.firstOf(40, preview.response.nodes.length) }}
              </p>
            </template>
            <div v-else class="output-state">
              <strong>{{ t.sheet.noEvidence }}</strong>
              <LtButton @click="showNodes()">{{ t.sheet.previewNodes }}</LtButton>
            </div>
          </section>
        </main>
      </div>
    </section>
  </div>
</template>
