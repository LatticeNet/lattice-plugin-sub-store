/**
 * Who owns Escape, and what "an overlay is open" means.
 *
 * The model itself lives in the plugin bridge's chassis, where every plugin
 * frame shares it: an overlay registers a close function while it is open, one
 * document handler closes the top of the stack, and "is an overlay open" is
 * the depth rather than a list a screen has to remember to extend. This module
 * keeps the plugin's own import path so the screens, the editor guard and the
 * tests read one name for it.
 */
export { closeTopOverlay, overlayDepth, registerOverlay, resetOverlayStack } from "@latticenet/plugin-bridge/chassis";
