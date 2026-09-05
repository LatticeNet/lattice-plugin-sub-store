<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from "vue";
import { Download, Upload } from "@lucide/vue";
import { PcButton, PcNotice, PcPanel, PcPanelBody, PcPanelHeader, PcSkeleton } from "@latticenet/plugin-bridge/chassis";

import { CONVERT_TARGETS } from "../client";
import { useHost } from "../host";
import { copyText } from "../hostClipboard";
import { useSubscriptionOps } from "../useSubscriptionOps";
import LtConfirmDialog from "../components/lt/LtConfirmDialog.vue";
import { useOverlayEscape } from "../useOverlayEscape";

const host = useHost();
const ops = useSubscriptionOps(host);

const defaultTarget = ref("");
const defaultUa = ref("");
/** Whether the stored settings have actually been read. */
const settingsReady = computed(() => ops.state.value === "ready");
const migrateUrl = ref("");
const backupText = ref("");
const exported = ref("");

/**
 * Save writes the loaded settings back with the two edited fields replaced.
 *
 * It refuses to run before the load has landed: until then `settings` is still
 * `{}`, so an early click wrote `undefined` over whatever was stored. The two
 * controls looked empty because nothing had been read yet, and clicking Save
 * made that emptiness real.
 */
async function saveSettings(): Promise<void> {
  if (!settingsReady.value) return;
  await ops.saveSettings({
    ...ops.settings.value,
    default_target: defaultTarget.value || undefined,
    default_ua: defaultUa.value || undefined,
  });
}

async function doExport(): Promise<void> {
  exported.value = await ops.exportBackup();
}

const exportField = ref<HTMLTextAreaElement | null>(null);

/**
 * The backup is offered as a copyable block rather than a download.
 *
 * The plugin UI runs in an opaque-origin sandbox; an anchor with a blob URL and
 * a `download` attribute is exactly the kind of thing that behaves differently
 * across browsers there, and a save that silently does nothing is worse than a
 * textarea the operator can select.
 *
 * That sandbox is also why the copy itself goes through the host: the frame's
 * own clipboard is blocked by Permissions Policy. See hostClipboard.ts.
 */
async function copyExported(): Promise<void> {
  if (!exported.value) return;
  ops.actionError.value = "";
  if (await copyText(exported.value)) {
    ops.notice.value = "Backup copied to the clipboard.";
    return;
  }
  // This screen already prints the envelope in a textarea below the button, so
  // the recovery is to put the operator's cursor in it with everything
  // selected rather than to describe where to look.
  ops.notice.value = "";
  ops.actionError.value =
    "The console could not reach the clipboard. The backup below is selected, copy it with your keyboard.";
  await nextTick();
  exportField.value?.focus();
  exportField.value?.select();
}

/**
 * Restore is the one irreversible action on this screen and it had no
 * confirmation at all: one click replaced live subscriptions by id, with
 * nothing between the operator and a pasted envelope from the wrong
 * deployment. It now goes through the same two-step dialog every other
 * destructive action uses.
 *
 * What the dialog restates is what the envelope actually claims to hold, read
 * out of the pasted text rather than described in general terms. A truncated
 * or hand-edited envelope is refused by the backend, so if it does not parse
 * here there is nothing worth confirming either.
 */
const restoreConfirm = ref(false);

// This screen has one overlay and no other use for Escape.
useOverlayEscape();

interface BackupEnvelope {
  version?: unknown;
  records?: unknown[];
  subscriptions?: unknown[];
}

const parsedBackup = computed<{ ok: true; count: number; version: string } | { ok: false; reason: string }>(() => {
  const text = backupText.value.trim();
  if (!text) return { ok: false, reason: "Paste an exported envelope first." };
  try {
    const value = JSON.parse(text) as BackupEnvelope;
    if (!value || typeof value !== "object" || Array.isArray(value)) {
      return { ok: false, reason: "This is not a backup envelope." };
    }
    const records = Array.isArray(value.records)
      ? value.records
      : Array.isArray(value.subscriptions)
        ? value.subscriptions
        : null;
    if (!records) return { ok: false, reason: "This envelope carries no records." };
    return { ok: true, count: records.length, version: String(value.version ?? "unversioned") };
  } catch {
    return { ok: false, reason: "This is not valid JSON, so nothing can be read from it." };
  }
});

