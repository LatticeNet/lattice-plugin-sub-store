<script setup lang="ts">
import { ref, watchEffect } from "vue";

import { provideHost } from "../src/host";
import Shell from "../src/Shell.vue";
import { createFakeHost } from "./fakeHost";

// The same shell the plugin ships, against a fake host.
provideHost(createFakeHost());

/**
 * The host injects design tokens through the bridge; the harness has no host,
 * so it applies representative values itself.
 *
 * Without this the UI renders against its light fallbacks, and polishing it
 * would mean polishing colours the operator never sees. The dashboard is
 * dark-forward.
 */
const DARK: Record<string, string> = {
  "--background": "#0d1117",
  "--foreground": "#e9eef5",
  "--card": "#161c26",
  "--border": "#242d3a",
  "--muted": "#1a212c",
  "--muted-foreground": "#8b96a5",
  "--primary": "#2dd4bf",
  "--primary-foreground": "#04211d",
  "--destructive": "#f87171",
  "--ring": "#2dd4bf",
};

const LIGHT: Record<string, string> = {
  "--background": "#f7f8f9",
  "--foreground": "#17191c",
  "--card": "#ffffff",
  "--border": "#d9dde2",
  "--muted": "#f1f3f5",
  "--muted-foreground": "#656d76",
  "--primary": "#1769aa",
  "--primary-foreground": "#ffffff",
  "--destructive": "#c43838",
  "--ring": "#1769aa",
};

const dark = ref(true);

watchEffect(() => {
  const tokens = dark.value ? DARK : LIGHT;
  for (const [name, value] of Object.entries(tokens)) {
    document.documentElement.style.setProperty(name, value);
  }
});
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
  z-index: 50;
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
