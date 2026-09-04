<script setup lang="ts">
import { ref, watchEffect } from "vue";

import { provideHost } from "../src/host";
import Shell from "../src/Shell.vue";
import { createFakeHost } from "./fakeHost";
import { applyHostTheme } from "./hostTheme";

// The same shell the plugin ships, against a fake host.
provideHost(createFakeHost());

/**
 * The console publishes its whole chassis to the frame (token contract v2) and
 * the harness has no console, so it sends the same payload itself.
 *
 * It used to send ten approximate hexes and nothing else, so a harness
 * screenshot showed the plugin's own radius scale, its own spacing and its own
 * hardcoded green and amber rather than the console's. Everything anyone
 * polished here was a value the operator never sees. `dev/hostTheme.ts` holds
 * the payload; it is applied the way the bridge applies it, as inline
 * properties on <html>, so the precedence is the real one.
 */
const dark = ref(true);

watchEffect(() => applyHostTheme(dark.value ? "dark" : "light"));
</script>

<template>
  <div class="dev-bar">
    <span>dev harness, fake host, canned records</span>
    <button type="button" @click="dark = !dark">{{ dark ? "Light" : "Dark" }}</button>
  </div>
  <Shell />
</template>

<style>
.dev-bar {
  position: sticky;
  top: 0;
  /* Under the overlays on purpose: the panel is fixed 12px from the frame top
     in production, and a harness bar painted over it would hide the geometry
     this harness exists to show. */
  z-index: 1;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 6px 14px;
  background: repeating-linear-gradient(45deg, #7c3aed 0 10px, #6d28d9 10px 20px);
  color: #fff;
  font-size: 11.5px;
  font-weight: 600;
}

.dev-bar button {
  padding: 3px 10px;
  border: 1px solid rgba(255, 255, 255, 0.5);
  border-radius: 999px;
  background: transparent;
  color: inherit;
  font-size: 11px;
  cursor: pointer;
}
</style>
