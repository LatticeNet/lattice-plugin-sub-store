/**
 * Escape closes the topmost overlay, for a screen that has no other use for
 * the key. The record screens arbitrate Escape themselves, because after the
 * overlays there is a row menu, an open row and an editor that all answer the
 * same key in a fixed order.
 */
export { useOverlayEscape } from "@latticenet/plugin-bridge/chassis";