const canRestore = computed(() => ops.canImport.value && !ops.busy.value && parsedBackup.value.ok);

/** What the confirmation lists by name: the records the envelope will write. */
const restoreNames = computed(() => {
  const parsed = parsedBackup.value;
  if (!parsed.ok) return [];
  return [
    `${parsed.count} record(s) from a ${parsed.version} envelope`,
    "Restoring writes every one of them into the live store, overwriting any record that shares an id.",
  ];
});

function requestRestore(): void {
  restoreConfirm.value = true;
}

async function runRestore(): Promise<void> {
  restoreConfirm.value = false;
  await ops.importBackup(backupText.value);
}

/**
 * Load after the bridge handshake, not on mount.
 *
 * `available()` reads the interfaces the host declared for this frame, and on
 * first paint that has not arrived, so loading in `onMounted` alone silently
 * no-ops and never retries, leaving the screen looking empty and permissionless.
 */
async function loadAll(): Promise<void> {
  await ops.loadSettings();
  defaultTarget.value = (ops.settings.value.default_target as string) ?? "";
  defaultUa.value = (ops.settings.value.default_ua as string) ?? "";
}

onMounted(() => {
  if (host.init.value) void loadAll();
});

watch(host.init, (value) => {
  if (value) void loadAll();
});
</script>

