import { onBeforeUnmount, watch, type Ref } from "vue";

import { registerOverlay } from "./overlayStack";

/**
 * Keep an overlay's place in the stack for exactly as long as it is open.
 *
 * Every overlay component needs the same four lines and gets them wrong in the
 * same way (registering on mount rather than on open, or forgetting the
 * unmount path), so they live here once.
 */
export function useOverlayRegistration(open: Ref<boolean> | (() => boolean), close: () => void): void {
  let dispose: (() => void) | undefined;
  const release = (): void => {
    dispose?.();
    dispose = undefined;
  };
  watch(
    typeof open === "function" ? open : () => open.value,
    (isOpen) => {
      if (isOpen && !dispose) dispose = registerOverlay(close);
      else if (!isOpen) release();
    },
    { immediate: true },
  );
  onBeforeUnmount(release);
}
