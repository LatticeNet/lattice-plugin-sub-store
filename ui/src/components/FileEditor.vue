<script setup lang="ts">
import { computed, watch } from "vue";
import { Braces, ChevronLeft, CircleAlert, ClipboardPaste, Eye, FileCode, FileText, Globe, LoaderCircle } from "@lucide/vue";
import { PcCount, PcPanel, PcPanelBody, PcPanelHeader } from "@latticenet/plugin-bridge/chassis";

import {
  FILE_TYPE_CONFIG,
  FILE_TYPE_PLAIN,
  FILE_TYPE_SCRIPT,
  KIND_COLLECTION,
  KIND_FILE,
  SOURCE_LOCAL,
  SOURCE_REMOTE,
} from "../client";
import type { EditorLanguage } from "../codemirror";
import { filePreviewSupport } from "../filePreview";
import { editorLanguageForFileType, editorLanguageLabel } from "../previewLanguage";
import type { FileEditorState, FileEditorTab } from "../useFileEditor";
import type { UseSubscriptions } from "../useSubscriptions";
import CodeEditor from "./CodeEditor.vue";
import DocumentView from "./DocumentView.vue";
import EditorSectionTabs from "./EditorSectionTabs.vue";
import MaskedUrlInput from "./MaskedUrlInput.vue";
import ProcessChain, { type ChainStep } from "./ProcessChain.vue";
import RegexRewriteOffer from "./RegexRewriteOffer.vue";
import LtConfirmDialog from "./lt/LtConfirmDialog.vue";

/**
 * One file, open for editing: what it is called, what it is made of, and what
 * is done to it, beside the document a client would receive.
 *
 * A file is a document the core serves, usually a client configuration the
 * operator has already tuned, whose proxy list is filled in from a source or
 * a combination. It draws the editor and owns nothing else: the state is
 * `useFileEditor`, created by the Records screen, which routes on `editing`
 * the same way it does for the record editor.
 */
const props = defineProps<{ editor: FileEditorState; subs: UseSubscriptions }>();

const {
  draft,
  editingId,
  tagText,
  queryParamText,
  contentLanguageOverride,
  editorTab,
  draftError,
  canSave,
  nodeSources,
  exit,
  cancelEdit,
  submit,
} = props.editor;
const { discarding } = exit;
const editorDirty = exit.dirty;
const leaveEditor = exit.leaveEditor;
const subs = props.subs;

const isPlain = computed(() => draft.value.fileType === FILE_TYPE_PLAIN);
const isScript = computed(() => draft.value.fileType === FILE_TYPE_SCRIPT);

/**
 * Editor highlighting. The file type decides the sensible default (script →
 * JavaScript, config → YAML), and the selector lets the operator override it
 * for the odd file. A JSON template, an INI ruleset. Without inventing new
 * file types. Pure presentation: nothing about the record changes.
 */
const CONTENT_LANGUAGES: ReadonlyArray<{ id: EditorLanguage; label: string }> = [
  { id: "yaml", label: "YAML" },
  { id: "javascript", label: "JavaScript" },
  { id: "json", label: "JSON" },
  { id: "ini", label: "INI" },
  { id: "plain", label: "Plain text" },
];
const autoLanguage = computed<EditorLanguage>(() => editorLanguageForFileType(draft.value.fileType));
const contentLanguage = computed<EditorLanguage>(() => contentLanguageOverride.value || autoLanguage.value);
const contentLanguageLabel = computed(() => editorLanguageLabel(contentLanguage.value));
const isRemote = computed(() => draft.value.source === SOURCE_REMOTE);

/**
 * Whether `preview` will answer for the draft as it stands.
 *
 * The backend refuses a file that needs a node source, a fetch, a program or a
 * chain, because preview is signed for two host calls and each of those is
 * host-capable work. The row renders such a file instead; the editor cannot,
 * because render takes the SAVED record and the editor's whole point is
 * unsaved text, so it says so and names what does work.
 */
const draftPreview = computed(() =>
  filePreviewSupport({
    kind: KIND_FILE,
    file_type: draft.value.fileType,
    node_source: draft.value.nodeSource,
    source: draft.value.source,
    // Source is the current authority. The form retains a previous link so an
    // operator can switch back without retyping it, but local text must not be
    // classified as a fetch because that dormant field is still populated.
    has_url: isRemote.value && !!draft.value.url.trim(),
    step_count: (draft.value.process as unknown[]).length,
  }),
);

