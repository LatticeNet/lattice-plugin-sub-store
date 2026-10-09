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
import { t } from "../i18n";
import { editorLanguageForFileType, editorLanguageLabel } from "../previewLanguage";
import type { FileEditorState, FileEditorTab } from "../useFileEditor";
import type { UseSubscriptions } from "../useSubscriptions";
import CodeEditor from "./CodeEditor.vue";
import DocumentView from "./DocumentView.vue";
import EditorSectionTabs from "./EditorSectionTabs.vue";
import MaskedUrlInput from "./MaskedUrlInput.vue";
import RichText from "./RichText.vue";
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
const CONTENT_LANGUAGES: ReadonlyArray<{ id: EditorLanguage; readonly label: string }> = (["yaml", "javascript", "json", "ini", "plain"] as const).map(
  (id) => ({
    id,
    get label() {
      return editorLanguageLabel(id);
    },
  }),
);
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

/* The words of each card come from the message table, read when drawn. */
const FILE_TYPES = computed(() => [
  { id: FILE_TYPE_CONFIG, ...t.fileEditor.types.config, icon: FileCode },
  { id: FILE_TYPE_PLAIN, ...t.fileEditor.types.plain, icon: FileText },
  { id: FILE_TYPE_SCRIPT, ...t.fileEditor.types.script, icon: Braces },
]);

const TEMPLATE_SOURCES = computed(() => [
  { id: SOURCE_LOCAL, ...t.fileEditor.templateSources.local, icon: ClipboardPaste },
  { id: SOURCE_REMOTE, ...t.fileEditor.templateSources.remote, icon: Globe },
]);

/**
 * The editor's sections, split the way the record editor splits them: what
 * the file is called, what it is made of, and what is done to it.
 */
