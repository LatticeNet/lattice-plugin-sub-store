import { computed, ref } from "vue";

import {
  BINDINGS,
  callMethod,
  CONVERT_OUTPUT_WARN_BYTES,
  type ConversionResult,
} from "./client";
import type { HostContext } from "./host";
import { safeErrorMessage } from "./subStoreModel";

/**
 * State for the Convert tab: one-shot conversion of operator-supplied raw
 * subscription content through the embedded engine. The screen owns the form
 * state and validation (pipelinesModel); this layer owns the call and the
 * result. There is no preview method — the conversion result itself carries
 * the node counts alongside the output, so preview and produce are one call.
 */
export function useConvert(host: HostContext) {
  const result = ref<ConversionResult>();
  const producing = ref(false);
  const actionError = ref("");

  const available = computed(() => host.available(BINDINGS.engineConvert));
  // A rendered result cannot have been truncated — the runner aborts instead of
  // truncating — so this flags PROXIMITY to the ceiling, not an overrun.
  const resultNearBudget = computed(
    () => (result.value?.output_bytes ?? 0) >= CONVERT_OUTPUT_WARN_BYTES,
  );

  async function produce(raw: string, target: string, operators?: unknown[]): Promise<boolean> {
    if (!host.bridge || !available.value || producing.value || !raw.trim() || !target) return false;
    producing.value = true;
    actionError.value = "";
    result.value = undefined;
    try {
      result.value = await callMethod<ConversionResult>(
        host.bridge,
        BINDINGS.engineConvert,
        { raw, target, operators },
        60_000,
      ).promise;
      return true;
    } catch (cause) {
      actionError.value = safeErrorMessage(cause, "Conversion failed");
      return false;
    } finally {
      producing.value = false;
      await host.resize();
    }
  }

  function reset(): void {
    result.value = undefined;
    actionError.value = "";
  }

  return {
    result,
    producing,
    actionError,
    available,
    resultNearBudget,
    produce,
    reset,
  };
}
