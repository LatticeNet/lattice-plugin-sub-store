<script setup lang="ts">
import { computed, ref } from "vue";
import { GripVertical } from "@lucide/vue";

import type { SubscriptionListItem } from "../client";

/**
 * Choosing which subscriptions a combination gathers.
 *
 * Modelled on how Sub-Store actually does it: a tag filter across the top, a
 * checkbox per subscription, and a drag handle — because the ORDER of chosen
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
          <span class="row-name">{{ item.display_name || item.name }}</span>
          <span class="row-meta mono">{{ describe(item) }}</span>
        </label>
        <span class="row-tags">
          <span v-for="tag in item.tags ?? []" :key="tag" class="row-tag">{{ tag }}</span>
        </span>
        <span class="row-move">
          <button type="button" :disabled="index === 0" aria-label="Move up" @click="move(index, index - 1)">↑</button>
          <button
            type="button"
            :disabled="index === chosen.length - 1"
            aria-label="Move down"
            @click="move(index, index + 1)"
          >
            ↓
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
          <span class="row-name">{{ item.display_name || item.name }}</span>
          <span class="row-meta mono">{{ describe(item) }}</span>
        </label>
        <span class="row-tags">
          <span v-for="tag in item.tags ?? []" :key="tag" class="row-tag">{{ tag }}</span>
        </span>
      </li>
    </ul>

    <p v-else-if="!chosen.length" class="picker-note">
      <template v-if="tagFilter">Nothing tagged “{{ tagFilter }}”.</template>
      <template v-else>There are no subscriptions to combine yet.</template>
    </p>
  </div>
</template>

<style scoped>
.picker {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 12px;
  border: 1px solid var(--border, #d9dde2);
  border-radius: 11px;
  background: var(--background, #f7f8f9);
}

.picker-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
}

.picker-tags,
.picker-bulk {
  display: flex;
  flex-wrap: wrap;
  gap: 5px;
}

.picker-tags button,
.picker-bulk button {
  padding: 3px 10px;
  border: 1px solid transparent;
  border-radius: 999px;
  background: transparent;
  color: var(--muted-foreground, #656d76);
  font-size: 11.5px;
  cursor: pointer;
}

.picker-tags button.is-active {
  border-color: var(--primary, #1769aa);
  color: var(--primary, #1769aa);
}

.picker-bulk button {
  border-color: var(--border, #d9dde2);
}

.picker-bulk button:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}

.picker-chosen,
.picker-rest {
  display: flex;
  flex-direction: column;
  gap: 4px;
  margin: 0;
  padding: 0;
  list-style: none;
}

.row {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 7px 9px;
  border: 1px solid transparent;
  border-radius: 8px;
}

.row.is-chosen {
  border-color: color-mix(in srgb, var(--primary, #1769aa) 35%, var(--border, #d9dde2));
  background: color-mix(in srgb, var(--primary, #1769aa) 7%, transparent);
}

.row:hover {
  background: var(--card, #fff);
}

.row-grip {
  color: var(--muted-foreground, #656d76);
  cursor: grab;
}

.row-order {
  min-width: 18px;
  font-size: 11px;
  color: var(--primary, #1769aa);
}

.row-main {
  display: flex;
  align-items: center;
  gap: 8px;
  flex: 1;
  min-width: 0;
  cursor: pointer;
}

.row-name {
  font-size: 13px;
  font-weight: 600;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.row-meta {
  font-size: 11px;
  color: var(--muted-foreground, #656d76);
  white-space: nowrap;
}

.row-tags {
  display: flex;
  gap: 4px;
  flex-wrap: wrap;
}

.row-tag {
  padding: 1px 6px;
  border-radius: 999px;
  background: color-mix(in srgb, var(--muted-foreground, #656d76) 18%, transparent);
  font-size: 10px;
  color: var(--foreground, #17191c);
}

.row-move {
  display: flex;
  gap: 3px;
}

.row-move button {
  padding: 2px 6px;
  border: 1px solid var(--border, #d9dde2);
  border-radius: 6px;
  background: transparent;
  color: inherit;
  font-size: 11px;
  cursor: pointer;
}

.row-move button:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}

.picker-note {
  margin: 0;
  font-size: 11.5px;
  color: var(--muted-foreground, #656d76);
}
</style>
