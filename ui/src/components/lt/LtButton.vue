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
  gap: var(--space-1);
  font: inherit;
  font-size: var(--lt-text-sm);
  border-radius: var(--radius-sm);
  border: 1px solid var(--border);
  background: var(--card);
  color: var(--foreground);
  cursor: pointer;
  transition: background-color var(--lt-dur) var(--lt-ease), border-color var(--lt-dur) var(--lt-ease);
}
.lt-btn.s-md { height: 30px; padding: 0 var(--space-3); }
.lt-btn.s-sm { height: 24px; padding: 0 var(--space-2); font-size: var(--lt-text-xs); }
.lt-btn:focus-visible { outline: none; box-shadow: var(--lt-focus-ring); }
.lt-btn:disabled { opacity: 0.5; cursor: not-allowed; }
.lt-btn.v-primary { background: var(--primary); border-color: var(--primary); color: var(--primary-foreground); }
.lt-btn.v-primary:hover:not(:disabled) { background: color-mix(in oklab, var(--primary) 88%, black 12%); }
.lt-btn.v-ghost:hover:not(:disabled) { background: var(--muted); }
.lt-btn.v-danger { color: var(--lt-danger-ink); border-color: color-mix(in oklab, var(--destructive) 35%, var(--border) 65%); }
.lt-btn.v-danger:hover:not(:disabled) { background: var(--lt-danger-soft); }
</style>
