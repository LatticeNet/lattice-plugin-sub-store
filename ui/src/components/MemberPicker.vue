<script setup lang="ts">
import { computed, ref } from "vue";
import { ArrowDown, ArrowUp, GripVertical } from "@lucide/vue";

import type { SubscriptionListItem } from "../client";

/**
 * Choosing which subscriptions a combination gathers.
 *
 * Modelled on how Sub-Store actually does it: a tag filter across the top, a
 * checkbox per subscription, and a drag handle, because the ORDER of chosen
 * members decides the order their nodes appear in the merged output, and a
 * plain multi-select cannot express that.
 *
 * Selected members are listed first, in their chosen order, so the thing being
 * configured is visible without hunting through everything that was not picked.
 */

const props = defineProps<{
  candidates: readonly SubscriptionListItem[];
  selected: readonly string[];
}>();

const emit = defineEmits<{
  (e: "update:selected", value: string[]): void;
}>();

const tagFilter = ref<string>("");
const dragIndex = ref<number | null>(null);

const tags = computed(() => {
  const seen = new Set<string>();
  for (const item of props.candidates) for (const tag of item.tags ?? []) seen.add(tag);
  return [...seen].sort();
});

const byId = computed(() => {
  const map = new Map<string, SubscriptionListItem>();
  for (const item of props.candidates) map.set(item.id, item);
  return map;
});

/** Chosen members keep their order; the rest follow, filtered by tag. */
const chosen = computed(() =>
  props.selected.map((id) => byId.value.get(id)).filter((item): item is SubscriptionListItem => !!item),
);

const rest = computed(() =>
  props.candidates.filter(
    (item) =>
      !props.selected.includes(item.id) &&
      (!tagFilter.value || (item.tags ?? []).includes(tagFilter.value)),
  ),
);

function toggle(id: string): void {
  const next = props.selected.includes(id)
    ? props.selected.filter((entry) => entry !== id)
    : [...props.selected, id];
  emit("update:selected", next);
}

function move(from: number, to: number): void {
  if (to < 0 || to >= props.selected.length || from === to) return;
  const next = [...props.selected];
  const [moved] = next.splice(from, 1);
  next.splice(to, 0, moved);
  emit("update:selected", next);
}

function onDrop(index: number): void {
  if (dragIndex.value === null) return;
  move(dragIndex.value, index);
  dragIndex.value = null;
}

function selectAllShown(): void {
  const ids = rest.value.map((item) => item.id);
  emit("update:selected", [...props.selected, ...ids]);
}

function clearAll(): void {
  emit("update:selected", []);
}

function describe(item: SubscriptionListItem): string {
  if (item.source === "vpn-core") return "fleet nodes";
  if (item.source === "local") return "pasted";
  return item.has_url ? "provider link" : "pasted";
}
</script>

<template>
  <div class="picker">
    <div class="picker-bar">
      <div class="picker-tags">
        <button
          type="button"
          :class="{ 'is-active': tagFilter === '' }"
          @click="tagFilter = ''"
        >
          All
        </button>
        <button
          v-for="tag in tags"
          :key="tag"
          type="button"
          :class="{ 'is-active': tagFilter === tag }"
          @click="tagFilter = tag"
        >
          {{ tag }}
        </button>
      </div>
      <div class="picker-bulk">
        <button type="button" :disabled="!rest.length" @click="selectAllShown">Select shown</button>
        <button type="button" :disabled="!selected.length" @click="clearAll">Clear</button>
      </div>
    </div>

    <!-- Chosen members, in merge order, reorderable. -->
    <ol v-if="chosen.length" class="picker-chosen">
      <li
        v-for="(item, index) in chosen"
        :key="item.id"
        class="row is-chosen"
        draggable="true"
        @dragstart="dragIndex = index"
        @dragover.prevent
        @drop="onDrop(index)"
      >
        <span class="row-grip" aria-hidden="true"><GripVertical :size="14" /></span>
        <span class="row-order mono">{{ index + 1 }}</span>
        <label class="row-main">
          <input type="checkbox" checked @change="toggle(item.id)" />
          <span class="row-name" :title="item.display_name || item.name">{{ item.display_name || item.name }}</span>
          <span class="row-meta mono">{{ describe(item) }}</span>
        </label>
        <span class="row-tags">
          <span v-for="tag in item.tags ?? []" :key="tag" class="row-tag">{{ tag }}</span>
        </span>
        <span class="row-move">
          <button
            type="button"
            :disabled="index === 0"
            :aria-label="`Move ${item.display_name || item.name} up`"
            title="Move up"
            @click="move(index, index - 1)"
          >
            <ArrowUp :size="13" aria-hidden="true" />
          </button>
          <button
            type="button"
            :disabled="index === chosen.length - 1"
            :aria-label="`Move ${item.display_name || item.name} down`"
            title="Move down"
            @click="move(index, index + 1)"
          >
            <ArrowDown :size="13" aria-hidden="true" />
          </button>
        </span>
      </li>
    </ol>

    <p v-if="chosen.length" class="picker-note">
      Nodes appear in this order. Drag, or use the arrows.
    </p>

    <ul v-if="rest.length" class="picker-rest">
      <li v-for="item in rest" :key="item.id" class="row">
        <label class="row-main">
          <input type="checkbox" @change="toggle(item.id)" />
          <span class="row-name" :title="item.display_name || item.name">{{ item.display_name || item.name }}</span>
          <span class="row-meta mono">{{ describe(item) }}</span>
        </label>
        <span class="row-tags">
          <span v-for="tag in item.tags ?? []" :key="tag" class="row-tag">{{ tag }}</span>
        </span>
      </li>
    </ul>

    <p v-else-if="!chosen.length" class="picker-note">
      <template v-if="tagFilter">Nothing tagged "{{ tagFilter }}".</template>
      <template v-else>There are no subscriptions to combine yet.</template>
    </p>
  </div>
