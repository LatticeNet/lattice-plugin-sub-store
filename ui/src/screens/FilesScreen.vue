<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue";
import {
  CircleAlert,
  CircleCheck,
  ClipboardPaste,
  CopyPlus,
  Ellipsis,
  Eye,
  FileCode,
  FileText,
  Globe,
  Braces,
  LoaderCircle,
  Pencil,
  Plus,
  Share2,
  SquareArrowOutUpRight,
  Trash2,
} from "@lucide/vue";

import {
  FILE_TYPE_CONFIG,
  FILE_TYPE_PLAIN,
  FILE_TYPE_SCRIPT,
  KIND_COLLECTION,
  KIND_FILE,
  KIND_SUB,
  MAX_SUBSCRIPTION_RECORDS,
  SOURCE_LOCAL,
  SOURCE_REMOTE,
  type SubscriptionListItem,
} from "../client";
import { useHost } from "../host";
import { hostOriginFromHash, postNavigate, sharesRoute } from "../navigate";
import {
  draftFromRecord,
  emptyDraft,
  useSubscriptions,
  validateDraft,
  type SubscriptionDraft,
} from "../useSubscriptions";
import LtIconButton from "../components/lt/LtIconButton.vue";
import CodeEditor from "../components/CodeEditor.vue";
import EngineUnavailable from "../components/EngineUnavailable.vue";
import ProcessChain, { type ChainStep } from "../components/ProcessChain.vue";
import type { EditorLanguage } from "../codemirror";

/**
 * The Files tab.
 *
 * A file is a document the core serves — usually a client configuration the
 * operator has already tuned — whose proxy list is filled in from a
 * subscription or a combination. It is the piece that lets nodes change without
 * anyone hand-editing a config, and it shares the subscription store, so
 * everything here runs on methods the signed manifest already declares.
 */

const host = useHost();
const subs = useSubscriptions(host);

const editing = ref(false);
const editingId = ref<string | null>(null);
const draft = ref<SubscriptionDraft>(emptyDraft());
const confirmingDelete = ref<string | null>(null);
const tagText = ref("");
const sharingId = ref<string | null>(null);
/** Which file's overflow menu is open; only ever one, mirroring the list. */
const openFileMenuId = ref("");
function toggleFileMenu(id: string): void {
  openFileMenuId.value = openFileMenuId.value === id ? "" : id;
}

/**
 * Shares are published by the dashboard, not by this frame: the frame can only
 * ask the console to navigate there. The origin is the one the bridge pinned
 * from the frame URL — re-read here rather than trusted from a second source.
 */
const shareOrigin = computed(() => hostOriginFromHash(window.location.hash));

function toggleShare(id: string): void {
  sharingId.value = sharingId.value === id ? null : id;
}

function openShares(recordName: string): void {
  if (!shareOrigin.value) return;
  postNavigate(window, sharesRoute(recordName), shareOrigin.value);
  sharingId.value = null;
  subs.notice.value = "Asked the console to open Networking → Subscription Shares.";
}

const isPlain = computed(() => draft.value.fileType === FILE_TYPE_PLAIN);
const isScript = computed(() => draft.value.fileType === FILE_TYPE_SCRIPT);

/**
 * Editor highlighting. The file type decides the sensible default (script →
 * JavaScript, config → YAML), and the selector lets the operator override it
 * for the odd file — a JSON template, an INI ruleset — without inventing new
 * file types. Pure presentation: nothing about the record changes.
 */
const CONTENT_LANGUAGES: ReadonlyArray<{ id: EditorLanguage; label: string }> = [
  { id: "yaml", label: "YAML" },
  { id: "javascript", label: "JavaScript" },
  { id: "json", label: "JSON" },
  { id: "ini", label: "INI" },
  { id: "plain", label: "Plain text" },
];
const contentLanguageOverride = ref<EditorLanguage | "">("");
const autoLanguage = computed<EditorLanguage>(() => {
  if (isScript.value) return "javascript";
  if (isPlain.value) return "plain";
  return "yaml";
});
const contentLanguage = computed<EditorLanguage>(
  () => contentLanguageOverride.value || autoLanguage.value,
);
const queryParamText = ref("");
const isRemote = computed(() => draft.value.source === SOURCE_REMOTE);
const draftError = computed(() => (editing.value ? validateDraft(draft.value) : ""));
const canSave = computed(() => !draftError.value && !subs.saving.value);
const canPreviewNow = computed(
  () =>
    subs.canPreview.value &&
    !subs.previewing.value &&
    !draftError.value &&
    !!editingId.value &&
    draft.value.source === SOURCE_LOCAL &&
    !draft.value.url.trim() &&
    !isScript.value &&
    !draft.value.nodeSource.trim() &&
    draft.value.process.length === 0,
);

