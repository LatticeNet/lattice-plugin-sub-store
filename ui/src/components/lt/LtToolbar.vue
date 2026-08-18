<script setup lang="ts">
/** List-screen toolbar: search slot, filter chips slot, controls slot. */
</script>

<template>
  <div class="lt-toolbar">
    <div class="lt-toolbar-search"><slot name="search" /></div>
    <div class="lt-toolbar-filters"><slot name="filters" /></div>
    <div class="lt-toolbar-controls"><slot name="controls" /></div>
  </div>
</template>

<style scoped>
.lt-toolbar {
  display: flex;
  align-items: center;
  gap: var(--lt-space-3);
  flex-wrap: wrap;
  margin-bottom: var(--lt-space-3);
}
.lt-toolbar-search { flex: 0 1 260px; min-width: 180px; }
.lt-toolbar-filters { display: flex; align-items: center; gap: var(--lt-space-1); flex-wrap: wrap; flex: 1; }
.lt-toolbar-controls { display: flex; align-items: center; gap: var(--lt-space-2); margin-left: auto; }
</style>
