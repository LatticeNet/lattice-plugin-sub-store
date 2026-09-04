import { onActivated, onBeforeUnmount, onDeactivated, onMounted } from "vue";

import { closeTopOverlay } from "./overlayStack";

/**
 * Escape closes the topmost overlay, for a screen that has no other use for
 * the key.
 *
 * The record screens do not use this: they arbitrate Escape themselves,
 * because after the overlays there is a row menu, an open row and an editor
 * that all answer the same key in a fixed order. Every other screen wants only
 * the first step, and a screen that raises a dialog without it is a dialog the
 * keyboard cannot dismiss, which is the exact fault this whole change is here
 * to remove.
 *
 * Bound to the activate/deactivate pair, not to mount, because the shell keeps
 * screens alive across tab switches: bound to mount, a hidden screen would
 * still be listening.
 */
export function useOverlayEscape(): void {
  function onKeydown(event: KeyboardEvent): void {
    if (event.key !== "Escape") return;
    if (closeTopOverlay()) event.stopPropagation();
  }
  const bind = (): void => document.addEventListener("keydown", onKeydown);
  const release = (): void => document.removeEventListener("keydown", onKeydown);
  onMounted(bind);
  onActivated(bind);
  onDeactivated(release);
  onBeforeUnmount(release);
}
