<script setup lang="ts">
/** Semantic badge. tone carries the only meaning color is allowed to carry. */
defineProps<{
  tone?: "neutral" | "ok" | "warn" | "danger" | "accent";
  /** Render as a dot + label instead of a filled pill (for table status). */
  dot?: boolean;
}>();
</script>

<template>
  <span class="lt-badge" :class="[`tone-${tone ?? 'neutral'}`, { dot }]">
    <span v-if="dot" class="lt-badge-dot" aria-hidden="true" />
    <slot />
  </span>
</template>

<style scoped>
.lt-badge {
  display: inline-flex;
  align-items: center;
  gap: var(--lt-space-1);
  font-size: var(--lt-text-xs);
  line-height: 18px;
  padding: 0 var(--lt-space-2);
  border-radius: 999px;
  white-space: nowrap;
  border: 1px solid transparent;
}
.lt-badge:not(.dot).tone-neutral { background: var(--lt-neutral-soft); color: var(--lt-fg-muted); }
.lt-badge:not(.dot).tone-ok { background: var(--lt-ok-soft); color: var(--lt-ok); }
.lt-badge:not(.dot).tone-warn { background: var(--lt-warn-soft); color: var(--lt-warn); }
.lt-badge:not(.dot).tone-danger { background: var(--lt-danger-soft); color: var(--lt-danger); }
.lt-badge:not(.dot).tone-accent { background: color-mix(in oklab, var(--lt-accent) 12%, var(--lt-surface) 88%); color: var(--lt-accent); }
.lt-badge.dot { padding: 0; background: none; color: var(--lt-fg); font-size: var(--lt-text-sm); }
.lt-badge-dot { width: 7px; height: 7px; border-radius: 999px; flex: none; }
.tone-ok .lt-badge-dot { background: var(--lt-ok); }
.tone-warn .lt-badge-dot { background: var(--lt-warn); }
.tone-danger .lt-badge-dot { background: var(--lt-danger); }
.tone-neutral .lt-badge-dot { background: var(--lt-fg-muted); }
.tone-accent .lt-badge-dot { background: var(--lt-accent); }
</style>
