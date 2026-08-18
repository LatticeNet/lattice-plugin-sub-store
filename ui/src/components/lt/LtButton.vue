<script setup lang="ts">
defineProps<{
  variant?: "primary" | "ghost" | "danger";
  size?: "md" | "sm";
  disabled?: boolean;
  type?: "button" | "submit";
}>();
</script>

<template>
  <button
    class="lt-btn"
    :class="[`v-${variant ?? 'ghost'}`, `s-${size ?? 'md'}`]"
    :type="type ?? 'button'"
    :disabled="disabled"
  >
    <slot />
  </button>
</template>

<style scoped>
.lt-btn {
  display: inline-flex;
  align-items: center;
  gap: var(--lt-space-1);
  font: inherit;
  font-size: var(--lt-text-sm);
  border-radius: var(--lt-radius-sm);
  border: 1px solid var(--lt-border);
  background: var(--lt-surface);
  color: var(--lt-fg);
  cursor: pointer;
  transition: background-color var(--lt-dur) var(--lt-ease), border-color var(--lt-dur) var(--lt-ease);
}
.lt-btn.s-md { height: 30px; padding: 0 var(--lt-space-3); }
.lt-btn.s-sm { height: 24px; padding: 0 var(--lt-space-2); font-size: var(--lt-text-xs); }
.lt-btn:focus-visible { outline: none; box-shadow: var(--lt-focus-ring); }
.lt-btn:disabled { opacity: 0.5; cursor: not-allowed; }
.lt-btn.v-primary { background: var(--lt-accent); border-color: var(--lt-accent); color: var(--lt-accent-contrast); }
.lt-btn.v-primary:hover:not(:disabled) { background: color-mix(in oklab, var(--lt-accent) 88%, black 12%); }
.lt-btn.v-ghost:hover:not(:disabled) { background: var(--lt-surface-2); }
.lt-btn.v-danger { color: var(--lt-danger); border-color: color-mix(in oklab, var(--lt-danger) 35%, var(--lt-border) 65%); }
.lt-btn.v-danger:hover:not(:disabled) { background: var(--lt-danger-soft); }
</style>