/** Preview needs a saved record, a readable draft, and a shape the backend will actually answer for. */
const canPreviewNow = computed(
  () =>
    subs.canPreview.value &&
    !subs.previewing.value &&
    !draftError.value &&
    !!editingId.value &&
    draftPreview.value.supported,
);

/**
 * The stored node source when no candidate matches it, meaning the record it
 * names is gone. A `select` whose value matches no option renders blank, so
 * the field said "nothing chosen" over a file that is in fact pointing at a
 * deleted record, and the first touch of the control would silently rewrite
 * it. The dangling id is offered as its own option instead, marked for what
 * it is.
 */
const danglingNodeSource = computed(() => {
  const id = draft.value.nodeSource.trim();
  if (!id || subs.state.value !== "ready") return "";
  return nodeSources.value.some((item) => item.id === id) ? "" : id;
});

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

/**
 * The editor's sections, split the way the record editor splits them: what
 * the file is called, what it is made of, and what is done to it.
 */
const EDITOR_TABS: { id: FileEditorTab; label: string }[] = [
  { id: "display", label: "Display" },
  { id: "content", label: "Content" },
  { id: "operations", label: "Operations" },
];

/**
 * A script is the whole job, including anything an operator chain would have
 * done. Offering Operations as well would ask which runs first, and the
 * panel is already hidden for scripts. The tab stays for config (node chain)
 * and plain text (response chain).
 */
const editorTabs = computed(() =>
  isScript.value ? EDITOR_TABS.filter((tab) => tab.id !== "operations") : EDITOR_TABS,
);

function setEditorTab(id: string): void {
  if (id === "display" || id === "content" || id === "operations") editorTab.value = id;
}

watch(isScript, (script) => {
  if (script && editorTab.value === "operations") editorTab.value = "content";
});

/**
 * Which section holds the invalid field. A form that says what is wrong and not
 * where is worse behind tabs than in a single scroll: the name lives two tabs
 * away and nothing points at it. Every message except the name one is about
 * what the file is made of, so this is read off the draft rather than by
 * matching message text.
 */
const errorTab = computed<FileEditorTab | "">(() => {
  if (!draftError.value) return "";
  return draft.value.name.trim() ? "content" : "display";
});

/** The chain's size, shown on the tab so it is visible without opening it. */
const chainCount = computed(() => (draft.value.process as unknown[]).length);
</script>

