import { computed, ref, type Ref } from "vue";

/** Column contract for LtTable. Cells render through named slots (`cell-<id>`)
 *  so the table owns structure and screens own content. */
export interface LtColumn<Row> {
  id: string;
  label: string;
  /** Sort accessor; a column without one is not sortable. */
  sort?: (row: Row) => string | number;
  /** Optional columns can be hidden through the column manager. */
  optional?: boolean;
  /** Fixed width (CSS); unset columns share the rest. */
  width?: string;
  align?: "left" | "right";
}

export interface LtSortState {
  id: string;
  desc: boolean;
}

/** Sorting, visibility, and selection for LtTable, kept as plain composable
 *  state so tests can drive it without mounting anything. */
export function useLtTable<Row>(options: {
  rows: Ref<readonly Row[]>;
  columns: readonly LtColumn<Row>[];
  rowKey: (row: Row) => string;
  storageKey?: string;
}) {
  const { rows, columns, rowKey, storageKey } = options;

  const stored = storageKey ? readStored(storageKey) : undefined;
  const sort = ref<LtSortState | null>(stored?.sort ?? null);
  const hidden = ref<Set<string>>(new Set(stored?.hidden ?? []));
  const compact = ref<boolean>(stored?.compact ?? false);
  const selected = ref<Set<string>>(new Set());

  function persist(): void {
    if (!storageKey) return;
    try {
      localStorage.setItem(
        storageKey,
        JSON.stringify({ sort: sort.value, hidden: [...hidden.value], compact: compact.value }),
      );
    } catch {
      /* storage denied in some sandboxes; the UI simply forgets */
    }
  }

  function toggleSort(id: string): void {
    const column = columns.find((c) => c.id === id);
    if (!column?.sort) return;
    if (sort.value?.id !== id) sort.value = { id, desc: false };
    else if (!sort.value.desc) sort.value = { id, desc: true };
    else sort.value = null;
    persist();
  }

  function toggleColumn(id: string): void {
    const next = new Set(hidden.value);
    if (next.has(id)) next.delete(id);
    else next.add(id);
    hidden.value = next;
    persist();
  }

  function setCompact(value: boolean): void {
    compact.value = value;
    persist();
  }

  const visibleColumns = computed(() => columns.filter((c) => !c.optional || !hidden.value.has(c.id)));

  const sortedRows = computed(() => {
    const current = sort.value;
    if (!current) return [...rows.value];
    const column = columns.find((c) => c.id === current.id);
    if (!column?.sort) return [...rows.value];
    const accessor = column.sort;
    const factor = current.desc ? -1 : 1;
    return [...rows.value].sort((a, b) => {
      const va = accessor(a);
      const vb = accessor(b);
      if (va === vb) return 0;
      return (va < vb ? -1 : 1) * factor;
    });
  });

  function toggleRow(key: string): void {
    const next = new Set(selected.value);
    if (next.has(key)) next.delete(key);
    else next.add(key);
    selected.value = next;
  }

  const allVisibleKeys = computed(() => sortedRows.value.map(rowKey));
  const allSelected = computed(
    () => allVisibleKeys.value.length > 0 && allVisibleKeys.value.every((k) => selected.value.has(k)),
  );

  function toggleAll(): void {
    selected.value = allSelected.value ? new Set() : new Set(allVisibleKeys.value);
  }

  function clearSelection(): void {
    selected.value = new Set();
  }

  /** Drop selections that no longer exist (after delete/filter). */
  function pruneSelection(): void {
    const keep = new Set(allVisibleKeys.value);
    const next = new Set([...selected.value].filter((k) => keep.has(k)));
    if (next.size !== selected.value.size) selected.value = next;
  }

  return {
    sort,
    hidden,
    compact,
    selected,
    visibleColumns,
    sortedRows,
    allSelected,
    toggleSort,
    toggleColumn,
    setCompact,
    toggleRow,
    toggleAll,
    clearSelection,
    pruneSelection,
  };
}

function readStored(
  key: string,
): { sort?: LtSortState | null; hidden?: string[]; compact?: boolean } | undefined {
  try {
    const raw = localStorage.getItem(key);
    return raw ? JSON.parse(raw) : undefined;
  } catch {
    return undefined;
  }
}
