<script setup lang="ts">
import { CircleAlert, ChevronLeft, Eye, ListOrdered, LoaderCircle, TriangleAlert } from "@lucide/vue";

import CodeEditor from "./CodeEditor.vue";
import CommonSettingsBlock from "./CommonSettings.vue";
import GraphSubscriptionEditor from "./GraphSubscriptionEditor.vue";
import MaskedUrlInput from "./MaskedUrlInput.vue";
import MemberPicker from "./MemberPicker.vue";
import ProcessChain, { type ChainStep } from "./ProcessChain.vue";
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
import type { UseSubscriptions } from "../useSubscriptions";
import type { RecordEditor } from "../useRecordEditor";

/**
 * One record, open for editing: the breadcrumb, the stale-save compare panel,
 * the three tabs, the operator chain and the preview pane beside it.
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
</script>

<template>
  <section class="configuration editor-shell" aria-labelledby="editor-title">
    <nav class="lt-breadcrumb" aria-label="Breadcrumb">
      <button type="button" class="lt-breadcrumb-root" @click="leaveEditor">
        <ChevronLeft :size="14" aria-hidden="true" /> Subscriptions
      </button>
      <span class="lt-breadcrumb-sep" aria-hidden="true">/</span>
      <span class="lt-breadcrumb-here" aria-current="page">
        {{ editingId ? draft.displayName || draft.name || editingId : (isCollection ? "New combination" : "New subscription") }}
      </span>
    </nav>
    <div class="section-heading">
      <div>
        <h2 id="editor-title">
          {{ editingId ? "Edit" : "New" }}
          {{ isCollection ? "combination" : "subscription" }}
          <!-- The draft survives a switch to another lens and back; this
               says so on return, so an edit is not mistaken for saved. -->
          <span v-if="editorDirty" class="editor-dirty" role="status" title="Not saved yet. The draft stays here while you look at another lens.">Unsaved changes</span>
        </h2>
        <p v-if="isCollection">
          Merges several subscriptions and processes the merged result as one.
        </p>
        <p v-else>One source of nodes, processed and served.</p>
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
        <h3 id="conflict-title" class="conflict-panel__title">Your edit was not saved</h3>
      </div>
      <p class="conflict-panel__summary">{{ subs.saveConflict.value.summary }}</p>

      <table v-if="subs.saveConflict.value.changes.length" class="conflict-table">
        <thead>
          <tr>
            <th scope="col">Field</th>
            <th scope="col">When you opened it</th>
            <th scope="col">Now</th>
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
              <span v-if="change.contested" class="conflict-tag">you edited this too</span>
            </th>
            <td class="mono">{{ change.before }}</td>
            <td class="mono">{{ change.after }}</td>
          </tr>
        </tbody>
      </table>

      <div class="conflict-panel__actions">
        <LtButton variant="primary" @click="reopenOnCurrent()">
          Reopen on their version
        </LtButton>
        <LtButton @click="overwriteWithMine()">Replace theirs with mine</LtButton>
        <LtButton @click="discardMyEdit()">Discard my edit</LtButton>
      </div>
      <p class="conflict-panel__note">
        Reopening loses what you typed. Replacing loses what they wrote. Nothing here merges
        the two, because an operator chain merged without being read is a configuration nobody
        wrote.
      </p>
    </section>

    <nav class="editor-tabs" role="tablist" aria-label="Editor sections">
      <button
        v-for="tab in EDITOR_TABS"
        :key="tab.id"
        type="button"
        role="tab"
        class="editor-tab"
        :aria-selected="editorTab === tab.id"
        :data-active="editorTab === tab.id"
        @click="editorTab = tab.id"
      >
        {{ tab.label }}
        <span v-if="tab.id === 'operations' && chainCount" class="editor-tab-count">{{ chainCount }}</span>
        <span
          v-if="errorTab === tab.id && editorTab !== tab.id"
          class="editor-tab-flag"
          :title="draftError"
          aria-label="This section has a problem"
        />
      </button>
    </nav>

    <div class="editor-layout">
    <form class="editor-main" @submit.prevent="submit">
    <fieldset v-show="editorTab === 'display'" class="editor-group">
      <legend>Basics</legend>
      <div class="form-grid">
      <label class="field field-wide">
        <span class="field-label">Name</span>
        <input
          v-model="draft.name"
          type="text"
          autocomplete="off"
          :placeholder="isCollection ? 'Everything' : 'Home nodes'"
        />
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
        <input v-model="draft.displayName" type="text" autocomplete="off" placeholder="Home" />
        <span class="field-optional">Shown in the list instead of the name.</span>
      </label>

      <label class="field">
        <span class="field-label">Tags</span>
        <input
          v-model="tagText"
          type="text"
          autocomplete="off"
          spellcheck="false"
          placeholder="home, backup"
        />
        <span class="field-optional">Used to group, filter, and gather by.</span>
      </label>

      <label class="field field-wide">
        <span class="field-label">Note</span>
        <input v-model="draft.remark" type="text" autocomplete="off" placeholder="Optional" />
      </label>
      </div>
    </fieldset>

    <fieldset v-show="editorTab === 'content'" class="editor-group">
      <legend>{{ isCollection ? "What it gathers" : "Where the nodes come from" }}</legend>
      <div class="form-grid">
      <!-- ── sub: where the nodes come from ─────────────────────────── -->
      <div v-if="!isCollection" class="field field-wide">
        <!-- These cards are a single choice, so they carry the semantics of
             one: a radiogroup whose selected member is announced, not a row
             of buttons distinguishable only by tint. -->
        <div class="source-grid" role="radiogroup" aria-label="Where the nodes come from">
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
        <span class="field-label">Limit to one VPN user</span>
        <input
          v-model="draft.vpnIdentity"
          type="text"
          autocomplete="off"
          spellcheck="false"
          placeholder="Leave empty to include everyone's nodes"
        />
        <span class="field-optional">
          The export returns every node this fleet serves. Naming a proxy user narrows it to
          theirs, useful when one share is meant for one person.
        </span>
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
          <span class="field-label">Provider link</span>
          <MaskedUrlInput
            v-model="draft.url"
            aria-label="Provider link"
            placeholder="The subscription link your provider gave you"
          />
          <span class="field-optional">Shown masked after the host; the record keeps the whole link.</span>
        </div>
        <label class="field">
          <span class="field-label">User agent</span>
          <input v-model="draft.ua" type="text" autocomplete="off" placeholder="Optional" />
          <span class="field-optional">
            Some providers return a different list per client. Set this if yours does.
          </span>
        </label>
      </template>

      <div v-if="!isCollection && draft.source === SOURCE_LOCAL" class="field field-wide">
        <span id="draft-nodes-label" class="field-label">Nodes</span>
        <CodeEditor
          aria-labelledby="draft-nodes-label"
          v-model="draft.content"
          language="plain"
          :rows="12"
          placeholder="Paste node links, a base64 blob, Clash YAML, or sing-box JSON"
        />
        <span class="field-optional">
          Mixed lists work. One node per line for link formats.
        </span>
      </div>

      <!-- ── collection: what it gathers ────────────────────────────── -->
      <template v-if="isCollection">
        <div class="field field-wide">
          <span class="field-label">Choose subscriptions</span>
          <MemberPicker
            :candidates="memberCandidates"
            :selected="draft.members"
            @update:selected="draft.members = $event"
          />
        </div>

        <label class="field field-wide">
          <span class="field-label">…and everything tagged</span>
          <input
            v-model="memberTagText"
            type="text"
            autocomplete="off"
            spellcheck="false"
            placeholder="home, backup"
          />
          <span class="field-optional">
            Gathering by tag means a new subscription joins by being tagged, without editing
            this combination.
          </span>
        </label>

        <div class="field field-wide">
          <span class="field-label">If a member cannot be fetched</span>
          <div class="choice-row">
            <button
              type="button"
              :class="{ 'is-active': draft.failureMode !== FAILURE_SKIP }"
              @click="draft.failureMode = FAILURE_STRICT"
            >
              Fail the whole thing
            </button>
            <button
              type="button"
              :class="{ 'is-active': draft.failureMode === FAILURE_SKIP }"
              @click="draft.failureMode = FAILURE_SKIP"
            >
              Skip it and serve the rest
            </button>
          </div>
          <span class="field-optional">
            Failing is the safer default: serving only the survivors reaches a client as “those
            nodes were removed”, and it deletes them. Skipping is right when one flaky provider
            should not take down a large combination.
          </span>
        </div>
      </template>

      </div>
    </fieldset>

    <fieldset v-show="editorTab === 'content'" class="editor-group">
      <legend>Output</legend>
      <div class="form-grid">
      <label class="field">
        <span class="field-label">Client format</span>
        <select v-model="draft.target" class="select">
          <option value="">Decide from the client that asks</option>
          <option v-for="target in CONVERT_TARGETS" :key="target.id" :value="target.id">
            {{ target.label }}
          </option>
        </select>
        <span class="field-optional">
          Left automatic, Surge gets Surge and Clash gets Clash from one URL.
        </span>
      </label>

      </div>
    </fieldset>

    <div v-show="editorTab === 'operations'" class="editor-block">
      <CommonSettingsBlock :model-value="common" @update:model-value="onCommonChange" />
    </div>

    <div v-show="editorTab === 'operations'" class="editor-block">
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
        <span v-if="isCollection" class="field-optional">
          Each member runs its own operations first; these run over everything merged.
        </span>
    </div>

      <!-- Deliberately not sticky. The frame is a viewport now, so it could
           be, but a bar pinned over a form this tall covers a field for the
           whole time it is being filled in. Save belongs at the end of the
           form; what needed to stay in view was the preview, and that is the
           pane beside it. -->
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
          :title="`Go to the ${EDITOR_TABS.find((t) => t.id === errorTab)?.label} section`"
          @click="editorTab = errorTab || editorTab"
        >
          {{ draftError }}
        </button>
        <button class="button button-secondary" type="button" @click="leaveEditor">Cancel</button>
        <button class="button button-primary" type="submit" :disabled="!canSave || !subs.canMutate.value">
          <LoaderCircle v-if="subs.saving.value" :size="16" class="spin" aria-hidden="true" />
          Save
        </button>
      </div>
    </form>

    <!-- What this record would produce, beside the form that decides it. The
         frame is a viewport now, so the pane can stay in view while a long
         form scrolls under it. Below the breakpoint it becomes the last block
         instead: a sticky column in a 375px frame is a column that covers the
         form. -->
    <aside class="editor-side" aria-labelledby="editor-preview-title">
      <div class="editor-side-head">
        <h3 id="editor-preview-title">Source and result</h3>
        <div class="editor-side-actions">
          <button
            class="button button-secondary"
            type="button"
            :disabled="!canPreviewNow || !explainable || explaining"
            :title="!explainable ? 'Add an operation first; there is nothing to explain' : (draftError || 'Run the chain one operation at a time and say what each one kept')"
            @click="explainDraft()"
          >
            <LoaderCircle v-if="explaining" :size="16" class="spin" aria-hidden="true" />
            <ListOrdered v-else :size="16" aria-hidden="true" />
            Explain chain
          </button>
          <button
            class="button button-secondary"
            type="button"
            :disabled="!canPreviewNow || explaining"
            :title="draftError || 'Run the chain and show the nodes it produces'"
            @click="subs.runPreview(draft)"
          >
            <LoaderCircle v-if="subs.previewing.value && !explaining" :size="16" class="spin" aria-hidden="true" />
            <Eye v-else :size="16" aria-hidden="true" />
            {{ subs.preview.value ? "Refresh" : "Preview" }}
          </button>
        </div>
      </div>

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
      <p v-else class="editor-side-note">
        Nothing run yet. Preview walks the chain over this draft without saving it, so the
        operations can be checked before anyone else sees them.
      </p>
    </aside>
    </div>

    <!-- Leaving with unsaved changes. It lives inside the editor because that
         is the only screen it can be asked from: parked next to the list's
         dialogs it was never rendered while the editor was up, and the exit
         silently did nothing at all. -->
    <LtConfirmDialog
      :open="discarding"
      title="Leave without saving? The changes you made to this record are not stored yet and will be lost."
      verb="Discard changes"
      :names="[draft.displayName || draft.name || (editingId ?? 'this record')]"
      @confirm="cancelEdit()"
      @cancel="discarding = false"
    />
  </section>
</template>