<template>
  <section class="configuration editor-shell" aria-labelledby="file-editor-title">
    <!-- The record editor has one; without it the only way back is the
         Cancel button at the far bottom of a long form. -->
    <nav class="lt-breadcrumb" aria-label="Breadcrumb">
      <button type="button" class="lt-breadcrumb-root" @click="leaveEditor">
        <ChevronLeft :size="14" aria-hidden="true" /> Records
      </button>
      <span class="lt-breadcrumb-sep" aria-hidden="true">/</span>
      <span class="lt-breadcrumb-here" aria-current="page">
        {{ editingId ? draft.displayName || draft.name || editingId : "New file" }}
      </span>
    </nav>
    <div class="section-heading">
      <div>
        <h2 id="file-editor-title" tabindex="-1" data-editor-title>
          {{ editingId ? "Edit" : "New" }} file
          <span v-if="editorDirty" class="editor-dirty" role="status" title="Not saved yet. The draft stays here while you look at another lens.">Unsaved changes</span>
        </h2>
        <p>
          A document served as it is, with its proxy list kept in step with a subscription.
        </p>
      </div>
    </div>

    <div v-if="subs.actionError.value" class="alert" role="alert">
      <CircleAlert :size="16" aria-hidden="true" /> {{ subs.actionError.value }}
    </div>

    <EditorSectionTabs
      :model-value="editorTab"
      label="Editor sections"
      :tabs="editorTabs.map((tab) => ({
        id: tab.id,
        label: tab.label,
        count: tab.id === 'operations' && chainCount ? chainCount : null,
      }))"
      :error-tab="errorTab"
      :error-title="draftError"
      @update:model-value="setEditorTab"
    />

    <!-- Form and evidence side by side, the same layout the subscription
         editor uses. The pane is wider here because the evidence is: a node
         list is short rows, a rendered configuration is 80-column text, and
         squeezing that into a 380px column to match would be shape over
         substance. -->
    <div class="editor-layout" data-pane="wide">
    <form class="editor-main" @submit.prevent="submit">
      <PcPanel v-show="editorTab === 'display'" class="editor-group" role="group" label="Basics">
        <PcPanelHeader title="Basics" description="How the file is named, tagged and listed." />
        <PcPanelBody>
        <div class="form-grid">
          <label class="field field-wide">
            <span class="field-label">Name</span>
            <input v-model="draft.name" type="text" autocomplete="off" placeholder="Phone config" />
            <span class="field-optional">
              <template v-if="editingId">
                Stored as <code>{{ editingId }}</code>. Renaming is safe. A published share keeps
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
        </PcPanelBody>
      </PcPanel>

      <PcPanel v-show="editorTab === 'content'" class="editor-group" role="group" label="What kind of file">
        <PcPanelHeader title="What kind of file" description="Plain text served as written, a template with a proxy list, or a script." />
        <PcPanelBody>
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
        </PcPanelBody>
      </PcPanel>

      <PcPanel v-show="editorTab === 'content'" class="editor-group" role="group" :label="isScript ? 'The program' : isPlain ? 'The text' : 'The template'">
        <PcPanelHeader :title="isScript ? 'The program' : isPlain ? 'The text' : 'The template'" :description="isScript ? 'What runs when a client asks for this file.' : isPlain ? 'What is served, exactly as written.' : 'What is served, with its proxy list filled in from the source below.'" />
        <PcPanelBody>
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
            <!-- A template link can carry a token like a provider link, so
                 it reads masked and shows whole only while edited. -->
            <div class="field field-wide">
              <span class="field-label">Link</span>
              <MaskedUrlInput
                v-model="draft.url"
                aria-label="Link"
                placeholder="Where the template is fetched from"
              />
            </div>
            <label class="field">
              <span class="field-label">User agent</span>
              <input v-model="draft.ua" type="text" autocomplete="off" placeholder="Optional" />
            </label>
          </template>

          <div v-if="isScript || !isRemote" class="field field-wide">
            <span id="file-content-label" class="field-label field-label-row">
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
              aria-labelledby="file-content-label"
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
              <code>$substore.http</code>. Every request leaves through the server's guarded
              egress (private addresses refused, redirects re-checked), capped at 8 requests per
              call. It reaches <code>ProxyUtils</code>, <code>produceArtifact()</code>,
              <code>$arguments</code> and <code>$options</code>, and returns its document by
              assigning <code>$content</code>. Response headers go in
              <code>$options._res.headers</code>.
            </span>
            <span v-else-if="!isPlain" class="field-optional">
              Keep your own rules, DNS and groups. Only <code>proxies</code> is replaced, and any
              group left pointing at a node that is gone gets the new ones instead.
            </span>
          </div>
        </div>
        </PcPanelBody>
      </PcPanel>

      <PcPanel v-if="!isPlain" v-show="editorTab === 'content'" class="editor-group" role="group" label="Where its nodes come from">
        <PcPanelHeader title="Where its nodes come from" description="The subscription whose nodes fill the proxy list." />
        <PcPanelBody>
        <div class="form-grid">
          <label class="field field-wide">
            <span class="field-label">Node source</span>
            <select v-model="draft.nodeSource" class="select">
              <option value="">Leave the configuration exactly as written</option>
              <option v-if="danglingNodeSource" :value="danglingNodeSource">
                {{ danglingNodeSource }} (no longer in the store)
              </option>
              <option v-for="item in nodeSources" :key="item.id" :value="item.id">
                {{ item.display_name || item.name }}
                {{ item.kind === KIND_COLLECTION ? "(combination)" : "" }}
              </option>
            </select>
            <span class="field-optional">
              <template v-if="danglingNodeSource">
                The record this file draws from is not in the store any more, so serving it
                fails. Point it at another source, or clear it to serve the text as written.
              </template>
              <template v-else-if="!nodeSources.length">
                There is nothing to point at yet: create a source first.
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
        </PcPanelBody>
      </PcPanel>

      <PcPanel v-if="isScript" v-show="editorTab === 'content'" class="editor-group" role="group" label="What the script can read">
        <PcPanelHeader title="What the script can read" description="The settings handed to the script, and the request parameters it may read." />
        <PcPanelBody>
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
        </PcPanelBody>
      </PcPanel>

      <!-- A program does the whole job, including anything an operator chain
           would have done. Offering one as well would ask which runs first. -->
      <PcPanel v-if="!isScript" v-show="editorTab === 'operations'" class="editor-group" role="group" label="Operations">
        <PcPanelHeader title="Operations" description="Run in order over the nodes before they are placed into the document.">
          <PcCount v-if="chainCount" :value="chainCount" :label="`${chainCount} operation${chainCount === 1 ? '' : 's'} in the chain`" />
        </PcPanelHeader>
        <PcPanelBody>
        <ProcessChain
          :steps="(draft.process as ChainStep[])"
          :catalog="subs.operators.value"
          :catalog-state="subs.operatorsState.value"
          :chain="isPlain ? 'response' : 'nodes'"
          :heading="isPlain ? 'Document operations' : undefined"
          :empty-copy="isPlain ? 'No operations. The text is served exactly as written.' : undefined"
          @update:steps="draft.process = $event"
        />
        <p class="field-optional">
          <template v-if="isPlain">
            A script receives the document and returns what gets served. The node operators do
            not appear here. The engine skips them for responses.
          </template>
          <template v-else>
            Operations run over the nodes before they are placed into the configuration.
          </template>
        </p>
        </PcPanelBody>
      </PcPanel>

      <!-- A save refused for a pattern the native engine cannot run: next to
           the Save that was refused and under the chain it is about. -->
      <RegexRewriteOffer :refusal="subs.saveRefusal.value" :chain="draft.process" @apply="(chain) => (draft.process = chain)" />

      <div class="editor-actions">
        <span v-if="subs.actionError.value" class="field-error" role="alert">{{ subs.actionError.value }}</span>
        <p v-if="draftError" class="field-error">{{ draftError }}</p>
        <button class="button button-secondary" type="button" @click="leaveEditor">Cancel</button>
        <button class="button button-primary" type="submit" :disabled="!canSave">
          <LoaderCircle v-if="subs.saving.value" :size="16" class="spin" aria-hidden="true" />
          Save
        </button>
      </div>
    </form>

    <PcPanel class="editor-side" role="complementary" label="What a client receives">
      <!-- The chassis header, written out: the rendered document below is
           labelled by this heading, and the component gives its h2 no id. -->
      <header class="pc-panel-header">
        <div><h2 id="file-editor-preview-label">What a client receives</h2></div>
        <div class="pc-panel-header-end">
        <button
          class="button button-secondary"
          type="button"
          :disabled="!canPreviewNow"
          :title="
            editingId
              ? draftError || draftPreview.reason || 'Render this file and show what a client would receive'
              : 'Save it once, then preview'
          "
          @click="subs.runPreview(draft)"
        >
          <LoaderCircle v-if="subs.previewing.value" :size="16" class="spin" aria-hidden="true" />
          <Eye v-else :size="16" aria-hidden="true" />
          {{ subs.preview.value?.document ? "Refresh" : "Preview" }}
        </button>
        </div>
      </header>
      <PcPanelBody>

      <template v-if="subs.preview.value?.document">
        <p class="preview-evidence-meta">
          {{ contentLanguageLabel }} · {{ subs.preview.value.document.length }} characters<span
            v-if="subs.preview.value.truncated"
          > · truncated</span>
        </p>
        <DocumentView
          class="output-area"
          :text="subs.preview.value.document"
          :language="contentLanguage"
          :aria-labelledby="'file-editor-preview-label'"
        />
      </template>
      <p v-else-if="subs.previewError.value" class="editor-side-note is-error" role="alert">
        {{ subs.previewError.value }}
      </p>
      <p v-else-if="editingId && !draftPreview.supported" class="editor-side-note">
        {{ draftPreview.reason }} It is on this file's row menu, and it shows the record as last
        saved rather than the edits here.
      </p>
      <p v-else-if="draftError" class="editor-side-note">{{ draftError }}</p>
      <p v-else class="editor-side-note">
        Nothing run yet. Preview renders this draft without saving it, so the document can be
        read before anyone else receives it.
      </p>
      </PcPanelBody>
    </PcPanel>
    </div>

    <!-- Leaving with unsaved changes. It lives inside the editor because that
         is the only screen it can be asked from. -->
    <LtConfirmDialog
      :open="discarding"
      title="Leave without saving? The changes you made to this file are not stored yet and will be lost."
      verb="Discard changes"
      :names="[draft.displayName || draft.name || (editingId ?? 'this file')]"
      @confirm="cancelEdit()"
      @cancel="discarding = false"
    />
  </section>
</template>
