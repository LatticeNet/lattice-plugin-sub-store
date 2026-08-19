<script setup lang="ts">
/**
 * TargetSheet, "get this record into a client".
 *
 * The daily job is one sentence long: copy one link for the client I use. The
 * sheet used to answer it with fourteen rows of three icon-only buttons, forty
 * two controls with no labels and no grouping, and it did that for file
 * records too, where the client makes no difference at all.
 *
 * So the shape follows the job. Pick the client once (the choice is
 * remembered for the next record), then act on it with named buttons. Nothing
 * is hidden: every client the core can produce is on screen as a chip, and all
 * three actions stay visible.
 *
 * Honest capability split, mirroring the backend rather than the wish:
 *  - a sub or a combination is produced per client, so the choice is real;
 *  - a FILE is served as the document it is. renderFile ignores the target and
 *    so does its preview, so the sheet offers one link and one document rather
 *    than fourteen copies of the same thing;
 *  - a combination's node preview always runs in URI (the merged list is
 *    parsed back), so the preview result does not claim a client.
 */
import { computed, nextTick, ref, watch } from "vue";
import { Check, Copy, Eye, Link, LoaderCircle, X } from "@lucide/vue";

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
import { isFileRecord } from "../filePreview";
import { useHost } from "../host";
import { safeErrorMessage } from "../subStoreModel";

const props = defineProps<{
  open: boolean;
  /**
   * Inert. It carried a document coordinate for the sheet to open at, back
   * when the host sized the frame to the content and there was no window to
   * centre in. The frame is a viewport now and the scrim is fixed, so the
   * sheet centres on screen and this is ignored. Still accepted so the screens
   * that compute it keep type-checking until that machinery is retired.
   */
  anchorTop?: number;
  /** The row being acted on; null closes the sheet. */
  record: SubscriptionListItem | null;
}>();

const emit = defineEmits<{ (e: "close"): void }>();

const host = useHost();

/**
 * The client chosen last, kept for as long as the screen is mounted rather
 * than reset per sheet. An operator runs one client, and re-picking it for
 * every record is the tax the old row list charged on every visit.
 */
const lastChosen = ref("");

type Action = "" | "link" | "show" | "copy";

const busy = ref<Action>("");
const copied = ref<Action>("");
const error = ref("");
const chosen = ref(CONVERT_TARGETS[0]!.id);
const preview = ref<{ target: string; response: SubscriptionPreviewResponse } | null>(null);
const rendered = ref<{ target: string; content: string; contentType: string } | null>(null);
/** Set when the clipboard refuses, so the link can still be selected by hand. */
const shownLink = ref("");

/**
 * The share core holds for this record, if any. Loaded when the sheet opens.
 * `unknown` is a third state and not the same as "none": a failed lookup told
 * the operator there was no published share, which is a different fact from
 * "the question could not be asked".
 */
const share = ref<SubStoreShareRow | null>(null);
const shareState = ref<"loading" | "ready" | "failed">("loading");

const sheet = ref<HTMLElement | null>(null);

const recordId = computed(() => props.record?.id ?? "");
const recordName = computed(() => props.record?.display_name || props.record?.name || "");
const isFile = computed(() => isFileRecord(props.record));
const isCollection = computed(() => props.record?.kind === KIND_COLLECTION);
const pinned = computed(() => (props.record?.target ?? "").trim());

const chosenTarget = computed(
  () => CONVERT_TARGETS.find((target) => target.id === chosen.value) ?? CONVERT_TARGETS[0]!,
);

watch(
  () => props.open,
  async (open) => {
    if (!open) return;
    error.value = "";
    preview.value = null;
    rendered.value = null;
    copied.value = "";
    busy.value = "";
    shownLink.value = "";
    // A record that pins a client is asking for that one; otherwise the client
    // this operator picked last is a better guess than the head of the list.
    chosen.value =
      CONVERT_TARGETS.find((target) => target.id === pinned.value)?.id ??
      CONVERT_TARGETS.find((target) => target.id === lastChosen.value)?.id ??
      CONVERT_TARGETS[0]!.id;
    void loadShare();
    // Escape is bound to the panel, and a key event only reaches an element
    // that has focus. Without this the shortcut did nothing until the operator
    // had tabbed inside. The opposite of an escape hatch.
    await nextTick();
    sheet.value?.focus();
  },
);

function choose(id: string): void {
  chosen.value = id;
  lastChosen.value = id;
  // The result on screen was produced for the client that was selected when it
  // ran. Keeping it under a new chip would relabel someone else's document.
  preview.value = null;
  rendered.value = null;
  copied.value = "";
  shownLink.value = "";
}

async function loadShare(): Promise<void> {
  share.value = null;
  shareState.value = "loading";
  if (!host.bridge) {
    shareState.value = "failed";
    return;
  }
  try {
    const response = await callMethod<SubStoreSharesResponse>(host.bridge, BINDINGS.sharesList, {}).promise;
    const mine = (response?.shares ?? []).filter((row) => row.subscription_id === recordId.value);
    share.value = mine.find((row) => row.enabled) ?? null;
    shareState.value = "ready";
  } catch {
    // Core-backed method unavailable (older server, restricted operator). The
    // sheet says it could not look rather than claiming there is no share.
    share.value = null;
    shareState.value = "failed";
  } finally {
    await host.resize();
  }
}

