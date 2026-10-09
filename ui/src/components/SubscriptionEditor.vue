<script setup lang="ts">
import { CircleAlert, ChevronLeft, Eye, ListOrdered, LoaderCircle, TriangleAlert } from "@lucide/vue";
import { PcCount, PcPanel, PcPanelBody, PcPanelHeader } from "@latticenet/plugin-bridge/chassis";

import CodeEditor from "./CodeEditor.vue";
import EditorSectionTabs from "./EditorSectionTabs.vue";
import CommonSettingsBlock from "./CommonSettings.vue";
import GraphSubscriptionEditor from "./GraphSubscriptionEditor.vue";
import MaskedUrlInput from "./MaskedUrlInput.vue";
import MemberPicker from "./MemberPicker.vue";
import ProcessChain, { type ChainStep } from "./ProcessChain.vue";
import RegexRewriteOffer from "./RegexRewriteOffer.vue";
import SubscriptionPreviewSummary from "./SubscriptionPreviewSummary.vue";
import LtButton from "./lt/LtButton.vue";
import LtConfirmDialog from "./lt/LtConfirmDialog.vue";
import {
  CONVERT_TARGETS,
  FAILURE_SKIP,
  FAILURE_STRICT,
  KIND_COLLECTION,
  SOURCE_LOCAL,
  SOURCE_REMOTE,
  SOURCE_VPN_CORE,
  SOURCE_VPN_CORE_GRAPH,
} from "../client";
import { t } from "../i18n";
import type { UseSubscriptions } from "../useSubscriptions";
import type { RecordEditor } from "../useRecordEditor";

/**
 * One record, open for editing: the breadcrumb, the stale-save compare panel,
 * the three tabs, the operator chain and the preview pane beside it.
 *
 * It is drawn in the chassis vocabulary the list uses: each section is the
 * same bordered panel as the list card, but the section tabs are a quieter
 * underline bar so one click from the list does not put two pill tab shapes
 * on one screen.
 *
 * It draws the editor and owns nothing else. The state is `useRecordEditor`,
 * created by the screen, because `editing` is what the screen routes on: the
 * list and the editor are two states of one screen rather than two screens.
 * The store is handed in for the same reason, so both halves read one instance.
 *
 * This was 430 lines of template and about 300 of script inside a 2,449-line
 * screen with 33 top-level refs, and every question about the editor had to be
 * answered by reading past the list to find its half of the answer.
 */
const props = defineProps<{ editor: RecordEditor; subs: UseSubscriptions }>();

const {
  draft,
  common,
  tagText,
  memberTagText,
  editingId,
  isCollection,
  draftError,
  canSave,
  canPreviewNow,
  previewStepLabel,
  explaining,
  explanation,
  explainable,
  explainDraft,
  previewUpToStep,
  memberCandidates,
  SOURCES,
  MANAGED_TYPES,
  cancelEdit,
  selectSource,
  reloadGraphOptions,
  addGraphRoot,
  removeGraphRoot,
  moveGraphRoot,
  setGraphIdentity,
  discarding,
  editorDirty,
  leaveEditor,
  onCommonChange,
  submit,
  discardMyEdit,
  reopenOnCurrent,
  overwriteWithMine,
  editorTab,
  EDITOR_TABS,
  errorTab,
  chainCount,
} = props.editor;

function setEditorTab(id: string): void {
  if (id === "display" || id === "content" || id === "operations") editorTab.value = id;
}
</script>

