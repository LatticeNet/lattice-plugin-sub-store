<script setup lang="ts">
/** Structure-predictable placeholder. Table rows only; never fake progress. */
defineProps<{ rows?: number; columns?: number }>();
</script>

<template>
  <div class="lt-skeleton" role="status" aria-label="Loading">
    <div v-for="r in rows ?? 6" :key="r" class="lt-skeleton-row">
      <span
        v-for="c in columns ?? 5"
        :key="c"
        class="lt-skeleton-cell"
        :style="{ width: c === 1 ? '32%' : `${10 + ((r * 7 + c * 13) % 12)}%` }"
      />
    </div>
  </div>
</template>

<style scoped>
/* The panel the loaded list will be in, so the container does not appear from
   nowhere when the rows land. A skeleton whose shape is not the shape of the
   answer is a skeleton that has to be redrawn. */
.lt-skeleton {
  border: 1px solid var(--border);
  border-radius: var(--radius-xl);
  background: var(--card);
  overflow: hidden;
}
.lt-skeleton-row:last-child { border-bottom: 0; }
.lt-skeleton-row {
  display: flex;
  gap: var(--space-4);
  align-items: center;
  height: var(--row-h);
  border-bottom: 1px solid var(--border);
  padding: 0 var(--space-3);
}
.lt-skeleton-cell {
  height: 10px;
  border-radius: 999px;
  background: var(--lt-neutral-soft);
  animation: lt-skeleton-pulse 1.2s ease-in-out infinite;
}
@keyframes lt-skeleton-pulse {
  50% { opacity: 0.45; }
}
@media (prefers-reduced-motion: reduce) {
  .lt-skeleton-cell { animation: none; }
}
</style>