/** Absolute when the server knows its public base, path-only otherwise,
 * a path link still works pasted next to the dashboard's own origin. */
const shareBase = computed(() => share.value?.url || share.value?.path || "");

/**
 * The link to hand a client. A file is served as its document, so pinning a
 * client onto its URL would add a parameter the serve path ignores.
 */
const shareUrl = computed(() =>
  shareBase.value
    ? isFile.value
      ? shareBase.value
      : buildShareLink(shareBase.value, chosen.value, includeUnsupported.value)
    : "",
);

/**
 * The produce() flags, mirroring Sub-Store's preview sheet toggles. They ride
 * on the copy and on the link so what you take is what a client would get.
 * A file ignores them (renderFile takes no produce options), so it is not
 * offered them.
 */
const includeUnsupported = ref(false);

function flash(action: Action): void {
  copied.value = action;
  window.setTimeout(() => {
    if (copied.value === action) copied.value = "";
  }, 2000);
}

async function copyLink(): Promise<void> {
  if (!shareUrl.value || busy.value) return;
  shownLink.value = "";
  error.value = "";
  try {
    await navigator.clipboard.writeText(shareUrl.value);
    flash("link");
  } catch {
    // Same rule as document copies: never lose the goods to a clipboard
    // denial. Show the link so it can be selected by hand.
    shownLink.value = shareUrl.value;
    await host.resize();
  }
}

/**
 * The node list a client would receive, after the chain.
 *
 * Not offered for a file: a file is a document, and its backend preview
 * returns that document, which is what `showDocument` already renders.
 */
async function runPreview(): Promise<void> {
  if (!host.bridge || busy.value) return;
  busy.value = "show";
  error.value = "";
  rendered.value = null;
  try {
    // No options here on purpose: preview summarizes the node list after the
    // chain, before produce() runs, so produce flags cannot change what it
    // shows. Sending them anyway would imply they do.
    const response = await callMethod<SubscriptionPreviewResponse>(host.bridge, BINDINGS.subPreview, {
      subscription_id: recordId.value,
      target: chosen.value,
    }).promise;
    preview.value = { target: chosen.value, response };
  } catch (cause) {
    preview.value = null;
    error.value = safeErrorMessage(cause, `Preview for ${chosenTarget.value.label} failed`);
  } finally {
    busy.value = "";
    await host.resize();
  }
}

/**
 * The document itself, through render: the one call that produces what a
 * client actually receives, for every kind of record including the files
 * preview refuses.
 */
