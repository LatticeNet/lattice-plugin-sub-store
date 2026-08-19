import { computed, ref } from "vue";

import {
  BINDINGS,
  callMethod,
  type BackupExportResponse,
  type MigrationReport,
  type SubscriptionSettings,
} from "./client";
import type { HostContext } from "./host";
import { safeErrorMessage } from "./subStoreModel";

export type OpsState = "idle" | "loading" | "ready" | "error";

/**
 * Settings, backup/restore and migration. The operations surface.
 *
 * Kept apart from `useSubscriptions` because these are whole-store actions
 * rather than per-record ones, and because migration in particular has a
 * consequence worth stating in one place: importing records publishes nothing.
 */
export function useSubscriptionOps(host: HostContext) {
  const state = ref<OpsState>("idle");
  const settings = ref<SubscriptionSettings>({});
  const loadError = ref("");
  const actionError = ref("");
  const notice = ref("");
  const saving = ref(false);
  const busy = ref(false);
  const report = ref<MigrationReport | null>(null);

  const canReadSettings = computed(() => host.available(BINDINGS.subGetSettings));
  const canWriteSettings = computed(() => host.available(BINDINGS.subSaveSettings));
  const canExport = computed(() => host.available(BINDINGS.subExport));
  const canImport = computed(() => host.available(BINDINGS.subImport));
  const canMigrate = computed(() => host.available(BINDINGS.subMigrate));

  async function loadSettings(): Promise<void> {
    if (!host.bridge || !canReadSettings.value) return;
    state.value = "loading";
    loadError.value = "";
    try {
      settings.value = await callMethod<SubscriptionSettings>(host.bridge, BINDINGS.subGetSettings, {}).promise;
      state.value = "ready";
    } catch (cause) {
      state.value = "error";
      loadError.value = safeErrorMessage(cause, "Settings could not be loaded");
    } finally {
      await host.resize();
    }
  }

  async function saveSettings(next: SubscriptionSettings): Promise<boolean> {
    if (!host.bridge || !canWriteSettings.value || saving.value) return false;
    saving.value = true;
    actionError.value = "";
    notice.value = "";
    try {
      settings.value = await callMethod<SubscriptionSettings>(host.bridge, BINDINGS.subSaveSettings, next).promise;
      notice.value = "Settings saved.";
      return true;
    } catch (cause) {
      actionError.value = safeErrorMessage(cause, "Settings could not be saved");
      return false;
    } finally {
      saving.value = false;
      await host.resize();
    }
  }

  /** Returns the backup envelope so the caller can offer it as a download. */
  async function exportBackup(): Promise<string> {
    if (!host.bridge || !canExport.value || busy.value) return "";
    busy.value = true;
    actionError.value = "";
    notice.value = "";
    try {
      const response = await callMethod<BackupExportResponse>(host.bridge, BINDINGS.subExport, {}).promise;
      notice.value = response.backup
        ? "Backup exported."
        : "The server returned an empty backup, so there is nothing to save.";
      return response.backup ?? "";
    } catch (cause) {
      actionError.value = safeErrorMessage(cause, "Backup could not be exported");
      return "";
    } finally {
      busy.value = false;
      await host.resize();
    }
  }

  async function importBackup(backup: string): Promise<boolean> {
    if (!host.bridge || !canImport.value || busy.value) return false;
    if (!backup.trim()) {
      actionError.value = "Paste a backup envelope first.";
      return false;
    }
    busy.value = true;
    actionError.value = "";
    notice.value = "";
    try {
      const result = await callMethod<Record<string, unknown>>(host.bridge, BINDINGS.subImport, {
        backup,
      }).promise;
      const restored = Array.isArray(result.imported) ? result.imported.length : undefined;
      notice.value =
        restored === undefined
          ? "Backup restored."
          : `Backup restored: ${restored} record(s) landed. Any record the server rejected is not counted here. Nothing was published; a share is a separate decision.`;
      return true;
    } catch (cause) {
      // The export refuses an unknown or missing format rather than guessing,
      // so a truncated or hand-edited file fails here loudly. Say that.
      actionError.value = safeErrorMessage(cause, "Backup could not be restored");
      return false;
    } finally {
      busy.value = false;
      await host.resize();
    }
  }

  async function migrate(baseUrl: string): Promise<boolean> {
    if (!host.bridge || !canMigrate.value || busy.value) return false;
    if (!baseUrl.trim()) {
      actionError.value = "Give the standalone Sub-Store's base URL.";
      return false;
    }
    busy.value = true;
    actionError.value = "";
    notice.value = "";
    report.value = null;
    try {
      report.value = await callMethod<MigrationReport>(host.bridge, BINDINGS.subMigrate, {
        base_url: baseUrl.trim(),
      }).promise;
      const count = report.value?.imported?.length ?? 0;
      notice.value = `Imported ${count} record(s); the report below lists anything that was skipped. Nothing is published yet, so create a share for each one you want served.`;
      return true;
    } catch (cause) {
      actionError.value = safeErrorMessage(cause, "Migration failed");
      return false;
    } finally {
      busy.value = false;
      await host.resize();
    }
  }

  function clearMessages(): void {
    actionError.value = "";
    notice.value = "";
  }

  return {
    state,
    settings,
    loadError,
    actionError,
    notice,
    saving,
    busy,
    report,
    canReadSettings,
    canWriteSettings,
    canExport,
    canImport,
    canMigrate,
    loadSettings,
    saveSettings,
    exportBackup,
    importBackup,
    migrate,
    clearMessages,
  };
}
