<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { ChevronRight } from "@lucide/vue";
import { PcStateDot, useMediaQuery } from "@latticenet/plugin-bridge/chassis";

import { groupByPrefix, pathOf, plural, STAGES, type Lineage, type Stage } from "../pipeline";

/**
 * The overview's one picture: sources, combinations, files and shares as four
 * columns, one chip per record, and an edge for every dependency a record
 * declares. Selecting a chip lights its whole path, upstream and downstream,
 * and dims the rest, so "which link does this phone fetch" and "what breaks
 * if this provider dies" are both one click.
 *
 * Files named for a person (`for-openjobs-loon`, `for-openjobs-stash`) fold
 * into one chip per person when three or more share the prefix; the chip
 * opens in place. Below 640px the columns become stage lists with each
 * record's downstream written under it, because four columns of chips do
 * not fit a phone and a sideways-scrolling picture is not a picture.
 */
export interface ChipFacts {
  tone: "healthy" | "warning" | "error" | "neutral";
  state: string;
  /** Mono figure on the chip: "166→25", "…", "/cdcd". */
  figure: string;
  title: string;
}

const props = defineProps<{
  lineage: Lineage;
  facts: (id: string) => ChipFacts;
  /** The selected node id, or a group key (`group:<prefix>`); "" for none. */
  selected: string;
}>();

const emit = defineEmits<{ select: [key: string] }>();

const STAGE_LABEL: Record<Stage, string> = {
  source: "Sources",
  combination: "Combinations",
  file: "Files",
  share: "Shares",
};

interface DisplayItem {
  key: string;
  kind: "node" | "group";
  /** Node ids this item stands for: one, or a group's members. */
  ids: string[];
  label: string;
  /** For a group: whether its members are drawn under it. */
  open?: boolean;
  /** For a member drawn under its open group. */
  member?: boolean;
}

const openGroups = ref(new Set<string>());

const fileEntries = computed(() =>
  groupByPrefix(props.lineage.columns.file, (id) => props.lineage.nodes.get(id)?.item?.name ?? id),
);

/** Which group a file belongs to, by node id. */
const groupOf = computed(() => {
  const out = new Map<string, string>();
  for (const entry of fileEntries.value) {
    if (entry.kind === "group") for (const id of entry.members) out.set(id, `group:${entry.prefix}`);
  }
  return out;
});

// A record selected from elsewhere (the side panel, a table, the palette)
// opens the group it sits in, or it would be lit inside a folded chip.
watch(
  () => props.selected,
  (key) => {
    const group = groupOf.value.get(key);
    if (group && !openGroups.value.has(group)) openGroups.value = new Set([...openGroups.value, group]);
  },
  { immediate: true },
);

const columns = computed<Record<Stage, DisplayItem[]>>(() => {
  const node = (id: string, member = false): DisplayItem => ({
    key: id,
    kind: "node",
    ids: [id],
    label: props.lineage.nodes.get(id)?.label ?? id,
    member,
  });
  const files: DisplayItem[] = [];
  for (const entry of fileEntries.value) {
    if (entry.kind === "single") {
      files.push(node(entry.member));
      continue;
    }
    const key = `group:${entry.prefix}`;
    const open = openGroups.value.has(key);
    files.push({ key, kind: "group", ids: entry.members, label: entry.label, open });
    if (open) for (const id of entry.members) files.push(node(id, true));
  }
  return {
    source: props.lineage.columns.source.map((id) => node(id)),
    combination: props.lineage.columns.combination.map((id) => node(id)),
    file: files,
    share: props.lineage.columns.share.map((id) => node(id)),
  };
});

/** Where a node is drawn: itself, or the folded group standing in for it. */
function anchorOf(id: string): string {
  const group = groupOf.value.get(id);
  return group && !openGroups.value.has(group) ? group : id;
}

