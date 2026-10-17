<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { CircleAlert, ChevronLeft, Eye, ListOrdered, LoaderCircle, ShieldAlert, TriangleAlert } from "@lucide/vue";
import { PcCount, PcNotice, PcPanel, PcPanelBody, PcPanelHeader } from "@latticenet/plugin-bridge/chassis";

import BindPreviewList from "./BindPreviewList.vue";
import CodeEditor from "./CodeEditor.vue";
import DocumentView from "./DocumentView.vue";
import FleetSelectionEditor from "./FleetSelectionEditor.vue";
import IdentityPicker from "./IdentityPicker.vue";
import LineDetailDrawer from "./LineDetailDrawer.vue";
import EditorSectionTabs from "./EditorSectionTabs.vue";
import CommonSettingsBlock from "./CommonSettings.vue";
import GraphSubscriptionEditor from "./GraphSubscriptionEditor.vue";
import MaskedUrlInput from "./MaskedUrlInput.vue";
import MemberPicker from "./MemberPicker.vue";
import ProcessChain, { type ChainStep } from "./ProcessChain.vue";
import RegexRewriteOffer from "./RegexRewriteOffer.vue";
import ScriptNetworkNotice from "./ScriptNetworkNotice.vue";
import SubscriptionPreviewSummary from "./SubscriptionPreviewSummary.vue";
import WithheldNotice from "./WithheldNotice.vue";
import LtButton from "./lt/LtButton.vue";
import LtConfirmDialog from "./lt/LtConfirmDialog.vue";
import {
  CONVERT_TARGETS,
  ERROR_CODES,
  FAILURE_SKIP,
  FAILURE_STRICT,
  KIND_COLLECTION,
  SOURCE_FLEET,
  SOURCE_LOCAL,
  SOURCE_REMOTE,
  SOURCE_VPN_CORE,
  SOURCE_VPN_CORE_GRAPH,
  errorCodeOf,
} from "../client";
import { DEFAULT_PROBE_FAILURES, MAX_PROBE_FAILURES, MIN_PROBE_FAILURES, type FleetOptions } from "../fleet";
import { t } from "../i18n";
import { recordName, shortRevision } from "../planFlow";
import { editorLanguageForRender } from "../previewLanguage";
import { refusedStepIn, scriptNetworkRefusal } from "../scriptNetwork";
import type { UseSubscriptions } from "../useSubscriptions";
import { isLegacySource, type RecordEditor } from "../useRecordEditor";

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
  storedSource,
  fleet,
  editingStaged,
  stepToOpen,
  openStep,
  startEdit,
} = props.editor;

function setEditorTab(id: string): void {
  if (id === "display" || id === "content" || id === "operations") editorTab.value = id;
}

// ── the fleet source (design 28 S2, s2-plan section 11) ──────────────────────

const isFleet = computed(() => !isCollection.value && draft.value.source === SOURCE_FLEET);
const legacyChosen = computed(() => !isCollection.value && isLegacySource(draft.value.source));

/** The line the drawer shows, opened from the sample or from a bind entry. */
const openLine = ref<string | null>(null);

/** From the selection's proof line to the identity preview, which sits below the form. */
function jumpToIdentity(): void {
  const heading = document.getElementById("identity-preview-title");
  heading?.scrollIntoView({ block: "start" });
  heading?.focus();
}

/** The preview pane's error, as a code the editor knows. */
const previewCode = computed(() => errorCodeOf(new Error(props.subs.previewError.value || "")));
const withheld = computed(
  () => previewCode.value === ERROR_CODES.ownerCredentialsWithheld || (legacyChosen.value && storedSource.value === draft.value.source && !!editingId.value),
);

/**
 * A fleet render's script asked for the network (S2-SR-2): the preview pane
 * says which step and offers it, and the chain marks it. The refusal names
 * the record it is about; one that names another record (a member's chain
 * read through this combination) is offered as that record's steps.
 */
