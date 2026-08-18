<script setup lang="ts">
/** Icon-only action; label is mandatory for the accessible name and tooltip. */
defineProps<{
  label: string;
  danger?: boolean;
  disabled?: boolean;
}>();
</script>

<template>
  <button
    class="lt-iconbtn"
    :class="{ danger }"
    type="button"
    :disabled="disabled"
    :aria-label="label"
    :title="label"
  >
    <slot />
  </button>
</template>

<style scoped>
.lt-iconbtn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 26px;
  height: 26px;
  border-radius: var(--lt-radius-sm);
  border: none;
  background: transparent;
  color: var(--lt-fg-muted);
  cursor: pointer;
  transition: background-color var(--lt-dur) var(--lt-ease), color var(--lt-dur) var(--lt-ease);
}
.lt-iconbtn:hover:not(:disabled) { background: var(--lt-surface-2); color: var(--lt-fg); }
.lt-iconbtn:focus-visible { outline: none; box-shadow: var(--lt-focus-ring); }
.lt-iconbtn:disabled { opacity: 0.4; cursor: not-allowed; }
.lt-iconbtn.danger:hover:not(:disabled) { background: var(--lt-danger-soft); color: var(--lt-danger); }
</style>