/** The lit path: the selection's own path, or the union of a group's. */
const lit = computed<Set<string> | null>(() => {
  const key = props.selected;
  if (!key) return null;
  if (key.startsWith("group:")) {
    const members = fileEntries.value.find((entry) => entry.kind === "group" && `group:${entry.prefix}` === key);
    if (!members || members.kind !== "group") return null;
    const out = new Set<string>();
    for (const id of members.members) for (const on of pathOf(props.lineage, id)) out.add(on);
    return out;
  }
  if (!props.lineage.nodes.has(key)) return null;
  return pathOf(props.lineage, key);
});

function itemState(item: DisplayItem): "selected" | "on" | "off" | undefined {
  if (!lit.value) return undefined;
  if (item.key === props.selected) return "selected";
  return item.ids.some((id) => lit.value!.has(id)) ? "on" : "off";
}

/** The edges between drawn items, one per pair, whatever folded into them. */
/** The column each drawn item sits in, so an edge knows which columns it skips. */
const columnOf = computed(() => {
  const out = new Map<string, number>();
  STAGES.forEach((stage, index) => {
    for (const item of columns.value[stage]) out.set(item.key, index);
  });
  return out;
});

const drawnEdges = computed(() => {
  const seen = new Map<string, { from: string; to: string; tag: boolean; on: boolean }>();
  for (const edge of props.lineage.edges) {
    const from = anchorOf(edge.from);
    const to = anchorOf(edge.to);
    const key = `${from}\u0000${to}`;
    const on = !!lit.value && lit.value.has(edge.from) && lit.value.has(edge.to);
    const existing = seen.get(key);
    if (existing) {
      existing.on ||= on;
      existing.tag &&= edge.via === "tag";
      continue;
    }
    seen.set(key, { from, to, tag: edge.via === "tag", on });
  }
  return [...seen.values()];
});

// ── facts per drawn item ────────────────────────────────────────────────────

function factsOf(item: DisplayItem): ChipFacts {
  if (item.kind === "node") return props.facts(item.key);
  const facts = item.ids.map((id) => props.facts(id));
  const worst = facts.find((f) => f.tone === "error") ?? facts.find((f) => f.tone === "warning");
  const published = facts.filter((f) => f.tone === "healthy").length;
  // Healthy only when every file in the group is served: one published file
  // out of nine is not a green group.
  const all = published === item.ids.length;
  return {
    tone: worst?.tone ?? (all ? "healthy" : "neutral"),
    state: worst?.state ?? (all ? "all published" : `${published} of ${item.ids.length} published`),
    figure: plural(item.ids.length, "file"),
    title: `${plural(item.ids.length, "file")} named ${item.label}, ${published} published. ${item.open ? "Fold them" : "Show them"}.`,
  };
}

function nameOf(id: string): string {
  return props.lineage.nodes.get(id)?.label ?? id;
}

/** What an item feeds, for the stage lists: "merge-cd-openjobs, for-cdcd-self-use". */
function feeds(item: DisplayItem): string[] {
  const out = new Set<string>();
  for (const id of item.ids) {
    for (const to of props.lineage.downstream.get(id) ?? []) out.add(anchorOf(to));
  }
  return [...out].map((key) => (key.startsWith("group:") ? `${key.slice(6)}-*` : nameOf(key)));
}

function onChip(item: DisplayItem): void {
  if (item.kind === "group") {
    const next = new Set(openGroups.value);
    if (next.has(item.key)) next.delete(item.key);
    else next.add(item.key);
    openGroups.value = next;
  }
  emit("select", item.key === props.selected && item.kind === "node" ? "" : item.key);
}

// ── geometry ────────────────────────────────────────────────────────────────

const narrow = useMediaQuery("(max-width: 640px)");
const canvas = ref<HTMLElement | null>(null);
const paths = ref<Array<{ d: string; on: boolean; tag: boolean; key: string }>>([]);
const size = ref({ width: 0, height: 0 });