const networkRefusal = computed(() => (previewCode.value === ERROR_CODES.fleetScriptNetworkUnavailable ? scriptNetworkRefusal(props.subs.previewError.value) : null));
const refusalElsewhere = computed(() => {
  const named = networkRefusal.value?.record ?? "";
  return !!named && named !== (editingId.value ?? "") && named !== draft.value.id;
});
const refusedStep = computed(() => {
  const refusal = networkRefusal.value;
  if (!refusal) return null;
  return refusalElsewhere.value ? refusal.step : refusedStepIn(refusal, draft.value.process);
});
const refusalRecordName = computed(() => {
  if (!refusalElsewhere.value) return t.plans.scriptNetwork.thisRecord;
  const id = networkRefusal.value!.record;
  return recordName(props.subs.items.value.find((item) => item.id === id), id);
});
/** Another record's steps open in place of this draft only when nothing here is unsaved. */
const canOpenRefusedStep = computed(() => !refusalElsewhere.value || (!editorDirty.value && props.subs.items.value.some((item) => item.id === networkRefusal.value?.record)));
async function openRefusedStep(position: number | null): Promise<void> {
  if (refusalElsewhere.value) {
    const id = networkRefusal.value?.record ?? "";
    if (!id || editorDirty.value) return;
    await startEdit(id);
    if (editingId.value !== id) return;
  }
  if (position) openStep(position);
  else editorTab.value = "operations";
}

/**
 * The page's error, unless it is a refusal the preview pane already states
 * in its own notice (the withheld preview, the script network): the same
 * refusal twice, once raw, reads as a failure when it is a rule.
 */
const actionError = computed(() => {
  const message = props.subs.actionError.value;
  if ((withheld.value || networkRefusal.value) && message && message === props.subs.previewError.value) return "";
  return message;
});

/** The fleet preview's document: the record's client format over placeholder credentials. */
const placeholderDoc = computed(() => {
  const preview = props.subs.preview.value;
  if (!preview?.placeholder_credentials) return null;
  const target = CONVERT_TARGETS.find((entry) => entry.id === (draft.value.target || "URI")) ?? CONVERT_TARGETS[0]!;
  return {
    text: preview.document ?? "",
    label: target.label,
    language: editorLanguageForRender({ contentType: "", produces: target.produces }),
  };
});

// ── binding rules ─────────────────────────────────────────────────────────────

/** The usage cap's units: MB, GB and TB of the locale's byte scale, binary. */
const USAGE_UNITS = [2, 3, 4] as const;
const usageAmount = ref("");
const usageUnit = ref<number>(3);

function syncUsage(options: FleetOptions | undefined): void {
  const bytes = options?.usage_exclusion?.max_bytes_per_line ?? 0;
  if (!bytes) return;
  for (const unit of [...USAGE_UNITS].reverse()) {
    const size = 1024 ** unit;
    if (bytes % size === 0) {
      usageUnit.value = unit;
      usageAmount.value = String(bytes / size);
      return;
    }
  }
  const megabyte = 1024 ** 2;
  usageUnit.value = 2;
  usageAmount.value = String(bytes / megabyte);
}
watch(() => draft.value.fleet, syncUsage, { immediate: true });

function setRule(patch: Partial<FleetOptions>): void {
  fleet.setRules({ ...fleet.rules.value, ...patch });
}

function setProbe(enabled: boolean): void {
  setRule({ probe_exclusion: enabled ? { enabled: true, consecutive_failures: fleet.rules.value.probe_exclusion?.consecutive_failures ?? DEFAULT_PROBE_FAILURES } : undefined });
}

function setProbeFailures(raw: string): void {
  const n = Number(raw);
  setRule({ probe_exclusion: { enabled: true, consecutive_failures: raw.trim() === "" ? DEFAULT_PROBE_FAILURES : n } });
}

function usageBytes(): number {
  const amount = Number(usageAmount.value);
  if (!Number.isFinite(amount) || amount <= 0) return 0;
  return Math.round(amount * 1024 ** usageUnit.value);
}

function setUsage(enabled: boolean): void {
  if (!enabled) {
    setRule({ usage_exclusion: undefined });
    return;
  }
  if (!usageAmount.value) usageAmount.value = "50";
  setRule({ usage_exclusion: { enabled: true, max_bytes_per_line: usageBytes() } });
}