async function renderDocument(copy: boolean): Promise<void> {
  if (!host.bridge || busy.value) return;
  busy.value = copy ? "copy" : "show";
  error.value = "";
  try {
    const response = await callMethod<SubscriptionRenderResponse>(host.bridge, BINDINGS.subRender, {
      subscription_id: recordId.value,
      format: "plain",
      // Explicit target: the caller who names a client means that client,
      // record pin or not. The same contract as Sub-Store's ?target= URLs.
      // A file ignores it, and sending it would not make it apply.
      ...(isFile.value ? {} : { target: chosen.value, options: { "include-unsupported-proxy": includeUnsupported.value } }),
    }).promise;
    const content = response?.content ?? "";
    if (!content) throw new Error("The render returned no document");
    // The document is shown either way. A clipboard write can fail for
    // reasons that have nothing to do with the configuration. An unfocused
    // document, a denied permission, and losing a good render to that would
    // make the operator run it again to get back what is already in hand.
    rendered.value = {
      target: isFile.value ? "" : chosen.value,
      content,
      contentType: response.content_type,
    };
    preview.value = null;
    if (!copy) return;
    try {
      await navigator.clipboard.writeText(content);
      flash("copy");
    } catch {
      error.value = "The clipboard is unavailable. The document is below, select it to copy.";
    }
  } catch (cause) {
    error.value = safeErrorMessage(
      cause,
      isFile.value ? "Could not render this file" : `Could not render the ${chosenTarget.value.label} document`,
    );
  } finally {
    busy.value = "";
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
        <div class="sheet-headings">
          <h2 class="sheet-title">Preview / copy</h2>
          <p class="sheet-sub" :title="recordName">{{ recordName }}</p>
        </div>
        <button type="button" class="sheet-close" aria-label="Close" @click="close">
          <X :size="16" aria-hidden="true" />
        </button>
      </header>

      <p v-if="shareState === 'loading'" class="sheet-note">Checking whether this record is published…</p>
      <p v-else-if="shareState === 'failed'" class="sheet-note">
        Could not read the published shares, so this sheet cannot say whether there is a stable
        link. The copies below are one-off documents and are unaffected.
      </p>
      <p v-else-if="share" class="sheet-note sheet-note-share">
        Published as <strong>/{{ share.slug }}</strong>.
        <template v-if="isFile">That URL serves this document.</template>
        <template v-else>Copy link gives you that URL pinned to <strong>{{ chosenTarget.label }}</strong>.</template>
      </p>
      <p v-else class="sheet-note">
        No published share yet, so there are no stable links, copies below are one-off documents.
        Publish this record from the row menu to hand clients a URL.
      </p>

      <!-- ── a file: one document, no client to pick ───────────────────── -->
      <template v-if="isFile">
        <p class="sheet-caption">
          A file is served as the document it is, so there is nothing to choose: the client target
          does not change what this record produces.
        </p>
      </template>

      <!-- ── a sub or a combination: the client decides the document ───── -->
      <template v-else>
        <p v-if="pinned" class="sheet-note">
          Served URLs for this record always produce <strong>{{ pinned }}</strong> (its pinned
          target). The link and the copy below name their client explicitly, which overrides the
          pin.
        </p>

        <fieldset class="target-picker">
          <legend class="target-legend">Client</legend>
          <div class="target-grid" role="radiogroup" aria-label="Client">
            <button
              v-for="target in CONVERT_TARGETS"
              :key="target.id"
              type="button"
              role="radio"
              :aria-checked="chosen === target.id"
              class="target-chip"
              :class="{ 'is-active': chosen === target.id }"
              @click="choose(target.id)"
            >
              <span class="target-chip-name">{{ target.label }}</span>
              <span class="target-chip-produces">{{ target.produces }}</span>
            </button>
          </div>
        </fieldset>

        <label class="sheet-toggle">
          <input v-model="includeUnsupported" type="checkbox" />
          Include protocols {{ chosenTarget.label }} does not support
        </label>
      </template>

      <div class="sheet-actions">
        <LtButton
          v-if="share"
          variant="primary"
          :disabled="!!busy"
          @click="copyLink()"
        >
          <Check v-if="copied === 'link'" :size="14" aria-hidden="true" />
          <Link v-else :size="14" aria-hidden="true" />
          {{ copied === "link" ? "Link copied" : "Copy link" }}
        </LtButton>

        <LtButton v-if="isFile" :disabled="!!busy" @click="renderDocument(false)">
          <LoaderCircle v-if="busy === 'show'" :size="14" class="spin" aria-hidden="true" />
          <Eye v-else :size="14" aria-hidden="true" />
          Show document
        </LtButton>
        <LtButton v-else :disabled="!!busy" @click="runPreview()">
          <LoaderCircle v-if="busy === 'show'" :size="14" class="spin" aria-hidden="true" />
          <Eye v-else :size="14" aria-hidden="true" />
          Preview nodes
        </LtButton>

        <LtButton :variant="share ? 'ghost' : 'primary'" :disabled="!!busy" @click="renderDocument(true)">
          <LoaderCircle v-if="busy === 'copy'" :size="14" class="spin" aria-hidden="true" />
          <Check v-else-if="copied === 'copy'" :size="14" aria-hidden="true" />
          <Copy v-else :size="14" aria-hidden="true" />
          {{ copied === "copy" ? "Document copied" : "Copy document" }}
        </LtButton>
      </div>

      <p v-if="error" class="sheet-error" role="alert">{{ error }}</p>

      <p v-if="shownLink" class="sheet-note sheet-note-share">
        The clipboard is unavailable, select the link by hand:
        <code class="share-link">{{ shownLink }}</code>
      </p>

      <section v-if="preview" class="sheet-result">
        <h3 class="result-title">
          <span>{{ preview.response.node_count }} node(s)</span>
          <span v-if="preview.response.source_node_count !== undefined" class="result-sub">
            of {{ preview.response.source_node_count }} before the chain
          </span>
        </h3>
        <!-- A combination's preview is always merged and parsed back in URI, so
             naming the chosen client over this list would be a claim the
             backend did not make. -->
        <p class="result-note">
          <template v-if="isCollection">
            The merged node list. A combination previews the same nodes for every client.
          </template>
          <template v-else>The nodes {{ preview.target }} would receive.</template>
        </p>
        <ul class="node-list">
          <li
            v-for="(node, index) in preview.response.nodes.slice(0, 40)"
            :key="`${node.name}:${index}`"
            class="node-row"
          >
            <span class="node-name" :title="node.name">{{ node.name }}</span>
            <span class="node-meta">{{ node.type }}<template v-if="node.server"> · {{ node.server }}</template></span>
          </li>
        </ul>
        <p v-if="!preview.response.nodes.length" class="result-note">
          The chain kept no nodes. A client subscribing to this now receives an empty list.
        </p>
        <p v-else-if="preview.response.nodes.length > 40" class="result-more">
          Showing the first 40 of {{ preview.response.nodes.length }}.
        </p>
      </section>

      <section v-else-if="rendered" class="sheet-result">
        <h3 class="result-title">
          <span>{{ rendered.target || "What a client receives" }}</span>
          <span class="result-sub">{{ rendered.content.length }} bytes · {{ rendered.contentType }}</span>
        </h3>
        <pre class="result-doc" tabindex="0">{{ rendered.content }}</pre>
      </section>
    </section>
  </div>
</template>
