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
// The console's default palette (DESIGN-PROGRAM 1.x, "claude"): warm
// neutrals and one terracotta accent. These are the host's values, not the
// plugin's; the plugin only ever reads the tokens.
const DARK: Record<string, string> = {
  "--background": "oklch(0.20 0.006 60)",
  "--foreground": "oklch(0.95 0.006 80)",
  "--card": "oklch(0.235 0.007 60)",
  "--border": "oklch(1 0 0 / 10%)",
  "--muted": "oklch(0.27 0.007 60)",
  "--muted-foreground": "oklch(0.72 0.01 70)",
  "--primary": "oklch(0.74 0.12 42)",
  "--primary-foreground": "oklch(0.20 0.02 45)",
  "--destructive": "#f87171",
  "--ring": "oklch(0.74 0.12 42)",
};

const LIGHT: Record<string, string> = {
  "--background": "oklch(0.985 0.004 85)",
  "--foreground": "oklch(0.24 0.012 60)",
  "--card": "oklch(0.995 0.002 85)",
  "--border": "oklch(0.90 0.008 75)",
  "--muted": "oklch(0.955 0.006 80)",
  "--muted-foreground": "oklch(0.50 0.012 60)",
  "--primary": "oklch(0.58 0.14 40)",
  "--primary-foreground": "#ffffff",
  "--destructive": "#c43838",
  "--ring": "oklch(0.58 0.14 40)",
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