const EDITOR_TABS: { id: FileEditorTab; readonly label: string }[] = [
  { id: "display", get label() { return t.editor.tabs.display; } },
  { id: "content", get label() { return t.editor.tabs.content; } },
  { id: "operations", get label() { return t.editor.tabs.operations; } },
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
    <nav class="lt-breadcrumb" :aria-label="t.editor.breadcrumb">
      <button type="button" class="lt-breadcrumb-root" @click="leaveEditor">
        <ChevronLeft :size="14" aria-hidden="true" /> {{ t.layers.records }}
      </button>
      <span class="lt-breadcrumb-sep" aria-hidden="true">/</span>
      <span class="lt-breadcrumb-here" aria-current="page">
        {{ editingId ? draft.displayName || draft.name || editingId : t.fileEditor.headingNew }}
      </span>
    </nav>
    <div class="section-heading">
      <div>
        <h2 id="file-editor-title" tabindex="-1" data-editor-title>
          {{ editingId ? t.fileEditor.headingEdit : t.fileEditor.headingNew }}
          <span v-if="editorDirty" class="editor-dirty" role="status" :title="t.editor.dirtyTitle">{{ t.editor.dirty }}</span>
        </h2>
        <p>{{ t.fileEditor.lead }}</p>
      </div>
    </div>

    <div v-if="subs.actionError.value" class="alert" role="alert">
      <CircleAlert :size="16" aria-hidden="true" /> {{ subs.actionError.value }}
    </div>

    <EditorSectionTabs
      :model-value="editorTab"
      :label="t.editor.tabsLabel"
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
      <PcPanel v-show="editorTab === 'display'" class="editor-group" role="group" :label="t.editor.basics">
        <PcPanelHeader :title="t.editor.basics" :description="t.fileEditor.basicsDescription" />
        <PcPanelBody>
        <div class="form-grid">
          <label class="field field-wide">
            <span class="field-label">{{ t.editor.name }}</span>
            <input v-model="draft.name" type="text" autocomplete="off" :placeholder="t.fileEditor.namePlaceholder" />
            <span class="field-optional">
              <template v-if="editingId">
                {{ t.editor.storedAsBefore }} <code>{{ editingId }}</code>{{ t.editor.storedAsAfter }}
              </template>
              <template v-else>{{ t.editor.nameRequired }}</template>
            </span>
          </label>

          <label class="field">
            <span class="field-label">{{ t.editor.displayName }} <span class="field-optional">{{ t.editor.optional }}</span></span>
            <input v-model="draft.displayName" type="text" autocomplete="off" :placeholder="t.fileEditor.displayNamePlaceholder" />
            <span class="field-optional">{{ t.editor.displayNameHint }}</span>
          </label>

          <label class="field">
            <span class="field-label">{{ t.editor.tags }}</span>
            <input v-model="tagText" type="text" autocomplete="off" spellcheck="false" :placeholder="t.fileEditor.tagsPlaceholder" />
          </label>

          <label class="field field-wide">
            <span class="field-label">{{ t.editor.note }}</span>
            <input v-model="draft.remark" type="text" autocomplete="off" :placeholder="t.editor.notePlaceholder" />
          </label>

          <label class="field field-wide checkbox-field">
            <input v-model="draft.download" type="checkbox" />
            <span>
              <span class="field-label">{{ t.fileEditor.download }}</span>
              <span class="field-optional">{{ t.fileEditor.downloadHint }}</span>
            </span>
          </label>
        </div>
        </PcPanelBody>
      </PcPanel>

      <PcPanel v-show="editorTab === 'content'" class="editor-group" role="group" :label="t.fileEditor.kindTitle">
        <PcPanelHeader :title="t.fileEditor.kindTitle" :description="t.fileEditor.kindDescription" />
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

      <PcPanel
        v-show="editorTab === 'content'"
        class="editor-group"
        role="group"
        :label="isScript ? t.fileEditor.program : isPlain ? t.fileEditor.text : t.fileEditor.template"
      >
        <PcPanelHeader
          :title="isScript ? t.fileEditor.program : isPlain ? t.fileEditor.text : t.fileEditor.template"
          :description="isScript ? t.fileEditor.programDescription : isPlain ? t.fileEditor.textDescription : t.fileEditor.templateDescription"
        />
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
              <span class="field-label">{{ t.fileEditor.link }}</span>
              <MaskedUrlInput
                v-model="draft.url"
                :aria-label="t.fileEditor.link"
                :placeholder="t.fileEditor.linkPlaceholder"
              />
            </div>
            <label class="field">
              <span class="field-label">{{ t.editor.userAgent }}</span>
              <input v-model="draft.ua" type="text" autocomplete="off" :placeholder="t.editor.notePlaceholder" />
            </label>
          </template>

          <div v-if="isScript || !isRemote" class="field field-wide">
            <span id="file-content-label" class="field-label field-label-row">
              {{ isScript ? t.fileEditor.contentScript : isPlain ? t.fileEditor.contentText : t.fileEditor.contentConfig }}
              <select
                v-model="contentLanguageOverride"
                class="select select-compact"
                :aria-label="t.fileEditor.highlighting"
              >
                <option value="">{{ t.fileEditor.auto(CONTENT_LANGUAGES.find((l) => l.id === autoLanguage)?.label ?? "") }}</option>
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
              :placeholder="isScript ? t.fileEditor.placeholderScript : isPlain ? t.fileEditor.placeholderPlain : t.fileEditor.placeholderConfig"
            />
            <span v-if="isScript" class="field-optional"><RichText :parts="t.fileEditor.scriptHint" /></span>
            <span v-else-if="!isPlain" class="field-optional"><RichText :parts="t.fileEditor.configHint" /></span>
          </div>
        </div>
        </PcPanelBody>
      </PcPanel>

      <PcPanel v-if="!isPlain" v-show="editorTab === 'content'" class="editor-group" role="group" :label="t.fileEditor.nodesFrom">
        <PcPanelHeader :title="t.fileEditor.nodesFrom" :description="t.fileEditor.nodesFromDescription" />
        <PcPanelBody>
        <div class="form-grid">
          <label class="field field-wide">
            <span class="field-label">{{ t.fileEditor.nodeSource }}</span>
            <select v-model="draft.nodeSource" class="select">
              <option value="">{{ t.fileEditor.leaveAsWritten }}</option>
              <option v-if="danglingNodeSource" :value="danglingNodeSource">
                {{ t.fileEditor.dangling(danglingNodeSource) }}
              </option>
              <option v-for="item in nodeSources" :key="item.id" :value="item.id">
                {{ item.display_name || item.name }}
                {{ item.kind === KIND_COLLECTION ? t.fileEditor.combinationSuffix : "" }}
              </option>
            </select>
            <span class="field-optional">
              <template v-if="danglingNodeSource">{{ t.fileEditor.danglingHint }}</template>
              <template v-else-if="!nodeSources.length">{{ t.fileEditor.noSources }}</template>
              <template v-else-if="isScript"><RichText :parts="t.fileEditor.scriptSourceHint" /></template>
              <template v-else>{{ t.fileEditor.sourceHint }}</template>
            </span>
          </label>
        </div>
        </PcPanelBody>
      </PcPanel>

      <PcPanel v-if="isScript" v-show="editorTab === 'content'" class="editor-group" role="group" :label="t.fileEditor.scriptReads">
        <PcPanelHeader :title="t.fileEditor.scriptReads" :description="t.fileEditor.scriptReadsDescription" />
        <PcPanelBody>
        <div class="form-grid">
          <label class="field field-wide">
            <span class="field-label">{{ t.fileEditor.settings }} <span class="field-optional">($arguments)</span></span>
            <textarea
              v-model="draft.argumentsText"
              class="code-area"
              rows="4"
              spellcheck="false"
              placeholder="enhanced-mode = fake-ip"
            ></textarea>
            <span class="field-optional"><RichText :parts="t.fileEditor.argumentsHint" /></span>
          </label>

          <label class="field field-wide">
            <span class="field-label">{{ t.fileEditor.queryParams }}</span>
            <input
              v-model="queryParamText"
              type="text"
              autocomplete="off"
              spellcheck="false"
              placeholder="enhanced-mode"
            />
            <span class="field-optional">{{ t.fileEditor.queryHint }}</span>
          </label>
        </div>
        </PcPanelBody>
      </PcPanel>

      <!-- A program does the whole job, including anything an operator chain
           would have done. Offering one as well would ask which runs first. -->
      <PcPanel v-if="!isScript" v-show="editorTab === 'operations'" class="editor-group" role="group" :label="t.editor.operations">
        <PcPanelHeader :title="t.editor.operations" :description="t.fileEditor.operationsDescription">
          <PcCount v-if="chainCount" :value="chainCount" :label="t.editor.chainCount(chainCount)" />
        </PcPanelHeader>
        <PcPanelBody>
        <ProcessChain
          :steps="(draft.process as ChainStep[])"
          :catalog="subs.operators.value"
          :catalog-state="subs.operatorsState.value"
          :chain="isPlain ? 'response' : 'nodes'"
          :heading="isPlain ? t.fileEditor.documentOperations : undefined"
          :empty-copy="isPlain ? t.fileEditor.plainEmpty : undefined"
          @update:steps="draft.process = $event"
        />
        <p class="field-optional">{{ isPlain ? t.fileEditor.plainOpsHint : t.fileEditor.configOpsHint }}</p>
        </PcPanelBody>
      </PcPanel>

      <!-- A pattern the native engine cannot run, from the moment the record
           opens: next to Save, which it refuses once the chain changes, and
           under the chain it is about. -->
      <RegexRewriteOffer
        :refusal="subs.saveRefusal.value"
        :chain="draft.process"
        @apply="(chain) => (draft.process = chain)"
        @resolved="subs.settleRefusal()"
      />

      <div class="editor-actions">
        <span v-if="subs.actionError.value" class="field-error" role="alert">{{ subs.actionError.value }}</span>
        <p v-if="draftError" class="field-error">{{ draftError }}</p>
        <button class="button button-secondary" type="button" @click="leaveEditor">{{ t.common.cancel }}</button>
        <button class="button button-primary" type="submit" :disabled="!canSave">
          <LoaderCircle v-if="subs.saving.value" :size="16" class="spin" aria-hidden="true" />
          {{ t.editor.save }}
        </button>
      </div>
    </form>

    <PcPanel class="editor-side" role="complementary" :label="t.fileEditor.receives">
      <!-- The chassis header, written out: the rendered document below is
           labelled by this heading, and the component gives its h2 no id. -->
      <header class="pc-panel-header">
        <div><h2 id="file-editor-preview-label">{{ t.fileEditor.receives }}</h2></div>
        <div class="pc-panel-header-end">
        <button
          class="button button-secondary"
          type="button"
          :disabled="!canPreviewNow"
          :title="
            editingId
              ? draftError || draftPreview.reason || t.fileEditor.previewTitle
              : t.fileEditor.saveFirst
          "
          @click="subs.runPreview(draft)"
        >
          <LoaderCircle v-if="subs.previewing.value" :size="16" class="spin" aria-hidden="true" />
          <Eye v-else :size="16" aria-hidden="true" />
          {{ subs.preview.value?.document ? t.editor.refresh : t.editor.preview }}
        </button>
        </div>
      </header>
      <PcPanelBody>

      <template v-if="subs.preview.value?.document">
        <p class="preview-evidence-meta">
          {{ t.fileEditor.meta(contentLanguageLabel, subs.preview.value.document.length, !!subs.preview.value.truncated) }}
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
      <p v-else-if="editingId && !draftPreview.supported" class="editor-side-note">{{ t.fileEditor.unsupported(draftPreview.reason) }}</p>
      <p v-else-if="draftError" class="editor-side-note">{{ draftError }}</p>
      <p v-else class="editor-side-note">{{ t.fileEditor.nothingRun }}</p>
      </PcPanelBody>
    </PcPanel>
    </div>

    <!-- Leaving with unsaved changes. It lives inside the editor because that
         is the only screen it can be asked from. -->
    <LtConfirmDialog
      :open="discarding"
      :title="t.fileEditor.leaveConfirm"
      :verb="t.editor.discardVerb"
      :names="[draft.displayName || draft.name || (editingId ?? t.fileEditor.thisFile)]"
      @confirm="cancelEdit()"
      @cancel="discarding = false"
    />
  </section>
</template>