const files = computed(() => subs.items.value.filter((i) => i.kind === KIND_FILE));

/** Anything that resolves to nodes. A file sourcing a file would recurse. */
const nodeSources = computed(() =>
  subs.items.value.filter((i) => (i.kind || KIND_SUB) === KIND_SUB || i.kind === KIND_COLLECTION),
);

const FILE_TYPES = [
  {
    id: FILE_TYPE_CONFIG,
    title: "Client configuration",
    detail: "Mihomo or Clash YAML. Its proxies get replaced from the node source you pick below.",
    icon: FileCode,
  },
  {
    id: FILE_TYPE_PLAIN,
    title: "Plain text",
    detail: "A rule list, a fragment, anything else. Served exactly as written.",
    icon: FileText,
  },
  {
    id: FILE_TYPE_SCRIPT,
    title: "Built by a script",
    detail: "A JavaScript program assembles the whole document from your nodes.",
    icon: Braces,
  },
] as const;

const TEMPLATE_SOURCES = [
  {
    id: SOURCE_LOCAL,
    title: "Text I paste",
    detail: "Kept in this deployment. Edit it here whenever you like.",
    icon: ClipboardPaste,
  },
  {
    id: SOURCE_REMOTE,
    title: "A link",
    detail: "Fetched from a URL, so a template you maintain elsewhere stays the source of truth.",
    icon: Globe,
  },
] as const;

function sourceName(id: string | undefined): string {
  if (!id) return "";
  const found = subs.items.value.find((item) => item.id === id);
  return found ? found.display_name || found.name : id;
}

function describe(item: SubscriptionListItem): string {
  if (item.file_type === FILE_TYPE_PLAIN) return "Plain text";
  const kind = item.file_type === FILE_TYPE_SCRIPT ? "Built by a script" : "Client configuration";
  return item.node_source ? `${kind} · nodes from ${sourceName(item.node_source)}` : `${kind} · no node source`;
}

function startCreate(fileType: string = FILE_TYPE_CONFIG): void {
  subs.clearMessages();
  draft.value = emptyDraft();
  draft.value.kind = KIND_FILE;
  draft.value.fileType = fileType;
  draft.value.source = SOURCE_LOCAL;
  // Most deployments have exactly one thing worth pointing at, and picking it
  // by default is the difference between a form that works and one that saves
  // a config serving no nodes. Plain text has no proxy list to fill, so the
  // default would be a stored setting with no effect.
  draft.value.nodeSource =
    fileType !== FILE_TYPE_PLAIN && nodeSources.value.length === 1 ? nodeSources.value[0]!.id : "";
  tagText.value = "";
  contentLanguageOverride.value = "";
  editingId.value = null;
  editing.value = true;
}

async function startEdit(id: string): Promise<void> {
  subs.clearMessages();
  const record = await subs.get(id);
  if (!record) return;
  draft.value = draftFromRecord(record);
  if (!draft.value.source) draft.value.source = draft.value.url ? SOURCE_REMOTE : SOURCE_LOCAL;
  tagText.value = draft.value.tags.join(", ");
  queryParamText.value = draft.value.queryParams.join(", ");
  contentLanguageOverride.value = "";
  editingId.value = id;
  editing.value = true;
  await host.resize();
}

function cancelEdit(): void {
  editing.value = false;
  editingId.value = null;
  draft.value = emptyDraft();
  subs.preview.value = null;
}

function splitList(text: string): string[] {
  return text
    .split(/[,\n]/)
    .map((entry) => entry.trim())
    .filter(Boolean);
}

async function submit(): Promise<void> {
  draft.value.tags = splitList(tagText.value);
  draft.value.queryParams = splitList(queryParamText.value);
  const ok = await subs.save(draft.value);
  if (ok) cancelEdit();
}

async function confirmDelete(id: string): Promise<void> {
  const ok = await subs.remove(id);
  if (ok) confirmingDelete.value = null;
}

