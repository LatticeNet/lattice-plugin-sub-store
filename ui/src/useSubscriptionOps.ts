import { computed, ref } from "vue";

import {
  BINDINGS,
  callMethod,
  isRefused,
  type BackupExportResponse,
  type MigrationReport,
  type MutationRefusal,
  type SubscriptionSettings,
} from "./client";
import { refusalMessage } from "./fleet";
import type { HostContext } from "./host";
import { t } from "./i18n";
import { safeErrorMessage } from "./subStoreModel";

/**
 * What an import or a migration did beside writing records (design 28 S2,
 * s2-plan section 2.7): the fleet-bound records it staged instead of writing
 * live, which wait for Publish, and the records a guard refused, each with
 * its structured refusal. Said after the count, so a restore that brought
 * back a fleet record never reads as if that record were serving.
 */
function writeOutcome(result: { staged?: Record<string, string>; refused?: Record<string, MutationRefusal> }): string {
  const parts: string[] = [];
  const staged = Object.keys(result.staged ?? {});
  if (staged.length) parts.push(t.plans.importStaged(staged.length));
  for (const [id, refusal] of Object.entries(result.refused ?? {})) {
    parts.push(refusalMessage(refusal, id, [], (reason) => t.plans.refusal.notImportedRecord(id, reason)));
  }
  return parts.join(" ");
}

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
      loadError.value = safeErrorMessage(cause, t.ops.loadFailed);
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
      notice.value = t.ops.saved;
      return true;
    } catch (cause) {
      actionError.value = safeErrorMessage(cause, t.ops.saveFailed);
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
      notice.value = response.backup ? t.ops.exported : t.ops.exportEmpty;
      return response.backup ?? "";
    } catch (cause) {
      actionError.value = safeErrorMessage(cause, t.ops.exportFailed);
      return "";
    } finally {
      busy.value = false;
      await host.resize();
    }
  }

  async function importBackup(backup: string): Promise<boolean> {
    if (!host.bridge || !canImport.value || busy.value) return false;
    if (!backup.trim()) {
      actionError.value = t.ops.pasteBackup;
      return false;
    }
    busy.value = true;
    actionError.value = "";
    notice.value = "";
    try {
      const result = await callMethod<Record<string, unknown>>(host.bridge, BINDINGS.subImport, {
        backup,
      }).promise;
      // A refusal of the whole import is a successful reply, never an error
      // (s2-plan section 2.6): the gateway replaces a mutating method's error.
      if (isRefused(result)) {
        actionError.value = refusalMessage(result.refused, "", [], t.plans.refusal.notImported);
        return false;
      }
      const restored = Array.isArray(result.imported) ? result.imported.length : undefined;
      const counted = restored === undefined ? t.ops.restored : t.ops.restoredCount(restored);
      notice.value = [counted, writeOutcome(result as Parameters<typeof writeOutcome>[0])].filter(Boolean).join(" ");
      return true;
    } catch (cause) {
      // The export refuses an unknown or missing format rather than guessing,
      // so a truncated or hand-edited file fails here loudly. Say that.
      actionError.value = safeErrorMessage(cause, t.ops.restoreFailed);
      return false;
    } finally {
      busy.value = false;
      await host.resize();
    }
  }

  async function migrate(baseUrl: string): Promise<boolean> {
    if (!host.bridge || !canMigrate.value || busy.value) return false;
    if (!baseUrl.trim()) {
      actionError.value = t.ops.giveBaseUrl;
      return false;
    }
    busy.value = true;
    actionError.value = "";
    notice.value = "";
    report.value = null;
    try {
      const reply = await callMethod<MigrationReport | Record<string, unknown>>(host.bridge, BINDINGS.subMigrate, {
        base_url: baseUrl.trim(),
      }).promise;
      if (isRefused(reply)) {
        actionError.value = refusalMessage(reply.refused, "", [], t.plans.refusal.notImported);
        return false;
      }
      report.value = reply as MigrationReport;
      const count = report.value?.imported?.length ?? 0;
      notice.value = [t.ops.imported(count), writeOutcome(reply as Parameters<typeof writeOutcome>[0])].filter(Boolean).join(" ");
      return true;
    } catch (cause) {
      actionError.value = safeErrorMessage(cause, t.ops.migrateFailed);
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