function measure(): void {
  const root = canvas.value;
  if (!root || narrow.value) {
    paths.value = [];
    return;
  }
  const base = root.getBoundingClientRect();
  size.value = { width: base.width, height: base.height };
  const boxes = new Map<string, DOMRect>();
  for (const el of root.querySelectorAll<HTMLElement>("[data-map-key]")) boxes.set(el.dataset.mapKey!, el.getBoundingClientRect());
  // Each column's box and the band its chips occupy, so an edge that skips a
  // column (a source rendered straight into a file) can go round the chips
  // instead of vanishing under one and reading as if it ended there.
  const cols = [...root.querySelectorAll<HTMLElement>("[data-map-col]")].map((el) => {
    const box = el.getBoundingClientRect();
    const chips = [...el.querySelectorAll<HTMLElement>("[data-map-key]")].map((chip) => chip.getBoundingClientRect());
    return {
      left: box.left - base.left,
      right: box.right - base.left,
      top: chips.length ? Math.min(...chips.map((c) => c.top)) - base.top : Number.POSITIVE_INFINITY,
      bottom: chips.length ? Math.max(...chips.map((c) => c.bottom)) - base.top : Number.NEGATIVE_INFINITY,
    };
  });
  const f = (n: number) => n.toFixed(1);
  const lanes = new Map<number, number>();
  const out: typeof paths.value = [];
  for (const edge of drawnEdges.value) {
    const a = boxes.get(edge.from);
    const b = boxes.get(edge.to);
    if (!a || !b) continue;
    const x1 = a.right - base.left;
    const y1 = a.top + a.height / 2 - base.top;
    const x2 = b.left - base.left;
    const y2 = b.top + b.height / 2 - base.top;
    const from = columnOf.value.get(edge.from) ?? 0;
    const to = columnOf.value.get(edge.to) ?? from + 1;
    let d: string;
    if (to - from > 1 && cols[from + 1] && cols[to - 1]) {
      const skipped = cols.slice(from + 1, to);
      const top = Math.min(...skipped.map((c) => c.top));
      const bottom = Math.max(...skipped.map((c) => c.bottom));
      // Stay at the source's height when that is already clear of the chips;
      // otherwise run in a lane under them, one lane per such edge.
      let lane = y1;
      if (y1 > top - 6 && y1 < bottom + 6) {
        const n = lanes.get(from) ?? 0;
        lanes.set(from, n + 1);
        lane = bottom + 12 + n * 6;
      }
      const xa = skipped[0]!.left - 8;
      const xb = skipped[skipped.length - 1]!.right + 8;
      const da = Math.max(16, (xa - x1) / 2);
      const db = Math.max(16, (x2 - xb) / 2);
      d = `M${f(x1)} ${f(y1)} C${f(x1 + da)} ${f(y1)} ${f(xa - da)} ${f(lane)} ${f(xa)} ${f(lane)} L${f(xb)} ${f(lane)} C${f(xb + db)} ${f(lane)} ${f(x2 - db)} ${f(y2)} ${f(x2)} ${f(y2)}`;
    } else {
      const dx = Math.max(24, (x2 - x1) / 2);
      d = `M${f(x1)} ${f(y1)} C${f(x1 + dx)} ${f(y1)} ${f(x2 - dx)} ${f(y2)} ${f(x2)} ${f(y2)}`;
    }
    out.push({ key: `${edge.from}>${edge.to}`, on: edge.on, tag: edge.tag, d });
  }
  // Lit edges last, so they are painted over the dimmed ones they cross.
  paths.value = out.sort((p, q) => Number(p.on) - Number(q.on));
}

let observer: ResizeObserver | undefined;
onMounted(() => {
  if (typeof ResizeObserver === "function" && canvas.value) {
    observer = new ResizeObserver(() => measure());
    observer.observe(canvas.value);
  }
  void nextTick(measure);
});
onBeforeUnmount(() => observer?.disconnect());
watch([columns, drawnEdges, narrow], () => void nextTick(measure), { flush: "post" });

const summary = computed(() => {
  const c = props.lineage.columns;
  const edges = props.lineage.edges.length;
  return `Lineage: ${plural(c.source.length, "source")}, ${plural(c.combination.length, "combination")}, ${plural(c.file.length, "file")}, ${plural(c.share.length, "share")}, ${edges} ${edges === 1 ? "dependency" : "dependencies"}`;
});
</script>

