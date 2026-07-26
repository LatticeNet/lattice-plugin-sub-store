import { computed, ref } from "vue";

import {
  BINDINGS,
  callMethod,
  OUTPUT_SIZE_BUDGET_BYTES,
  type ConvertPreviewResponse,
  type ConvertRunResponse,
  type ConvertTarget,
  type ConvertTargetsResponse,
} from "./client";
import type { HostContext } from "./host";
import { safeErrorMessage } from "./subStoreModel";

export type ConvertLoadState = "idle" | "loading" | "ready" | "error";

/**
 * State for the Convert tab: target discovery, source selection, preview
 * (always small) and produce (full content, guarded against the host's 1 MiB
 * output cap — see OUTPUT_SIZE_BUDGET_BYTES and the TASK-0002 spike).
 */
export function useConvert(host: HostContext) {
  const targets = ref<ConvertTarget[]>([]);
  const targetsState = ref<ConvertLoadState>("idle");
  const targetsError = ref("");

  const selected = ref<string[]>([]);
  const targetId = ref("");

  const preview = ref<ConvertPreviewResponse>();
  const previewing = ref(false);
  const output = ref<ConvertRunResponse>();
  const producing = ref(false);
  const actionError = ref("");

  const available = computed(() => host.available(BINDINGS.convertTargets) && host.available(BINDINGS.convertRun));
  const canConvert = computed(() => selected.value.length > 0 && !!targetId.value && !previewing.value && !producing.value);
  const previewOverBudget = computed(
    () => (preview.value?.size_estimate_bytes ?? 0) > OUTPUT_SIZE_BUDGET_BYTES,
  );

  async function loadTargets(): Promise<void> {
    if (!host.bridge || !available.value) return;
    targetsState.value = "loading";
    targetsError.value = "";
    try {
      const response = await callMethod<ConvertTargetsResponse>(host.bridge, BINDINGS.convertTargets, {}).promise;
      targets.value = response.targets ?? [];
      if (!targetId.value && targets.value.length) targetId.value = targets.value[0].id;
      targetsState.value = "ready";
    } catch (cause) {
      targetsState.value = "error";
      targetsError.value = safeErrorMessage(cause, "Conversion targets could not be loaded");
    } finally {
      await host.resize();
    }
  }

  function toggle(name: string): void {
    selected.value = selected.value.includes(name)
      ? selected.value.filter((item) => item !== name)
      : [...selected.value, name];
    preview.value = undefined;
    output.value = undefined;
  }

  async function runPreview(): Promise<boolean> {
    if (!host.bridge || !host.available(BINDINGS.convertPreview) || !canConvert.value) return false;
    previewing.value = true;
    actionError.value = "";
    preview.value = undefined;
    try {
      const response = await callMethod<ConvertPreviewResponse>(host.bridge, BINDINGS.convertPreview, {
        subscriptions: selected.value,
        target: targetId.value,
      }).promise;
      // Sparse-wire normalization: the templates index these arrays directly.
      preview.value = {
        node_count: response?.node_count ?? 0,
        groups: response?.groups ?? [],
        warnings: response?.warnings ?? [],
        size_estimate_bytes: response?.size_estimate_bytes ?? 0,
      };
      return true;
    } catch (cause) {
      actionError.value = safeErrorMessage(cause, "Conversion preview failed");
      return false;
    } finally {
      previewing.value = false;
      await host.resize();
    }
  }

  async function produce(): Promise<boolean> {
    if (!host.bridge || !canConvert.value) return false;
    producing.value = true;
    actionError.value = "";
    output.value = undefined;
    try {
      const response = await callMethod<ConvertRunResponse>(
        host.bridge,
        BINDINGS.convertRun,
        { subscriptions: selected.value, target: targetId.value },
        60_000,
      ).promise;
      output.value = {
        content: response?.content ?? "",
        content_type: response?.content_type ?? "text/plain",
        file_name: response?.file_name ?? "sub-store-output.txt",
        size_bytes: response?.size_bytes ?? 0,
      };
      return true;
    } catch (cause) {
      actionError.value = safeErrorMessage(cause, "Conversion failed");
      return false;
    } finally {
      producing.value = false;
      await host.resize();
    }
  }

  return {
    targets,
    targetsState,
    targetsError,
    selected,
    targetId,
    preview,
    previewing,
    output,
    producing,
    actionError,
    available,
    canConvert,
    previewOverBudget,
    loadTargets,
    toggle,
    runPreview,
    produce,
  };
}
