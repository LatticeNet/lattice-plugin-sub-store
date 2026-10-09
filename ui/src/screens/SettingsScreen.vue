<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from "vue";
import { Download, Upload } from "@lucide/vue";
import { PcButton, PcNotice, PcPanel, PcSkeleton } from "@latticenet/plugin-bridge/chassis";

import { CONVERT_TARGETS } from "../client";
import { useHost } from "../host";
import { copyText } from "../hostClipboard";
import { t } from "../i18n";
import { useLensChrome } from "../lensChrome";
import { describeSubStoreBase, resolveSubStoreBase } from "../migrateUrl";
import { useSubscriptionOps } from "../useSubscriptionOps";
import LtConfirmDialog from "../components/lt/LtConfirmDialog.vue";
import MaskedUrlInput from "../components/MaskedUrlInput.vue";
import { useOverlayEscape } from "../useOverlayEscape";

const host = useHost();
const ops = useSubscriptionOps(host);
const chrome = useLensChrome();

const defaultTarget = ref("");
const defaultUa = ref("");
/** Whether the stored settings have actually been read. */
const settingsReady = computed(() => ops.state.value === "ready");
const migrateUrl = ref("");
const migrateConfirm = ref(false);
const migrateParsed = computed(() => describeSubStoreBase(migrateUrl.value));
const migrateConfirmNames = computed(() =>
  migrateParsed.value.ok ? [t.records.importFrom(migrateParsed.value.origin)] : [],
);
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

function requestMigrate(): void {
  const parsed = migrateParsed.value;
  if (!parsed.ok) {
    ops.actionError.value = parsed.reason;
    return;
  }
  migrateConfirm.value = true;
}

async function runMigrate(): Promise<void> {
  migrateConfirm.value = false;
  await ops.migrate(resolveSubStoreBase(migrateUrl.value));
}

function viewImported(): void {
  chrome.openLens("records", { origin: "migrated" });
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
    ops.notice.value = t.settings.copied;
    return;
  }
  // This screen already prints the envelope in a textarea below the button, so
  // the recovery is to put the operator's cursor in it with everything
  // selected rather than to describe where to look.
  ops.notice.value = "";
  ops.actionError.value = t.settings.clipboardFailed;
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

// Escape closes the top overlay. This screen has two confirms (import and restore)
// and no other use for the key.
useOverlayEscape();

interface BackupEnvelope {
  version?: unknown;
  records?: unknown[];
  subscriptions?: unknown[];
}

const parsedBackup = computed<{ ok: true; count: number; version: string } | { ok: false; reason: string }>(() => {
  const text = backupText.value.trim();
  if (!text) return { ok: false, reason: t.settings.pasteFirst };
  try {
    const value = JSON.parse(text) as BackupEnvelope;
    if (!value || typeof value !== "object" || Array.isArray(value)) {
      return { ok: false, reason: t.settings.notEnvelope };
    }
    const records = Array.isArray(value.records)
      ? value.records
      : Array.isArray(value.subscriptions)
        ? value.subscriptions
        : null;
    if (!records) return { ok: false, reason: t.settings.noRecords };
    return { ok: true, count: records.length, version: String(value.version ?? t.settings.unversioned) };
  } catch {
    return { ok: false, reason: t.settings.notJson };
  }
});

const canRestore = computed(() => ops.canImport.value && !ops.busy.value && parsedBackup.value.ok);