function onUsageInput(): void {
  if (fleet.rules.value.usage_exclusion?.enabled) setRule({ usage_exclusion: { enabled: true, max_bytes_per_line: usageBytes() } });
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

    <!-- Why the editor opened, when another surface opened it for a reason (lane 6b: "Remove from <combination>"). -->
    <slot name="notice" />

    <!-- The staged revision is what this editor opened, and what a save builds on. -->
    <PcNotice v-if="editingStaged" tone="info" data-testid="editor-staged">
      {{ editingStaged.live ? t.plans.stagedSave.editing(shortRevision(editingStaged.revision), shortRevision(editingStaged.live)) : t.plans.stagedSave.editingNew(shortRevision(editingStaged.revision)) }}
    </PcNotice>

    <div v-if="actionError" class="alert" role="alert">
      <CircleAlert :size="16" aria-hidden="true" /> {{ actionError }}
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
            :aria-disabled="option.disabledReason ? 'true' : undefined"
            :class="['source', { 'is-active': draft.source === option.id, 'is-disabled': !!option.disabledReason }]"
            :data-testid="`source-${option.id}`"
            @click="selectSource(option.id)"
          >
            <component :is="option.icon" :size="17" aria-hidden="true" />
            <span class="source-title">
              {{ option.title }}
              <span v-if="option.legacy" class="source-badge" data-testid="source-owner-credentials">
                <ShieldAlert :size="12" aria-hidden="true" /> {{ t.fleetEditor.ownerCredentials }}
              </span>
            </span>
            <span class="source-detail">{{ option.disabledReason || option.detail }}</span>
          </button>
        </div>
        <span v-if="isFleet" class="field-optional" data-testid="fleet-identity-proof">{{ t.fleetEditor.identityPerShare }}</span>
        <span v-else-if="legacyChosen" class="field-optional source-pointer" data-testid="legacy-pointer">{{ t.fleetEditor.legacyPointer }}</span>
      </div>

      <div v-if="isFleet" class="field field-wide">
        <FleetSelectionEditor
          :fleet="fleet"
          :process="draft.process"
          :read-only="!subs.canMutate.value"
          :preview-nodes="subs.preview.value?.placeholder_credentials ? subs.preview.value.source_node_count ?? null : null"
          @update:process="draft.process = $event"
          @open-line="openLine = $event"
          @open-operations="editorTab = 'operations'"
          @jump-identity="jumpToIdentity()"
        />
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

    <!-- Binding rules: on a fleet record, and on a combination that gathers
         one, whose shares follow the combination's own rules. -->
    <PcPanel
      v-if="fleet.fleetBound.value"
      v-show="editorTab === 'content'"
      class="editor-group"
      role="group"
      :label="t.fleetEditor.rules.title"
      data-testid="binding-rules"
    >
      <PcPanelHeader :title="t.fleetEditor.rules.title" :description="t.fleetEditor.rules.description" />
      <PcPanelBody>
        <p v-if="isCollection" class="field-optional rules-proof" data-testid="rules-collection-proof">{{ t.fleetEditor.rules.collectionProof }}</p>
        <div class="rules">
          <div class="rules-row">
            <label class="field checkbox-field">
              <input
                type="checkbox"
                :disabled="!subs.canMutate.value"
                :checked="!!fleet.rules.value.ddns_dial"
                data-testid="rule-ddns"
                @change="setRule({ ddns_dial: ($event.target as HTMLInputElement).checked || undefined })"
              />
              <span class="rules-text">
                <span class="field-label">{{ t.fleetEditor.rules.ddns }}</span>
                <span class="field-optional">{{ t.fleetEditor.rules.ddnsProof }}</span>
              </span>
            </label>
          </div>

          <div class="rules-row">
            <label class="field checkbox-field">
              <input
                type="checkbox"
                :disabled="!subs.canMutate.value"
                :checked="!!fleet.rules.value.probe_exclusion?.enabled"
                data-testid="rule-probe"
                @change="setProbe(($event.target as HTMLInputElement).checked)"
              />
              <span class="rules-text">
                <span class="field-label">{{ t.fleetEditor.rules.probe }}</span>
                <span v-if="fleet.probeMissing.value" class="field-optional">{{ t.fleetEditor.rules.probeUnavailable }}</span>
              </span>
            </label>
            <label v-if="fleet.rules.value.probe_exclusion?.enabled" class="field rules-sub">
              <span class="field-label">{{ t.fleetEditor.rules.probeFailures }}</span>
              <input
                type="number"
                class="rules-number"
                :min="MIN_PROBE_FAILURES"
                :max="MAX_PROBE_FAILURES"
                step="1"
                :disabled="!subs.canMutate.value"
                :value="fleet.rules.value.probe_exclusion?.consecutive_failures ?? DEFAULT_PROBE_FAILURES"
                data-testid="rule-probe-failures"
                @input="setProbeFailures(($event.target as HTMLInputElement).value)"
              />
              <span class="field-optional">{{ t.fleetEditor.rules.probeHint }}</span>
            </label>
          </div>

          <div class="rules-row">
            <label class="field checkbox-field">
              <input
                type="checkbox"
                :disabled="!subs.canMutate.value"
                :checked="!!fleet.rules.value.usage_exclusion?.enabled"
                data-testid="rule-usage"
                @change="setUsage(($event.target as HTMLInputElement).checked)"
              />
              <span class="rules-text">
                <span class="field-label">{{ t.fleetEditor.rules.usage }}</span>
                <span class="field-optional">{{ t.fleetEditor.rules.usageHint }}</span>
              </span>
            </label>
            <div v-if="fleet.rules.value.usage_exclusion?.enabled" class="rules-sub rules-usage">
              <label class="field">
                <span class="field-label">{{ t.fleetEditor.rules.usageAmount }}</span>
                <input
                  v-model="usageAmount"
                  type="number"
                  class="rules-number"
                  min="1"
                  step="1"
                  inputmode="decimal"
                  :disabled="!subs.canMutate.value"
                  data-testid="rule-usage-amount"
                  @input="onUsageInput()"
                />
              </label>
              <label class="field">
                <span class="field-label">{{ t.fleetEditor.rules.usageUnit }}</span>
                <select v-model.number="usageUnit" class="select select-compact" :disabled="!subs.canMutate.value" @change="onUsageInput()">
                  <option v-for="unit in USAGE_UNITS" :key="unit" :value="unit">{{ t.units.bytes[unit] }}</option>
                </select>
              </label>
            </div>
          </div>
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
          :fleet-bound="fleet.fleetBound.value"
          :sample-rows="fleet.catalogue.value?.sample ?? fleet.selection.value?.sample ?? []"
          :probe-unavailable="fleet.probeMissing.value"
          :can-preview-step="canPreviewNow"
          :previewing-step="subs.previewing.value ? subs.previewStep.value : null"
          :open-step="stepToOpen"
          :flagged-step="refusedStep && !refusalElsewhere ? refusedStep.position - 1 : null"
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
        <span v-if="actionError" class="field-error" role="alert">{{ actionError }}</span>
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
      <!-- A legacy record's output is its owner-credential export, which only
           core's refresh may read (s2-plan section 1.2). -->
      <!-- With the migration as the way out (lane 6b's MigrationFlow). -->
      <WithheldNotice v-if="withheld" :record-id="editingId ?? ''" data-testid="preview-withheld" />
      <SubscriptionPreviewSummary
        v-else-if="subs.preview.value"
        :preview="subs.preview.value"
        :step-label="subs.previewStep.value === null ? '' : previewStepLabel"
        :deltas="explanation?.deltas ?? []"
        :dropped-by="explanation?.droppedBy"
      />
      <ScriptNetworkNotice
        v-else-if="networkRefusal"
        :refusal="networkRefusal"
        :record-name="refusalRecordName"
        :step="refusedStep"
        :can-open="canOpenRefusedStep"
        :other-record="refusalElsewhere"
        @open-step="openRefusedStep"
      />
      <p v-else-if="subs.previewError.value" class="editor-side-note is-error" role="alert" data-testid="preview-error">
        {{ subs.previewError.value }}
      </p>
      <p v-else-if="draftError" class="editor-side-note">{{ draftError }}</p>
      <p v-else class="editor-side-note">{{ t.editor.nothingRun }}</p>

      <!-- The client document over placeholder credentials: what a holder's
           client gets in shape, never in secrets. -->
      <section v-if="placeholderDoc && !withheld" class="side-section" data-testid="placeholder-document">
        <h3 id="placeholder-doc-title" class="side-heading">{{ t.fleetEditor.document.title(placeholderDoc.label) }}</h3>
        <p class="side-proof">{{ t.fleetEditor.document.proof }}</p>
        <div v-if="placeholderDoc.text" class="side-doc" tabindex="0" role="region" aria-labelledby="placeholder-doc-title">
          <DocumentView :text="placeholderDoc.text" :language="placeholderDoc.language" aria-labelledby="placeholder-doc-title" />
        </div>
        <p v-else class="editor-side-note">{{ t.fleetEditor.document.empty }}</p>
      </section>

      <!-- One identity's share: who receives which lines, and why the rest
           are left out. -->
      <section v-if="fleet.fleetBound.value && !withheld" class="side-section" data-testid="identity-preview">
        <h3 id="identity-preview-title" class="side-heading" tabindex="-1">{{ t.fleetEditor.identity.title }}</h3>
        <p class="side-proof">{{ t.fleetEditor.identity.description }}</p>
        <p v-if="fleet.bindable.value && editorDirty" class="side-unsaved" role="status" data-testid="identity-unsaved">{{ t.fleetEditor.identity.unsaved }}</p>
        <p v-if="!fleet.bindable.value" class="editor-side-note" data-testid="identity-save-first">{{ t.fleetEditor.identity.saveFirst }}</p>
        <template v-else>
          <IdentityPicker :fleet="fleet" />
          <p v-if="!fleet.identityId.value && fleet.identitiesState.value === 'ready' && fleet.identities.value.length" class="editor-side-note">
            {{ t.fleetEditor.identity.choose }}
          </p>
          <BindPreviewList
            v-if="fleet.identityId.value"
            :fleet="fleet"
            :steps="draft.process"
            :record-id="editingId ?? ''"
            @open-line="openLine = $event"
            @open-step="(position) => (position ? openStep(position) : (editorTab = 'operations'))"
          />
        </template>
      </section>
      </PcPanelBody>
    </PcPanel>
    </div>

    <LineDetailDrawer :fleet="fleet" :line-uuid="openLine" @close="openLine = null" />

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

<style scoped>
/* A card the record may not choose stays readable: its reason replaces its
   detail, and it does not take the hover a choosable card takes. */
.source.is-disabled { cursor: not-allowed; opacity: 0.72; }
.source.is-disabled:hover { background: var(--muted); }

.source-badge {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  margin-left: var(--space-1);
  padding: 0 var(--space-1);
  border: 1px solid var(--lt-warn-border);
  border-radius: 999px;
  background: var(--lt-warn-soft);
  color: var(--warning-text);
  font-size: var(--lt-text-xs);
  font-weight: 600;
  vertical-align: 1px;
  white-space: nowrap;
}

.source-pointer { color: var(--warning-text); }

.rules {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}

.rules-proof { margin: 0 0 var(--space-3); }

.rules-row {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.rules-text {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.rules-sub {
  margin-left: calc(16px + var(--space-2));
  max-width: var(--lt-measure-field);
}

.rules-usage {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-end;
  gap: var(--space-2);
}

.rules .rules-number {
  width: 10ch;
  max-width: 10ch;
  min-height: var(--lt-control-h-sm);
  padding: 0 var(--space-2);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  background: var(--card);
  color: var(--foreground);
  font-family: var(--font-mono);
  font-size: var(--lt-text-sm);
  outline: none;
}

.rules .rules-number:focus-visible { border-color: var(--primary); box-shadow: var(--lt-focus-ring-tight); }

.side-section {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  margin-top: var(--space-4);
  padding-top: var(--space-3);
  border-top: 1px solid var(--border);
  min-width: 0;
}

.side-heading {
  margin: 0;
  font-size: var(--text-body);
  font-weight: 650;
}

.side-heading:focus-visible { outline: none; box-shadow: var(--lt-focus-ring-tight); border-radius: var(--radius-sm); }

.side-unsaved {
  margin: 0;
  padding: var(--space-1) var(--space-2);
  border-left: 2px solid var(--warning);
  background: var(--lt-warn-soft);
  color: var(--warning-text);
  font-size: var(--lt-text-xs);
  line-height: var(--lt-leading);
}

.side-proof {
  margin: 0;
  font-size: var(--lt-text-xs);
  line-height: var(--lt-leading);
  color: var(--muted-foreground);
}

.side-doc:focus-visible { outline: none; box-shadow: var(--lt-focus-ring); }

.side-doc {
  max-height: 22rem;
  overflow: auto;
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
}

</style>