<template>
  <section class="configuration editor-shell" aria-labelledby="editor-title">
    <nav class="lt-breadcrumb" :aria-label="t.editor.breadcrumb">
      <button type="button" class="lt-breadcrumb-root" @click="leaveEditor">
        <ChevronLeft :size="14" aria-hidden="true" /> {{ t.layers.records }}
      </button>
      <span class="lt-breadcrumb-sep" aria-hidden="true">/</span>
      <span class="lt-breadcrumb-here" aria-current="page">
        {{ editingId ? draft.displayName || draft.name || editingId : (isCollection ? t.editor.headingNewCombination : t.editor.headingNewSource) }}
      </span>
    </nav>
    <div class="section-heading">
      <div>
        <h2 id="editor-title" tabindex="-1" data-editor-title>
          {{
            editingId
              ? (isCollection ? t.editor.headingEditCombination : t.editor.headingEditSource)
              : (isCollection ? t.editor.headingNewCombination : t.editor.headingNewSource)
          }}
          <!-- The draft survives a switch to another lens and back; this
               says so on return, so an edit is not mistaken for saved. -->
          <span v-if="editorDirty" class="editor-dirty" role="status" :title="t.editor.dirtyTitle">{{ t.editor.dirty }}</span>
        </h2>
        <p v-if="isCollection">{{ t.editor.leadCombination }}</p>
        <p v-else>{{ t.editor.leadSource }}</p>
      </div>
    </div>

    <div v-if="subs.actionError.value" class="alert" role="alert">
      <CircleAlert :size="16" aria-hidden="true" /> {{ subs.actionError.value }}
    </div>

    <!--
      A save refused because the record moved underneath it. Rendered where
      the operator is, above the editor they are still holding, rather than
      as a dialog: their work is on the screen behind it and covering that up
      while asking whose version wins is the wrong way round. Nothing is
      merged and nothing is discarded until they choose.
    -->
    <section v-if="subs.saveConflict.value" class="conflict-panel" role="alert" aria-labelledby="conflict-title">
      <div class="conflict-panel__head">
        <TriangleAlert :size="16" aria-hidden="true" />
        <h3 id="conflict-title" class="conflict-panel__title">{{ t.editor.conflictTitle }}</h3>
      </div>
      <p class="conflict-panel__summary">{{ subs.saveConflict.value.summary }}</p>

      <table v-if="subs.saveConflict.value.changes.length" class="conflict-table">
        <thead>
          <tr>
            <th scope="col">{{ t.editor.conflictField }}</th>
            <th scope="col">{{ t.editor.conflictOpened }}</th>
            <th scope="col">{{ t.editor.conflictNow }}</th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="change in subs.saveConflict.value.changes"
            :key="change.label"
            :class="{ 'is-contested': change.contested }"
          >
            <th scope="row">
              {{ change.label }}
              <span v-if="change.contested" class="conflict-tag">{{ t.editor.conflictContested }}</span>
            </th>
            <td class="mono">{{ change.before }}</td>
            <td class="mono">{{ change.after }}</td>
          </tr>
        </tbody>
      </table>

      <div class="conflict-panel__actions">
        <LtButton variant="primary" @click="reopenOnCurrent()">
          {{ t.editor.conflictReopen }}
        </LtButton>
        <LtButton @click="overwriteWithMine()">{{ t.editor.conflictOverwrite }}</LtButton>
        <LtButton @click="discardMyEdit()">{{ t.editor.conflictDiscard }}</LtButton>
      </div>
      <p class="conflict-panel__note">{{ t.editor.conflictNote }}</p>
    </section>

    <EditorSectionTabs
      :model-value="editorTab"
      :label="t.editor.tabsLabel"
      :tabs="EDITOR_TABS.map((tab) => ({
        id: tab.id,
        label: tab.label,
        count: tab.id === 'operations' && chainCount ? chainCount : null,
      }))"
      :error-tab="errorTab"
      :error-title="draftError"
      @update:model-value="setEditorTab"
    />

    <div class="editor-layout">
    <form class="editor-main" @submit.prevent="submit">
    <PcPanel v-show="editorTab === 'display'" class="editor-group" role="group" :label="t.editor.basics">
      <PcPanelHeader :title="t.editor.basics" :description="t.editor.basicsDescription" />
      <PcPanelBody>
      <div class="form-grid">
      <label class="field field-wide">
        <span class="field-label">{{ t.editor.name }}</span>
        <input
          v-model="draft.name"
          type="text"
          autocomplete="off"
          :placeholder="isCollection ? t.editor.namePlaceholderCombination : t.editor.namePlaceholderSource"
        />
        <span class="field-optional">
          <template v-if="editingId">
            {{ t.editor.storedAsBefore }} <code>{{ editingId }}</code>{{ t.editor.storedAsAfter }}
          </template>
          <template v-else>{{ t.editor.nameRequired }}</template>
        </span>
      </label>

      <label class="field">
        <span class="field-label">{{ t.editor.displayName }} <span class="field-optional">{{ t.editor.optional }}</span></span>
        <input v-model="draft.displayName" type="text" autocomplete="off" :placeholder="t.editor.displayNamePlaceholder" />
        <span class="field-optional">{{ t.editor.displayNameHint }}</span>
      </label>

      <label class="field">
        <span class="field-label">{{ t.editor.tags }}</span>
        <input
          v-model="tagText"
          type="text"
          autocomplete="off"
          spellcheck="false"
          :placeholder="t.editor.tagsPlaceholder"
        />
        <span class="field-optional">{{ t.editor.tagsHint }}</span>
      </label>

      <label class="field field-wide">
        <span class="field-label">{{ t.editor.note }}</span>
        <input v-model="draft.remark" type="text" autocomplete="off" :placeholder="t.editor.notePlaceholder" />
      </label>
      </div>
      </PcPanelBody>
    </PcPanel>

    <PcPanel
      v-show="editorTab === 'content'"
      class="editor-group"
      role="group"
      :label="isCollection ? t.editor.gathers : t.editor.nodesFrom"
    >
      <PcPanelHeader
        :title="isCollection ? t.editor.gathers : t.editor.nodesFrom"
        :description="isCollection ? t.editor.gathersDescription : t.editor.nodesFromDescription"
      />
      <PcPanelBody>
      <div class="form-grid">
      <!-- ── sub: where the nodes come from ─────────────────────────── -->
      <div v-if="!isCollection" class="field field-wide">
        <!-- These cards are a single choice, so they carry the semantics of
             one: a radiogroup whose selected member is announced, not a row
             of buttons distinguishable only by tint. -->
        <div class="source-grid" role="radiogroup" :aria-label="t.editor.nodesFrom">
          <button
            v-for="option in SOURCES"
            :key="option.id"
            type="button"
            role="radio"
            :aria-checked="draft.source === option.id"
            :class="['source', { 'is-active': draft.source === option.id }]"
            @click="selectSource(option.id)"
          >
            <component :is="option.icon" :size="17" aria-hidden="true" />
            <span class="source-title">{{ option.title }}</span>
            <span class="source-detail">{{ option.detail }}</span>
          </button>
        </div>
      </div>

      <label v-if="!isCollection && draft.source === SOURCE_VPN_CORE" class="field field-wide">
        <span class="field-label">{{ t.editor.vpnUser }}</span>
        <input
          v-model="draft.vpnIdentity"
          type="text"
          autocomplete="off"
          spellcheck="false"
          :placeholder="t.editor.vpnUserPlaceholder"
        />
        <span class="field-optional">{{ t.editor.vpnUserHint }}</span>
      </label>

      <GraphSubscriptionEditor
        v-if="!isCollection && draft.source === SOURCE_VPN_CORE_GRAPH"
        :draft="draft"
        :options="subs.graphOptions.value"
        :loading="subs.graphOptionsLoading.value"
        :read-only="!subs.canMutate.value"
        @reload="reloadGraphOptions"
        @identity="setGraphIdentity"
        @add="addGraphRoot"
        @remove="removeGraphRoot"
        @move="moveGraphRoot"
      />

      <template v-if="!isCollection && draft.source === SOURCE_REMOTE">
        <!-- The link carries the provider's token, so it reads masked and
             shows whole only while it is being edited or revealed. -->
        <div class="field field-wide">
          <span class="field-label">{{ t.editor.providerLink }}</span>
          <MaskedUrlInput
            v-model="draft.url"
            :aria-label="t.editor.providerLink"
            :placeholder="t.editor.providerPlaceholder"
          />
          <span class="field-optional">{{ t.editor.providerHint }}</span>
        </div>
        <label class="field">
          <span class="field-label">{{ t.editor.userAgent }}</span>
          <input v-model="draft.ua" type="text" autocomplete="off" :placeholder="t.editor.notePlaceholder" />
          <span class="field-optional">{{ t.editor.userAgentHint }}</span>
        </label>
      </template>

      <div v-if="!isCollection && draft.source === SOURCE_LOCAL" class="field field-wide">
        <span id="draft-nodes-label" class="field-label">{{ t.editor.nodes }}</span>
        <CodeEditor
          aria-labelledby="draft-nodes-label"
          v-model="draft.content"
          language="plain"
          :rows="12"
          :placeholder="t.editor.nodesPlaceholder"
        />
        <span class="field-optional">{{ t.editor.nodesHint }}</span>
      </div>

      <!-- ── collection: what it gathers ────────────────────────────── -->
      <template v-if="isCollection">
        <div class="field field-wide">
          <span class="field-label">{{ t.editor.chooseSubscriptions }}</span>
          <MemberPicker
            :candidates="memberCandidates"
            :selected="draft.members"
            @update:selected="draft.members = $event"
          />
        </div>

        <label class="field field-wide">
          <span class="field-label">{{ t.editor.taggedToo }}</span>
          <input
            v-model="memberTagText"
            type="text"
            autocomplete="off"
            spellcheck="false"
            :placeholder="t.editor.tagsPlaceholder"
          />
          <span class="field-optional">{{ t.editor.taggedHint }}</span>
        </label>

        <div class="field field-wide">
          <span class="field-label">{{ t.editor.memberFails }}</span>
          <div class="choice-row">
            <button
              type="button"
              :class="{ 'is-active': draft.failureMode !== FAILURE_SKIP }"
              @click="draft.failureMode = FAILURE_STRICT"
            >
              {{ t.editor.failAll }}
            </button>
            <button
              type="button"
              :class="{ 'is-active': draft.failureMode === FAILURE_SKIP }"
              @click="draft.failureMode = FAILURE_SKIP"
            >
              {{ t.editor.skipFailed }}
            </button>
          </div>
          <span class="field-optional">{{ t.editor.failHint }}</span>
        </div>
      </template>

      </div>
      </PcPanelBody>
    </PcPanel>

    <PcPanel v-show="editorTab === 'content'" class="editor-group" role="group" :label="t.editor.output">
      <PcPanelHeader :title="t.editor.output" :description="t.editor.outputDescription" />
      <PcPanelBody>
      <div class="form-grid">
      <label class="field">
        <span class="field-label">{{ t.editor.clientFormat }}</span>
        <select v-model="draft.target" class="select">
          <option value="">{{ t.editor.autoFormat }}</option>
          <option v-for="target in CONVERT_TARGETS" :key="target.id" :value="target.id">
            {{ target.label }}
          </option>
        </select>
        <span class="field-optional">{{ t.editor.autoFormatHint }}</span>
      </label>

      </div>
      </PcPanelBody>
    </PcPanel>

    <PcPanel v-show="editorTab === 'operations'" class="editor-group" role="group" :label="t.editor.operations">
      <PcPanelHeader :title="t.editor.operations" :description="t.editor.operationsDescription">
        <PcCount v-if="chainCount" :value="chainCount" :label="t.editor.chainCount(chainCount)" />
      </PcPanelHeader>
      <PcPanelBody>
      <div class="editor-block">
      <CommonSettingsBlock :model-value="common" @update:model-value="onCommonChange" />
      </div>

      <div class="editor-block">
        <ProcessChain
          :steps="(draft.process as ChainStep[])"
          :catalog="subs.operators.value"
          :catalog-state="subs.operatorsState.value"
          :managed-types="MANAGED_TYPES"
          :can-preview-step="canPreviewNow"
          :previewing-step="subs.previewing.value ? subs.previewStep.value : null"
          @update:steps="draft.process = $event"
          @preview-step="previewUpToStep"
        />
        <span v-if="isCollection" class="field-optional">{{ t.editor.collectionOpsHint }}</span>
      </div>
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

      <!-- Sticky so Save stays reachable while a long form scrolls. -->
      <div class="editor-actions">
        <!-- The failure belongs next to the button that produced it: this
             form is long, and a banner at the top is off-screen from the
             click that triggered it. -->
        <span v-if="subs.actionError.value" class="field-error" role="alert">{{ subs.actionError.value }}</span>
        <!-- Clickable, because the field it names is usually on another tab. -->
        <button
          v-else-if="draftError"
          type="button"
          class="field-error field-error-jump"
          :title="t.editor.goToSection(EDITOR_TABS.find((tab) => tab.id === errorTab)?.label ?? '')"
          @click="editorTab = errorTab || editorTab"
        >
          {{ draftError }}
        </button>
        <button class="button button-secondary" type="button" @click="leaveEditor">{{ t.common.cancel }}</button>
        <button class="button button-primary" type="submit" :disabled="!canSave || !subs.canMutate.value">
          <LoaderCircle v-if="subs.saving.value" :size="16" class="spin" aria-hidden="true" />
          {{ t.editor.save }}
        </button>
      </div>
    </form>

    <!-- What this record would produce, beside the form that decides it. The
         frame is a viewport now, so the pane can stay in view while a long
         form scrolls under it. Below the breakpoint it becomes the last block
         instead: a sticky column in a 375px frame is a column that covers the
         form. -->
    <PcPanel class="editor-side" role="complementary" :label="t.editor.sourceAndResult">
      <PcPanelHeader :title="t.editor.sourceAndResult">
          <button
            class="button button-secondary"
            type="button"
            :disabled="!canPreviewNow || !explainable || explaining"
            :title="!explainable ? t.editor.explainNothing : (draftError || t.editor.explainTitle)"
            @click="explainDraft()"
          >
            <LoaderCircle v-if="explaining" :size="16" class="spin" aria-hidden="true" />
            <ListOrdered v-else :size="16" aria-hidden="true" />
            {{ t.editor.explain }}
          </button>
          <button
            class="button button-secondary"
            type="button"
            :disabled="!canPreviewNow || explaining"
            :title="draftError || t.editor.previewTitle"
            @click="subs.runPreview(draft)"
          >
            <LoaderCircle v-if="subs.previewing.value && !explaining" :size="16" class="spin" aria-hidden="true" />
            <Eye v-else :size="16" aria-hidden="true" />
            {{ subs.preview.value ? t.editor.refresh : t.editor.preview }}
          </button>
      </PcPanelHeader>
      <PcPanelBody>

      <!-- What the preview could not do as asked and did instead: a read
           session previewing a saved record's stored source. -->
      <p v-if="subs.preview.value && subs.previewNote.value" class="editor-side-note is-note" role="status">
        {{ subs.previewNote.value }}
      </p>
      <SubscriptionPreviewSummary
        v-if="subs.preview.value"
        :preview="subs.preview.value"
        :step-label="subs.previewStep.value === null ? '' : previewStepLabel"
        :deltas="explanation?.deltas ?? []"
        :dropped-by="explanation?.droppedBy"
      />
      <p v-else-if="subs.previewError.value" class="editor-side-note is-error" role="alert">
        {{ subs.previewError.value }}
      </p>
      <p v-else-if="draftError" class="editor-side-note">{{ draftError }}</p>
      <p v-else class="editor-side-note">{{ t.editor.nothingRun }}</p>
      </PcPanelBody>
    </PcPanel>
    </div>

    <!-- Leaving with unsaved changes. It lives inside the editor because that
         is the only screen it can be asked from: parked next to the list's
         dialogs it was never rendered while the editor was up, and the exit
         silently did nothing at all. -->
    <LtConfirmDialog
      :open="discarding"
      :title="t.editor.leaveConfirm"
      :verb="t.editor.discardVerb"
      :names="[draft.displayName || draft.name || (editingId ?? t.editor.thisRecord)]"
      @confirm="cancelEdit()"
      @cancel="discarding = false"
    />
  </section>
</template>
