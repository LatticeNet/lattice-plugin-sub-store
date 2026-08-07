<script setup lang="ts">
import { onMounted, ref, watch } from "vue";
import { CircleAlert, CircleCheck, Download, LoaderCircle, Upload } from "@lucide/vue";

import { CONVERT_TARGETS } from "../client";
import { useHost } from "../host";
import { useSubscriptionOps } from "../useSubscriptionOps";

const host = useHost();
const ops = useSubscriptionOps(host);

const defaultTarget = ref("");
const defaultUa = ref("");
const migrateUrl = ref("");
const backupText = ref("");
const exported = ref("");

async function saveSettings(): Promise<void> {
  await ops.saveSettings({
    ...ops.settings.value,
    default_target: defaultTarget.value || undefined,
    default_ua: defaultUa.value || undefined,
  });
}

async function doExport(): Promise<void> {
  exported.value = await ops.exportBackup();
}

/**
 * The backup is offered as a copyable block rather than a download.
 *
 * The plugin UI runs in an opaque-origin sandbox; an anchor with a blob URL and
 * a `download` attribute is exactly the kind of thing that behaves differently
 * across browsers there, and a save that silently does nothing is worse than a
 * textarea the operator can select.
 */
async function copyExported(): Promise<void> {
  if (!exported.value) return;
  try {
    await navigator.clipboard.writeText(exported.value);
    ops.notice.value = "Backup copied to the clipboard.";
  } catch {
    ops.actionError.value = "The clipboard is unavailable here — select the text and copy it manually.";
  }
}

/**
 * Load after the bridge handshake, not on mount.
 *
 * `available()` reads the interfaces the host declared for this frame, and on
 * first paint that has not arrived — so loading in `onMounted` alone silently
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
  <section class="configuration" aria-labelledby="settings-title">
    <div class="section-heading">
      <div>
        <h2 id="settings-title">Defaults</h2>
        <p>Applied to subscriptions that do not set their own.</p>
      </div>
    </div>

    <div v-if="ops.actionError.value" class="alert" role="alert">
      <CircleAlert :size="16" aria-hidden="true" /> {{ ops.actionError.value }}
    </div>
    <div v-else-if="ops.notice.value" class="alert alert-ok" role="status">
      <CircleCheck :size="16" aria-hidden="true" /> {{ ops.notice.value }}
    </div>

    <p v-if="!ops.canReadSettings.value" class="permission-note">
      This bundle does not declare the settings methods.
    </p>

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
        <button
          class="button button-primary"
          type="button"
          :disabled="!ops.canWriteSettings.value || ops.saving.value"
          @click="saveSettings"
        >
          <LoaderCircle v-if="ops.saving.value" :size="16" class="spin" aria-hidden="true" />
          Save defaults
        </button>
      </div>
    </template>
  </section>

  <section class="configuration" aria-labelledby="migrate-title">
    <div class="section-heading">
      <div>
        <h2 id="migrate-title">Migrate from a standalone Sub-Store</h2>
        <p>Reads an existing instance and copies its subscriptions in.</p>
      </div>
    </div>

    <p v-if="!ops.canMigrate.value" class="permission-note">
      This bundle does not declare the migration method.
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
        <button
          class="button button-secondary"
          type="button"
          :disabled="ops.busy.value"
          @click="ops.migrate(migrateUrl)"
        >
          <LoaderCircle v-if="ops.busy.value" :size="16" class="spin" aria-hidden="true" />
          Import subscriptions
        </button>
      </div>

      <div v-if="ops.report.value" class="preview-summary">
        <p class="mono">Imported: {{ ops.report.value.imported?.length ?? 0 }}</p>
        <ul v-if="ops.report.value.imported?.length" class="sub-list">
          <li v-for="id in ops.report.value.imported" :key="id" class="sub-card">
            <span class="sub-title mono">{{ id }}</span>
          </li>
        </ul>
        <template v-if="ops.report.value.skipped && Object.keys(ops.report.value.skipped).length">
          <p class="mono">Skipped:</p>
          <ul class="sub-list">
            <li v-for="(reason, id) in ops.report.value.skipped" :key="id" class="sub-card">
              <span class="sub-title mono">{{ id }}</span>
              <span class="sub-meta">{{ reason }}</span>
            </li>
          </ul>
        </template>
      </div>
    </template>
  </section>

  <section class="configuration" aria-labelledby="backup-title">
    <div class="section-heading">
      <div>
        <h2 id="backup-title">Backup and restore</h2>
        <p>A versioned envelope you can keep outside the server.</p>
      </div>
    </div>

    <div class="form-actions">
      <button
        class="button button-secondary"
        type="button"
        :disabled="!ops.canExport.value || ops.busy.value"
        @click="doExport"
      >
        <Download :size="16" aria-hidden="true" /> Export
      </button>
      <button v-if="exported" class="button button-compact" type="button" @click="copyExported">
        Copy
      </button>
    </div>

    <label v-if="exported" class="field field-wide">
      <span class="field-label">Exported backup</span>
      <textarea class="code-area" rows="6" readonly :value="exported"></textarea>
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
    </label>

    <div class="form-actions">
      <button
        class="button button-secondary"
        type="button"
        :disabled="!ops.canImport.value || ops.busy.value"
        @click="ops.importBackup(backupText)"
      >
        <Upload :size="16" aria-hidden="true" /> Restore
      </button>
    </div>
  </section>
</template>