<template>
  <figure class="lineage" :aria-label="summary" :data-lit="lit ? 'true' : undefined">
    <!-- Wide: four columns and the edges between them. -->
    <div v-if="!narrow" ref="canvas" class="lineage-canvas" @click.self="emit('select', '')">
      <svg
        class="lineage-edges"
        role="img"
        :aria-label="`${drawnEdges.length} dependencies between the columns${lit ? `, ${drawnEdges.filter((e) => e.on).length} on the selected path` : ''}`"
        :width="size.width"
        :height="size.height"
        :viewBox="`0 0 ${size.width || 1} ${size.height || 1}`"
      >
        <path
          v-for="path in paths"
          :key="path.key"
          :d="path.d"
          class="lineage-edge"
          :data-on="lit ? (path.on ? 'true' : 'false') : undefined"
          :data-tag="path.tag ? 'true' : undefined"
        />
      </svg>
      <section v-for="stage in STAGES" :key="stage" class="lineage-col" data-map-col :aria-label="STAGE_LABEL[stage]">
        <h3 class="lineage-head">
          <span>{{ STAGE_LABEL[stage] }}</span>
          <span class="lineage-head-count">{{ lineage.columns[stage].length }}</span>
        </h3>
        <p v-if="!columns[stage].length" class="lineage-none">None</p>
        <button
          v-for="item in columns[stage]"
          :key="item.key"
          type="button"
          class="lineage-chip"
          :data-map-key="item.key"
          :data-kind="item.kind"
          :data-member="item.member ? 'true' : undefined"
          :data-state="itemState(item)"
          :aria-pressed="item.key === selected ? 'true' : 'false'"
          :aria-expanded="item.kind === 'group' ? (item.open ? 'true' : 'false') : undefined"
          :title="`${item.label}. ${factsOf(item).title}`"
          @click="onChip(item)"
        >
          <ChevronRight v-if="item.kind === 'group'" class="lineage-chevron" :size="13" aria-hidden="true" />
          <PcStateDot :tone="factsOf(item).tone" :label="''" :title="factsOf(item).state" class="lineage-dot" />
          <span class="lineage-name">{{ item.label }}</span>
          <span v-if="factsOf(item).figure" class="lineage-figure">{{ factsOf(item).figure }}</span>
        </button>
      </section>
    </div>

    <!-- Narrow: stage lists, each record with what it feeds written under it. -->
    <div v-else class="lineage-stages">
      <section v-for="stage in STAGES" :key="stage" class="lineage-stage" :aria-label="STAGE_LABEL[stage]">
        <h3 class="lineage-head">
          <span>{{ STAGE_LABEL[stage] }}</span>
          <span class="lineage-head-count">{{ lineage.columns[stage].length }}</span>
        </h3>
        <p v-if="!columns[stage].length" class="lineage-none">None</p>
        <ul class="lineage-list">
          <li
            v-for="item in columns[stage]"
            :key="item.key"
            class="lineage-entry"
            :data-member="item.member ? 'true' : undefined"
            :data-state="itemState(item)"
          >
            <button
              type="button"
              class="lineage-chip"
              :data-kind="item.kind"
              :aria-pressed="item.key === selected ? 'true' : 'false'"
              :aria-expanded="item.kind === 'group' ? (item.open ? 'true' : 'false') : undefined"
              :title="`${item.label}. ${factsOf(item).title}`"
              @click="onChip(item)"
            >
              <ChevronRight v-if="item.kind === 'group'" class="lineage-chevron" :size="13" aria-hidden="true" />
              <PcStateDot :tone="factsOf(item).tone" :label="''" :title="factsOf(item).state" class="lineage-dot" />
              <span class="lineage-name">{{ item.label }}</span>
              <span v-if="factsOf(item).figure" class="lineage-figure">{{ factsOf(item).figure }}</span>
            </button>
            <p v-if="feeds(item).length && !(item.kind === 'group' && item.open)" class="lineage-feeds">
              <span aria-hidden="true">→ </span><span class="pc-sr-only">feeds </span>{{ feeds(item).join(", ") }}
            </p>
          </li>
        </ul>
      </section>
    </div>
  </figure>
</template>
