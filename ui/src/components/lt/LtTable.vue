<script setup lang="ts" generic="Row">
import { ChevronDown, ChevronUp } from "@lucide/vue";
import type { LtColumn, LtSortState } from "./ltTable";

/**
 * Presentational data table. All state (sorting, visibility, selection) lives
 * in useLtTable at the screen, so this component stays a pure function of its
 * props and every behavior is unit-testable without mounting.
 */
const props = defineProps<{
  columns: readonly LtColumn<Row>[];
  rows: readonly Row[];
  rowKey: (row: Row) => string;
  sort: LtSortState | null;
  compact?: boolean;
  selectable?: boolean;
  selected?: ReadonlySet<string>;
  allSelected?: boolean;
  /** Rows currently mid-operation render with a pending treatment. */
  pending?: ReadonlySet<string>;
}>();
const emit = defineEmits<{
  (e: "sort", id: string): void;
  (e: "toggle-row", key: string): void;
  (e: "toggle-all"): void;
  (e: "row-click", row: Row): void;
}>();

function isSelected(row: Row): boolean {
  return props.selected?.has(props.rowKey(row)) ?? false;
}
function isPending(row: Row): boolean {
  return props.pending?.has(props.rowKey(row)) ?? false;
}
</script>

<template>
  <div class="lt-table-wrap">
    <table class="lt-table" :class="{ compact }">
      <thead>
        <tr>
          <th v-if="selectable" class="lt-th lt-th-select">
            <input
              type="checkbox"
              :checked="allSelected"
              aria-label="Select all rows"
              @change="emit('toggle-all')"
            />
          </th>
          <th
            v-for="column in columns"
            :key="column.id"
            class="lt-th"
            :class="{ sortable: !!column.sort, right: column.align === 'right' }"
            :style="column.width ? { width: column.width } : undefined"
            :aria-sort="sort?.id === column.id ? (sort.desc ? 'descending' : 'ascending') : undefined"
          >
            <button
              v-if="column.sort"
              class="lt-th-btn"
              type="button"
              @click="emit('sort', column.id)"
            >
              {{ column.label }}
              <ChevronUp v-if="sort?.id === column.id && !sort.desc" :size="12" aria-hidden="true" />
              <ChevronDown v-else-if="sort?.id === column.id" :size="12" aria-hidden="true" />
            </button>
            <template v-else>{{ column.label }}</template>
          </th>
        </tr>
      </thead>
      <tbody>
        <tr
          v-for="row in rows"
          :key="rowKey(row)"
          class="lt-tr"
          :class="{ selected: isSelected(row), pending: isPending(row) }"
          @click="emit('row-click', row)"
        >
          <td v-if="selectable" class="lt-td lt-td-select" @click.stop>
            <input
              type="checkbox"
              :checked="isSelected(row)"
              :aria-label="`Select ${rowKey(row)}`"
              @change="emit('toggle-row', rowKey(row))"
            />
          </td>
          <td
            v-for="column in columns"
            :key="column.id"
            class="lt-td"
            :class="{ right: column.align === 'right' }"
          >
            <slot :name="`cell-${column.id}`" :row="row">—</slot>
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>

<style scoped>
.lt-table-wrap {
  border: 1px solid var(--lt-border);
  border-radius: var(--lt-radius);
  overflow-x: auto;
  background: var(--lt-surface);
}
.lt-table {
  width: 100%;
  border-collapse: collapse;
  font-size: var(--lt-text-sm);
}
.lt-th {
  text-align: left;
  font-weight: 500;
  color: var(--lt-fg-muted);
  font-size: var(--lt-text-xs);
  text-transform: uppercase;
  letter-spacing: 0.04em;
  padding: 0 var(--lt-space-3);
  height: 34px;
  border-bottom: 1px solid var(--lt-border);
  white-space: nowrap;
  position: sticky;
  top: 0;
  background: var(--lt-surface);
  z-index: 1;
}
.lt-th.right, .lt-td.right { text-align: right; }
.lt-th-btn {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  border: none;
  background: none;
  font: inherit;
  color: inherit;
  text-transform: inherit;
  letter-spacing: inherit;
  cursor: pointer;
  padding: 0;
}
.lt-th-btn:hover { color: var(--lt-fg); }
.lt-th-btn:focus-visible { outline: none; box-shadow: var(--lt-focus-ring); border-radius: 2px; }
.lt-td {
  padding: 0 var(--lt-space-3);
  height: var(--lt-row-h);
  border-bottom: 1px solid var(--lt-border);
  vertical-align: middle;
}
.lt-table.compact .lt-td { height: var(--lt-row-h-compact); }
.lt-tr:last-child .lt-td { border-bottom: none; }
.lt-tr:hover { background: var(--lt-surface-2); }
.lt-tr.selected { background: color-mix(in oklab, var(--lt-accent) 7%, var(--lt-surface) 93%); }
.lt-tr.pending { opacity: 0.55; pointer-events: none; }
.lt-th-select, .lt-td-select { width: 32px; padding-right: 0; }
.lt-td-select input, .lt-th-select input { accent-color: var(--lt-accent); }
</style>