</template>

<style scoped>
.picker {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding: var(--space-3);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  background: var(--background);
}

.picker-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
  flex-wrap: wrap;
}

.picker-tags,
.picker-bulk {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-1);
}

.picker-tags button,
.picker-bulk button {
  height: var(--lt-control-h-sm);
  padding: 0 var(--space-3);
  border: 1px solid transparent;
  border-radius: 999px;
  background: transparent;
  color: var(--muted-foreground);
  font-size: var(--lt-text-xs);
}

.picker-tags button:hover,
.picker-bulk button:hover:not(:disabled) { color: var(--foreground); }

.picker-tags button:focus-visible,
.picker-bulk button:focus-visible,
.row-move button:focus-visible { outline: none; box-shadow: var(--lt-focus-ring); }

.picker-tags button.is-active {
  border-color: var(--primary);
  background: var(--lt-accent-soft);
  color: var(--primary);
}

.picker-bulk button { border-color: var(--border); }
.picker-bulk button:disabled { opacity: 0.45; }

.picker-chosen,
.picker-rest {
  display: flex;
  flex-direction: column;
  gap: 2px;
  margin: 0;
  padding: 0;
  list-style: none;
  /* A store can hold 256 records. The unchosen list is the long one, and it
     scrolls inside the picker rather than pushing the rest of the form down a
     screen and a half. */
  max-height: 320px;
  overflow-y: auto;
}

.row {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-1) var(--space-2);
  border: 1px solid transparent;
  border-radius: var(--radius-sm);
}

.row.is-chosen {
  border-color: var(--lt-accent-border);
  background: var(--lt-accent-soft);
}

.row:hover { background: var(--muted); }
.row.is-chosen:hover { background: var(--lt-accent-soft); }

.row-grip {
  display: inline-flex;
  color: var(--muted-foreground);
  cursor: grab;
}

.row-order {
  min-width: 18px;
  font-size: var(--lt-text-xs);
  color: var(--primary);
  font-variant-numeric: tabular-nums;
}

.row-main {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  flex: 1;
  min-width: 0;
  cursor: pointer;
}

.row-name {
  font-size: var(--text-body);
  font-weight: 600;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.row-meta {
  font-size: var(--lt-text-xs);
  color: var(--muted-foreground);
  white-space: nowrap;
  flex: none;
}

.row-tags {
  display: flex;
  gap: var(--space-1);
  flex-wrap: wrap;
  flex: none;
}

.row-tag {
  padding: 0 var(--space-2);
  border-radius: 999px;
  background: var(--lt-neutral-soft);
  font-size: var(--lt-text-xs);
  line-height: 18px;
  color: var(--muted-foreground);
}

.row-move { display: flex; gap: 2px; flex: none; }

.row-move button {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  border: 0;
  border-radius: var(--radius-sm);
  background: transparent;
  color: var(--muted-foreground);
}

.row-move button:hover:not(:disabled) { background: var(--muted); color: var(--foreground); }
.row-move button:disabled { opacity: 0.35; }

.picker-note {
  margin: 0;
  font-size: var(--lt-text-xs);
  color: var(--muted-foreground);
}
</style>