/** What the confirmation lists by name: the records the envelope will write. */
const restoreNames = computed(() => {
  const parsed = parsedBackup.value;
  if (!parsed.ok) return [];
  return [t.settings.restoreNames(parsed.count, parsed.version), t.settings.restoreWrites];
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

    <PcPanel :label="t.settings.panel">
      <div class="settings-card">
        <section class="settings-section" aria-labelledby="settings-defaults-title">
          <h3 id="settings-defaults-title" class="settings-h">{{ t.settings.defaults }}</h3>
          <p class="settings-lead">{{ t.settings.defaultsLead }}</p>

          <p v-if="!ops.canReadSettings.value" class="permission-note">{{ t.settings.cannotRead }}</p>

          <PcNotice v-else-if="ops.loadError.value" tone="danger" :title="t.settings.loadFailed">
            {{ ops.loadError.value }}
            <template #actions><PcButton compact @click="ops.loadSettings()">{{ t.common.tryAgain }}</PcButton></template>
          </PcNotice>

          <!-- The controls used to render immediately, empty, while the stored values
               were still in flight, indistinguishable from "nothing is set". -->
          <PcSkeleton v-else-if="!settingsReady" :count="2" :label="t.settings.loadingDefaults" />

          <template v-else>
            <div class="form-grid">
              <label class="field">
                <span class="field-label">{{ t.settings.defaultTarget }}</span>
                <select v-model="defaultTarget" class="select">
                  <option value="">{{ t.settings.noConversion }}</option>
                  <option v-for="target in CONVERT_TARGETS" :key="target.id" :value="target.id">
                    {{ target.label }}
                  </option>
                </select>
              </label>

              <label class="field">
                <span class="field-label">{{ t.settings.defaultUa }}</span>
                <input v-model="defaultUa" type="text" autocomplete="off" :placeholder="t.settings.optional" />
              </label>
            </div>
          </template>
        </section>

        <section class="settings-section" aria-labelledby="settings-import-title">
          <h3 id="settings-import-title" class="settings-h">{{ t.settings.importTitle }}</h3>
          <p class="settings-lead">{{ t.settings.importLead }}</p>

          <p v-if="!ops.canMigrate.value" class="permission-note">{{ t.settings.cannotImport }}</p>

          <template v-else>
            <div class="form-grid">
              <label class="field field-wide">
                <span class="field-label">{{ t.settings.runningSubStore }}</span>
                <MaskedUrlInput
                  v-model="migrateUrl"
                  :placeholder="t.records.importPlaceholder"
                  :aria-label="t.records.importAria"
                />
                <span class="field-optional">{{ t.settings.importNote }}</span>
                <span v-if="migrateUrl.trim() && !migrateParsed.ok" class="field-error" role="status">
                  {{ migrateParsed.reason }}
                </span>
                <span v-else-if="migrateParsed.ok" class="field-optional">
                  {{ t.settings.willImportBefore }} <code>{{ migrateParsed.origin }}</code>{{ t.settings.willImportAfter }}
                </span>
              </label>
            </div>

            <div class="form-actions">
              <PcButton :busy="ops.busy.value" :disabled="!migrateParsed.ok" @click="requestMigrate()">
                {{ t.settings.importAction }}
              </PcButton>
            </div>

            <LtConfirmDialog
              :open="migrateConfirm"
              :title="t.records.importConfirm"
              :verb="t.records.importVerb"
              :names="migrateConfirmNames"
              :busy="ops.busy.value"
              @cancel="migrateConfirm = false"
              @confirm="runMigrate()"
            />

            <!-- The report used to render every imported id as its own bordered card,
                 so a 40-record import produced 40 boxes. It is a list of ids. -->
            <div v-if="ops.report.value" class="report">
              <p class="report-line">{{ t.settings.reportImported(ops.report.value.imported?.length ?? 0, ops.report.value.total ?? 0) }}</p>
              <div class="form-actions" v-if="ops.report.value.imported?.length">
                <PcButton compact @click="viewImported()">{{ t.settings.viewImported }}</PcButton>
              </div>
              <ul v-if="ops.report.value.imported?.length" class="node-list">
                <li v-for="id in ops.report.value.imported" :key="id" class="node-row">
                  <span class="node-name mono" :title="id">{{ id }}</span>
                  <span class="node-meta">{{ t.settings.importedTag }}</span>
                </li>
              </ul>
              <template v-if="ops.report.value.skipped && Object.keys(ops.report.value.skipped).length">
                <p class="report-line">{{ t.settings.reportSkipped(Object.keys(ops.report.value.skipped).length) }}</p>
                <ul class="node-list">
                  <li v-for="(reason, id) in ops.report.value.skipped" :key="id" class="node-row">
                    <span class="node-name mono" :title="String(id)">{{ id }}</span>
                    <span class="node-meta" :title="String(reason)">{{ reason }}</span>
                  </li>
                </ul>
              </template>
              <template v-if="ops.report.value.unavailable && Object.keys(ops.report.value.unavailable).length">
                <p class="report-line">{{ t.settings.reportUnavailable(Object.keys(ops.report.value.unavailable).length) }}</p>
                <ul class="node-list">
                  <li v-for="(reason, id) in ops.report.value.unavailable" :key="id" class="node-row">
                    <span class="node-name mono" :title="String(id)">{{ id }}</span>
                    <span class="node-meta" :title="String(reason)">{{ reason }}</span>
                  </li>
                </ul>
              </template>
              <p v-if="ops.report.value.truncated" class="report-line" role="status">{{ t.settings.reportTruncated }}</p>
            </div>
          </template>
        </section>

        <section class="settings-section" aria-labelledby="settings-backup-title">
          <h3 id="settings-backup-title" class="settings-h">{{ t.settings.backupTitle }}</h3>
          <p class="settings-lead">{{ t.settings.backupLead }}</p>
          <div class="form-actions">
            <PcButton :disabled="!ops.canExport.value || ops.busy.value" @click="doExport">
              <template #icon><Download :size="15" aria-hidden="true" /></template>
              {{ t.settings.export }}
            </PcButton>
            <PcButton v-if="exported" compact @click="copyExported">{{ t.settings.copy }}</PcButton>
          </div>

          <div class="form-grid">
            <label v-if="exported" class="field field-wide">
              <span class="field-label">{{ t.settings.exported }}</span>
              <textarea ref="exportField" class="code-area" rows="6" readonly :value="exported"></textarea>
            </label>

            <label class="field field-wide">
              <span class="field-label">{{ t.settings.restoreFrom }}</span>
              <textarea
                v-model="backupText"
                class="code-area"
                rows="6"
                spellcheck="false"
                :placeholder="t.settings.restorePlaceholder"
              ></textarea>
              <span class="field-optional">{{ t.settings.restoreNote }}</span>
              <!-- Said before the click, not after: the button is otherwise live over
                   text that cannot possibly restore. -->
              <span v-if="backupText.trim() && !parsedBackup.ok" class="field-error" role="status">
                {{ parsedBackup.reason }}
              </span>
              <span v-else-if="parsedBackup.ok" class="field-optional">
                {{ t.settings.envelopeHolds(parsedBackup.count) }} <code>{{ parsedBackup.version }}</code>{{ t.settings.envelopeAfter }}
              </span>
            </label>
          </div>

          <div class="form-actions">
            <PcButton
              destructive
              :disabled="!canRestore"
              :title="ops.canImport.value ? undefined : t.settings.restoreBlocked"
              @click="requestRestore()"
            >
              <template #icon><Upload :size="15" aria-hidden="true" /></template>
              {{ t.settings.restore }}
            </PcButton>
          </div>

          <LtConfirmDialog
            :open="restoreConfirm"
            :title="t.settings.restoreConfirm"
            :verb="t.settings.restore"
            :names="restoreNames"
            :busy="ops.busy.value"
            @cancel="restoreConfirm = false"
            @confirm="runRestore()"
          />
        </section>

        <div class="settings-commit">
          <PcButton variant="primary" :busy="ops.saving.value" :disabled="!ops.canWriteSettings.value || !settingsReady" @click="saveSettings">
            {{ ops.saving.value ? t.settings.saving : t.settings.saveDefaults }}
          </PcButton>
        </div>
      </div>
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