<template>
  <div class="lens settings-lens">
    <PcNotice v-if="ops.actionError.value" tone="danger">{{ ops.actionError.value }}</PcNotice>
    <PcNotice v-else-if="ops.notice.value" tone="success">{{ ops.notice.value }}</PcNotice>

    <PcPanel label="Defaults">
      <PcPanelHeader title="Defaults" description="Applied to subscriptions that do not set their own." />
      <PcPanelBody>
        <p v-if="!ops.canReadSettings.value" class="permission-note">
          This session cannot read Sub-Store settings. Either the installed bundle does not declare
          those methods, or your token lacks the scope.
        </p>

        <PcNotice v-else-if="ops.loadError.value" tone="danger" title="The settings could not be read">
          {{ ops.loadError.value }}
          <template #actions><PcButton compact @click="ops.loadSettings()">Try again</PcButton></template>
        </PcNotice>

        <!-- The controls used to render immediately, empty, while the stored values
             were still in flight, indistinguishable from "nothing is set". -->
        <PcSkeleton v-else-if="!settingsReady" :count="2" label="Loading the defaults" />

        <template v-else>
          <div class="form-grid">
            <label class="field">
              <span class="field-label">Default target</span>
              <select v-model="defaultTarget" class="select">
                <option value="">No conversion</option>
                <option v-for="target in CONVERT_TARGETS" :key="target.id" :value="target.id">
                  {{ target.label }}
                </option>
              </select>
            </label>

            <label class="field">
              <span class="field-label">Default user agent</span>
              <input v-model="defaultUa" type="text" autocomplete="off" placeholder="Optional" />
            </label>
          </div>

          <div class="form-actions">
            <PcButton variant="primary" :busy="ops.saving.value" :disabled="!ops.canWriteSettings.value" @click="saveSettings">
              {{ ops.saving.value ? "Saving…" : "Save defaults" }}
            </PcButton>
          </div>
        </template>
      </PcPanelBody>
    </PcPanel>

    <PcPanel label="Migrate from a standalone Sub-Store">
      <PcPanelHeader title="Migrate from a standalone Sub-Store" description="Reads an existing instance and copies its subscriptions in." />
      <PcPanelBody>
        <p v-if="!ops.canMigrate.value" class="permission-note">
          This session cannot import from another Sub-Store. Either the installed bundle does not
          declare that method, or your token lacks the scope.
        </p>

        <template v-else>
          <div class="form-grid">
            <label class="field field-wide">
              <span class="field-label">Sub-Store base URL</span>
              <input
                v-model="migrateUrl"
                type="text"
                autocomplete="off"
                spellcheck="false"
                placeholder="Base URL of the running Sub-Store"
              />
              <span class="field-optional">
                Importing publishes nothing. Each subscription stays unserved until you create a share
                for it, so the cutover is a decision per subscription rather than a side effect.
              </span>
            </label>
          </div>

          <div class="form-actions">
            <PcButton :busy="ops.busy.value" :disabled="!migrateUrl.trim()" @click="ops.migrate(migrateUrl)">
              Import subscriptions
            </PcButton>
          </div>

          <!-- The report used to render every imported id as its own bordered card,
               so a 40-record import produced 40 boxes. It is a list of ids. -->
          <div v-if="ops.report.value" class="report">
            <p class="report-line">
              Imported <strong>{{ ops.report.value.imported?.length ?? 0 }}</strong> record(s).
            </p>
            <ul v-if="ops.report.value.imported?.length" class="node-list">
              <li v-for="id in ops.report.value.imported" :key="id" class="node-row">
                <span class="node-name mono" :title="id">{{ id }}</span>
                <span class="node-meta">imported</span>
              </li>
            </ul>
            <template v-if="ops.report.value.skipped && Object.keys(ops.report.value.skipped).length">
              <p class="report-line">
                Skipped <strong>{{ Object.keys(ops.report.value.skipped).length }}</strong>.
              </p>
              <ul class="node-list">
                <li v-for="(reason, id) in ops.report.value.skipped" :key="id" class="node-row">
                  <span class="node-name mono" :title="String(id)">{{ id }}</span>
                  <span class="node-meta" :title="String(reason)">{{ reason }}</span>
                </li>
              </ul>
            </template>
          </div>
        </template>
      </PcPanelBody>
    </PcPanel>

    <PcPanel label="Backup and restore">
      <PcPanelHeader title="Backup and restore" description="A versioned envelope you can keep outside the server." />
      <PcPanelBody>
        <div class="form-actions">
          <PcButton :disabled="!ops.canExport.value || ops.busy.value" @click="doExport">
            <template #icon><Download :size="15" aria-hidden="true" /></template>
            Export
          </PcButton>
          <PcButton v-if="exported" compact @click="copyExported">Copy</PcButton>
        </div>

        <div class="form-grid">
          <label v-if="exported" class="field field-wide">
            <span class="field-label">Exported backup</span>
            <textarea ref="exportField" class="code-area" rows="6" readonly :value="exported"></textarea>
          </label>

          <label class="field field-wide">
            <span class="field-label">Restore from a backup</span>
            <textarea
              v-model="backupText"
              class="code-area"
              rows="6"
              spellcheck="false"
              placeholder="Paste an exported envelope"
            ></textarea>
            <span class="field-optional">
              Restoring adds and replaces subscriptions by id. A truncated or hand-edited envelope is
              refused rather than partially applied.
            </span>
            <!-- Said before the click, not after: the button is otherwise live over
                 text that cannot possibly restore. -->
            <span v-if="backupText.trim() && !parsedBackup.ok" class="field-error" role="status">
              {{ parsedBackup.reason }}
            </span>
            <span v-else-if="parsedBackup.ok" class="field-optional">
              This envelope holds {{ parsedBackup.count }} record(s), version
              <code>{{ parsedBackup.version }}</code>.
            </span>
          </label>
        </div>

        <div class="form-actions">
          <PcButton
            destructive
            :disabled="!canRestore"
            :title="ops.canImport.value ? undefined : 'This session cannot restore a backup. Either the installed bundle does not declare that method, or your token lacks the scope.'"
            @click="requestRestore()"
          >
            <template #icon><Upload :size="15" aria-hidden="true" /></template>
            Restore
          </PcButton>
        </div>

        <LtConfirmDialog
          :open="restoreConfirm"
          title="Restore this backup? Every record in the envelope overwrites the stored one with the same id, and that cannot be undone from here."
          verb="Restore"
          :names="restoreNames"
          :busy="ops.busy.value"
          @cancel="restoreConfirm = false"
          @confirm="runRestore()"
        />
      </PcPanelBody>
    </PcPanel>
  </div>
</template>

<style scoped>
.report {
  max-width: var(--lt-measure-form);
  margin-top: var(--space-4);
  padding: var(--space-3);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  background: var(--card);
}

.report-line {
  margin: 0 0 var(--space-1);
  font-size: var(--lt-text-sm);
}

.report-line + .node-list { margin-bottom: var(--space-3); }
</style>