/**
 * Load after the bridge handshake, not on mount: `available()` reads the
 * interfaces the host declares for this frame, and on first paint that has not
 * arrived — so loading in `onMounted` alone silently no-ops and never retries.
 */
async function loadAll(): Promise<void> {
  await subs.load();
  await subs.loadOperators();
}

onMounted(() => {
  if (host.init.value) void loadAll();
});

watch(host.init, (value) => {
  if (value) void loadAll();
});
</script>

<template>
  <EngineUnavailable v-if="host.init.value && !subs.available.value" feature="Files" />

  <template v-else>
    <!-- ── editor ───────────────────────────────────────────────────────── -->
    <section v-if="editing" class="configuration" aria-labelledby="file-editor-title">
      <div class="section-heading">
        <div>
          <h2 id="file-editor-title">{{ editingId ? "Edit" : "New" }} file</h2>
          <p>
            A document served as it is, with its proxy list kept in step with a subscription.
          </p>
        </div>
      </div>

      <div v-if="subs.actionError.value" class="alert" role="alert">
        <CircleAlert :size="16" aria-hidden="true" /> {{ subs.actionError.value }}
      </div>

      <form @submit.prevent="submit">
        <fieldset class="editor-group">
          <legend>Basics</legend>
          <div class="form-grid">
            <label class="field field-wide">
              <span class="field-label">Name</span>
              <input v-model="draft.name" type="text" autocomplete="off" placeholder="Phone config" />
              <span class="field-optional">
                <template v-if="editingId">
                  Stored as <code>{{ editingId }}</code>. Renaming is safe — a published share keeps
                  working.
                </template>
                <template v-else>The only thing you have to fill in.</template>
              </span>
            </label>

            <label class="field">
              <span class="field-label">Display name <span class="field-optional">(optional)</span></span>
              <input v-model="draft.displayName" type="text" autocomplete="off" placeholder="Phone" />
              <span class="field-optional">Shown in the list instead of the name.</span>
            </label>

            <label class="field">
              <span class="field-label">Tags</span>
              <input v-model="tagText" type="text" autocomplete="off" spellcheck="false" placeholder="phone, laptop" />
            </label>

            <label class="field field-wide">
              <span class="field-label">Note</span>
              <input v-model="draft.remark" type="text" autocomplete="off" placeholder="Optional" />
            </label>

            <label class="field field-wide checkbox-field">
              <input v-model="draft.download" type="checkbox" />
              <span>
                <span class="field-label">Save rather than show</span>
                <span class="field-optional">
                  Served with a filename, so a browser downloads it instead of rendering it in a
                  tab. Clients that fetch the URL directly are unaffected.
                </span>
              </span>
            </label>
          </div>
        </fieldset>

        <fieldset class="editor-group">
          <legend>What kind of file</legend>
          <div class="form-grid">
            <div class="field field-wide">
              <div class="source-grid">
                <button
                  v-for="option in FILE_TYPES"
                  :key="option.id"
                  type="button"
                  :class="['source', { 'is-active': draft.fileType === option.id }]"
                  @click="draft.fileType = option.id"
                >
                  <component :is="option.icon" :size="17" aria-hidden="true" />
                  <span class="source-title">{{ option.title }}</span>
                  <span class="source-detail">{{ option.detail }}</span>
                </button>
              </div>
            </div>
          </div>
        </fieldset>

        <fieldset class="editor-group">
          <legend>{{ isScript ? "The program" : isPlain ? "The text" : "The template" }}</legend>
          <div class="form-grid">
            <div v-if="!isScript" class="field field-wide">
              <div class="source-grid">
                <button
                  v-for="option in TEMPLATE_SOURCES"
                  :key="option.id"
                  type="button"
                  :class="['source', { 'is-active': draft.source === option.id }]"
                  @click="draft.source = option.id"
                >
                  <component :is="option.icon" :size="17" aria-hidden="true" />
                  <span class="source-title">{{ option.title }}</span>
                  <span class="source-detail">{{ option.detail }}</span>
                </button>
              </div>
            </div>

            <template v-if="isRemote && !isScript">
              <label class="field field-wide">
                <span class="field-label">Link</span>
                <input
                  v-model="draft.url"
                  type="text"
                  autocomplete="off"
                  spellcheck="false"
                  placeholder="Where the template is fetched from"
                />
              </label>
              <label class="field">
                <span class="field-label">User agent</span>
                <input v-model="draft.ua" type="text" autocomplete="off" placeholder="Optional" />
              </label>
            </template>

            <div v-if="isScript || !isRemote" class="field field-wide">
              <span class="field-label field-label-row">
                {{ isScript ? "Script" : isPlain ? "Text" : "Configuration" }}
                <select
                  v-model="contentLanguageOverride"
                  class="select select-compact"
                  aria-label="Editor highlighting"
                >
                  <option value="">Auto ({{ CONTENT_LANGUAGES.find((l) => l.id === autoLanguage)?.label }})</option>
                  <option v-for="lang in CONTENT_LANGUAGES" :key="lang.id" :value="lang.id">
                    {{ lang.label }}
                  </option>
                </select>
              </span>
              <CodeEditor
                v-model="draft.content"
                :language="contentLanguage"
                :rows="isScript ? 22 : 16"
                :placeholder="
                  isScript
                    ? 'Paste the generator. Call produceArtifact({name, produceType: \'internal\'}) for your nodes and assign the result to $content.'
                    : isPlain
                      ? 'Anything you want served verbatim'
                      : 'Paste the Mihomo or Clash config you already run'
                "
              />
              <span v-if="isScript" class="field-optional">
                Runs in the engine's sandbox: no filesystem, and network only through
                <code>$substore.http</code> — every request leaves through the server's guarded
                egress (private addresses refused, redirects re-checked), capped at 8 requests per
                call. It reaches <code>ProxyUtils</code>, <code>produceArtifact()</code>,
                <code>$arguments</code> and <code>$options</code>, and returns its document by
                assigning <code>$content</code>. Response headers go in
                <code>$options._res.headers</code>.
              </span>
              <span v-else-if="!isPlain" class="field-optional">
                Keep your own rules, DNS and groups. Only <code>proxies</code> is replaced — and any
                group left pointing at a node that is gone gets the new ones instead.
              </span>
            </div>
          </div>
        </fieldset>

        <fieldset v-if="!isPlain" class="editor-group">
          <legend>Where its nodes come from</legend>
          <div class="form-grid">
            <label class="field field-wide">
              <span class="field-label">Node source</span>
              <select v-model="draft.nodeSource" class="select">
                <option value="">Leave the configuration exactly as written</option>
                <option v-for="item in nodeSources" :key="item.id" :value="item.id">
                  {{ item.display_name || item.name }}
                  {{ item.kind === KIND_COLLECTION ? "(combination)" : "" }}
                </option>
              </select>
              <span class="field-optional">
                <template v-if="!nodeSources.length">
                  There is nothing to point at yet — create a subscription on the Subscriptions tab
                  first.
                </template>
                <template v-else-if="isScript">
                  This is what <code>produceArtifact()</code> hands back. Each node keeps the name of
                  the subscription it came from, so a script can filter or rename by source.
                </template>
                <template v-else>
                  Whatever this resolves to at request time becomes the file's proxy list.
                </template>
              </span>
            </label>
          </div>
        </fieldset>

        <fieldset v-if="isScript" class="editor-group">
          <legend>What the script can read</legend>
          <div class="form-grid">
            <label class="field field-wide">
              <span class="field-label">Settings <span class="field-optional">($arguments)</span></span>
              <textarea
                v-model="draft.argumentsText"
                class="code-area"
                rows="4"
                spellcheck="false"
                placeholder="enhanced-mode = fake-ip"
              ></textarea>
              <span class="field-optional">One <code>name = value</code> per line.</span>
            </label>

            <label class="field field-wide">
              <span class="field-label">URL parameters the script may read</span>
              <input
                v-model="queryParamText"
                type="text"
                autocomplete="off"
                spellcheck="false"
                placeholder="enhanced-mode"
              />
              <span class="field-optional">
                A share link is public, so anything in its query is input from whoever holds the
                link. Only the names listed here reach the script; everything else is dropped before
                it runs. Leave empty and the script sees no query at all.
              </span>
            </label>
          </div>
        </fieldset>

        <!-- A program does the whole job, including anything an operator chain
             would have done. Offering one as well would ask which runs first. -->
        <fieldset v-if="!isScript" class="editor-group">
          <legend>Operations</legend>
          <ProcessChain
            :steps="(draft.process as ChainStep[])"
            :catalog="subs.operators.value"
            :chain="isPlain ? 'response' : 'nodes'"
            :heading="isPlain ? 'Document operations' : undefined"
            :empty-copy="isPlain ? 'No operations. The text is served exactly as written.' : undefined"
            @update:steps="draft.process = $event"
          />
          <p class="field-optional">
            <template v-if="isPlain">
              A script receives the document and returns what gets served. The node operators do
              not appear here — the engine skips them for responses.
            </template>
            <template v-else>
              Operations run over the nodes before they are placed into the configuration.
            </template>
          </p>
        </fieldset>

        <div class="editor-actions">
          <p v-if="draftError" class="field-error">{{ draftError }}</p>
          <button
            class="button button-secondary"
            type="button"
            :disabled="!canPreviewNow"
            :title="
              editingId
                ? draftError || (!canPreviewNow ? 'Preview requires a self-contained local config or plain-text file with no node source or operations' : 'Render this file and show what a client would receive')
                : 'Save it once, then preview'
            "
            @click="subs.runPreview(draft)"
          >
            <LoaderCircle v-if="subs.previewing.value" :size="16" class="spin" aria-hidden="true" />
            <Eye v-else :size="16" aria-hidden="true" />
            Preview
          </button>
          <button class="button button-secondary" type="button" @click="cancelEdit">Cancel</button>
          <button class="button button-primary" type="submit" :disabled="!canSave">
            <LoaderCircle v-if="subs.saving.value" :size="16" class="spin" aria-hidden="true" />
            Save
          </button>
        </div>
      </form>

      <div v-if="subs.preview.value?.document" class="preview-summary">
        <p class="mono">
          What a client receives<span v-if="subs.preview.value.truncated"> — truncated</span>
        </p>
        <pre class="output-area mono" tabindex="0">{{ subs.preview.value.document }}</pre>
      </div>
    </section>

    <!-- ── list ─────────────────────────────────────────────────────────── -->
    <section v-else class="configuration" aria-labelledby="files-title">
      <div class="section-heading">
        <div>
          <h2 id="files-title">Files</h2>
          <p>
            A configuration you keep, with its nodes kept current. Publish one from the dashboard
            under Networking to give it a URL.
          </p>
        </div>
        <div class="heading-actions">
          <span class="badge mono">{{ subs.items.value.length }} / {{ MAX_SUBSCRIPTION_RECORDS }}</span>
          <button
            class="button button-primary button-compact"
            type="button"
            :disabled="!subs.canMutate.value || subs.atRecordLimit.value"
            @click="startCreate()"
          >
            <Plus :size="15" aria-hidden="true" /> New
          </button>
        </div>
      </div>

      <div v-if="subs.actionError.value" class="alert" role="alert">
        <CircleAlert :size="16" aria-hidden="true" /> {{ subs.actionError.value }}
      </div>
      <div v-else-if="subs.notice.value" class="alert alert-ok" role="status">
        <CircleCheck :size="16" aria-hidden="true" /> {{ subs.notice.value }}
      </div>

      <p v-if="!host.init.value || subs.state.value === 'loading'" class="skeleton-row">Loading…</p>
      <div v-else-if="subs.loadError.value" class="alert" role="alert">
        <CircleAlert :size="16" aria-hidden="true" /> {{ subs.loadError.value }}
      </div>

      <div v-else-if="!files.length" class="panel-empty panel-empty-stack">
        <p class="panel-empty-copy">
          Paste the Mihomo config you already run. Lattice keeps your rules and groups and replaces
          only the proxy list, from whichever subscription you point it at — so nodes can change
          without you editing anything.
        </p>
        <div class="empty-actions">
          <button
            class="button button-primary"
            type="button"
            :disabled="!subs.canMutate.value"
            @click="startCreate()"
          >
            <FileCode :size="16" aria-hidden="true" /> Add a configuration
          </button>
          <button
            class="button button-secondary"
            type="button"
            :disabled="!subs.canMutate.value"
            @click="startCreate(FILE_TYPE_PLAIN)"
          >
            <FileText :size="16" aria-hidden="true" /> New plain-text file
          </button>
        </div>
      </div>

      <ul v-else class="sub-list">
        <li v-for="item in files" :key="item.id" class="sub-card sub-card-column">
          <div class="sub-card-row">
            <div class="sub-card-main">
              <span class="sub-title">
                <FileText v-if="item.file_type === FILE_TYPE_PLAIN" :size="14" aria-hidden="true" />
                <FileCode v-else :size="14" aria-hidden="true" />
                {{ item.display_name || item.name }}
                <span v-for="tag in item.tags ?? []" :key="tag" class="badge">{{ tag }}</span>
              </span>
              <span class="sub-meta">
                {{ describe(item) }}
                <template v-if="item.step_count">
                  · {{ item.step_count }} operation(s)<template v-if="item.disabled_step_count">
                    , {{ item.disabled_step_count }} off</template>
                </template>
              </span>
            </div>
            <div class="rec-actions" @click.stop>
              <LtIconButton
                :label="`Preview ${item.name}`"
                :disabled="!subs.canPreview.value"
                @click="subs.toggleRowPreview(item.id)"
              >
                <Eye :size="15" aria-hidden="true" />
              </LtIconButton>
              <LtIconButton
                :label="`Edit ${item.name}`"
                :disabled="!subs.canMutate.value || subs.saving.value"
                @click="startEdit(item.id)"
              >
                <Pencil :size="15" aria-hidden="true" />
              </LtIconButton>
              <LtIconButton
                :label="`Share ${item.name}`"
                :disabled="!host.init.value"
                :title="
                  host.init.value
                    ? `Share ${item.name}`
                    : 'Shares are published from the Lattice console — this frame is running standalone'
                "
                @click="toggleShare(item.id)"
              >
                <Share2 :size="15" aria-hidden="true" />
              </LtIconButton>
              <div class="rec-menu-wrap">
                <LtIconButton :label="`More actions for ${item.name}`" @click="toggleFileMenu(item.id)">
                  <Ellipsis :size="15" aria-hidden="true" />
                </LtIconButton>
                <div v-if="openFileMenuId === item.id" class="rec-menu" role="menu">
                  <button
                    type="button"
                    role="menuitem"
                    :disabled="!subs.canMutate.value"
                    @click="openFileMenuId = ''; subs.duplicate(item.id)"
                  >
                    <CopyPlus :size="14" aria-hidden="true" /> Duplicate
                  </button>
                  <button
                    type="button"
                    role="menuitem"
                    class="is-danger"
                    :disabled="!subs.canMutate.value"
                    @click="openFileMenuId = ''; confirmingDelete = item.id"
                  >
                    <Trash2 :size="14" aria-hidden="true" /> Delete
                  </button>
                </div>
              </div>
            </div>
          </div>

          <div v-if="subs.rowPreview.value?.id === item.id" class="row-popover">
            <p v-if="subs.rowPreview.value.loading" class="row-popover-note">
              <LoaderCircle :size="13" class="spin" aria-hidden="true" /> Rendering…
            </p>
            <p v-else-if="subs.rowPreview.value.error" class="row-popover-error" role="alert">
              {{ subs.rowPreview.value.error }}
            </p>
            <template v-else>
              <p class="row-popover-note">
                What a client receives<span v-if="subs.rowPreview.value.truncated"> — truncated</span>
              </p>
              <pre class="row-popover-document mono" tabindex="0">{{ subs.rowPreview.value.document }}</pre>
            </template>
          </div>

          <div v-if="sharingId === item.id" class="row-popover">
            <p class="row-popover-copy">
              Nothing here is reachable until a share is published for it. Shares live in the
              dashboard, under <strong>Networking → Subscription Shares</strong>.
            </p>
            <p class="row-popover-note">Already published? The Shares view shows its link.</p>
            <div v-if="shareOrigin" class="empty-actions">
              <button
                class="button button-primary button-compact"
                type="button"
                @click="openShares(item.name)"
              >
                <SquareArrowOutUpRight :size="13" aria-hidden="true" /> Open Shares view
              </button>
            </div>
            <p v-else class="row-popover-note">
              This frame cannot ask the console to navigate — open Networking → Subscription
              Shares yourself.
            </p>
          </div>

          <div v-if="confirmingDelete === item.id" class="alert" role="alert">
            <span>
              Delete <strong>{{ item.name }}</strong
              >? A published share for it keeps existing and starts failing.
            </span>
            <button class="button button-compact" type="button" @click="confirmingDelete = null">
              Keep
            </button>
            <button
              class="button button-compact destructive"
              type="button"
              @click="confirmDelete(item.id)"
            >
              Delete
            </button>
          </div>
        </li>
      </ul>
    </section>
  </template>
</template>
