<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { ChevronRight } from "@lucide/vue";
import { PcStateDot, useMediaQuery } from "@latticenet/plugin-bridge/chassis";

import { drawnEdges, layoutLineage, paintedEdges, type MapItem } from "../lineageLayout";
import { pathOf, plural, STAGES, type Lineage, type Stage } from "../pipeline";

/**
 * The overview's one picture: sources, combinations, files and shares as four
 * columns, one chip per record, and an edge for every dependency a record
 * declares. Selecting a chip lights its whole path, upstream and downstream,
 * so "which link does this phone fetch" and "what breaks if this provider
 * dies" are both one click.
 *
 * It stays one picture at any size (lineageLayout.ts): records that share a
 * name prefix fold into one chip, each column shows at most eight entries and
 * folds the rest into "N more", and a map with many dependencies draws paths
 * rather than all of them: the attention list's at rest, the selected one
 * otherwise. Production's 23 records are drawn whole, as before.
 *
 * An edge that skips a column runs in a lane below that column's chips, clear
 * of them, and turns in the gaps between columns; it never runs along a chip.
 * Below 640px the columns become stage lists with each record's downstream
 * written under it, because four columns of chips do not fit a phone.
 */
export interface ChipFacts {
  tone: "healthy" | "warning" | "error" | "neutral";
  state: string;
  /** Mono figure on the chip: "166→25", "counting", "/cdcd". */
  figure: string;
  title: string;
}

const props = defineProps<{
  lineage: Lineage;
  facts: (id: string) => ChipFacts;
  /** The selected node id, or a group key; "" for none. */
  selected: string;
  /** Node ids the attention list names; their paths are the map at rest. */
  attention?: readonly string[];
}>();

const emit = defineEmits<{ select: [key: string] }>();

const STAGE_LABEL: Record<Stage, string> = {
  source: "Sources",
  combination: "Combinations",
  file: "Files",
  share: "Shares",
};
const STAGE_NOUN: Record<Stage, string> = { source: "source", combination: "combination", file: "file", share: "share" };

const openGroups = ref(new Set<string>());
const expanded = ref(new Set<Stage>());

const attentionIds = computed(() => (props.attention ?? []).filter((id) => props.lineage.nodes.has(id)));
const attentionPath = computed(() => {
  const out = new Set<string>();
  for (const id of attentionIds.value) for (const on of pathOf(props.lineage, id)) out.add(on);
  return out;
});

/** Groups by key, from a layout that folds nothing, so a group key always resolves. */
const groupMembers = computed(() => {
  const all = layoutLineage(props.lineage, { openGroups: new Set(), expanded: new Set(STAGES), pinned: new Set() });
  const out = new Map<string, string[]>();
  for (const stage of STAGES) for (const item of all.columns[stage]) if (item.kind === "group") out.set(item.key, item.ids);
  return { members: out, groupOf: all.groupOf };
});

/** The node ids the selection stands for: one record, or a group's members. */
const selectedIds = computed<string[]>(() => {
  const key = props.selected;
  if (!key) return [];
  return groupMembers.value.members.get(key) ?? (props.lineage.nodes.has(key) ? [key] : []);
});

/** The lit path: the selection's own path, or the union of a group's. */
const lit = computed<Set<string> | null>(() => {
  const key = props.selected;
  if (!key) return null;
  const members = groupMembers.value.members.get(key);
  if (members) {
    const out = new Set<string>();
    for (const id of members) for (const on of pathOf(props.lineage, id)) out.add(on);
    return out;
  }
  if (!props.lineage.nodes.has(key)) return null;
  return pathOf(props.lineage, key);
});

// A record selected from elsewhere (the side panel, a table, the palette)
// opens the group it sits in, or it would be lit inside a folded chip.
watch(
  () => props.selected,
  (key) => {
    const group = groupMembers.value.groupOf.get(key);
    if (group && !openGroups.value.has(group)) openGroups.value = new Set([...openGroups.value, group]);
  },
  { immediate: true },
);

const layout = computed(() =>
  layoutLineage(props.lineage, {
    openGroups: openGroups.value,
    expanded: expanded.value,
    // The selection and the records attention names stay drawn. The rest of a
    // selected path keeps the caps: an edge into a folded entry ends at its
    // column's "more", which says the path goes on in there.
    pinned: new Set([...selectedIds.value, ...attentionIds.value]),
  }),
);
const columns = computed(() => layout.value.columns);

const labelOf = computed(() => {
  const out = new Map<string, string>();
  for (const stage of STAGES) for (const item of columns.value[stage]) out.set(item.key, item.label);
  return out;
});

function itemState(item: MapItem): "selected" | "on" | "off" | undefined {
  if (!lit.value) return undefined;
  if (item.key === props.selected) return "selected";
  return item.ids.some((id) => lit.value!.has(id)) ? "on" : "off";
}

/** The column each drawn item sits in, so an edge knows which columns it skips. */
const columnOf = computed(() => {
  const out = new Map<string, number>();
  STAGES.forEach((stage, index) => {
    for (const item of columns.value[stage]) out.set(item.key, index);
  });
  return out;
});

const allEdges = computed(() => drawnEdges(props.lineage, layout.value.anchor, lit.value, attentionPath.value));
const painted = computed(() => paintedEdges(allEdges.value, !!lit.value));

/** Said once above a dense map, so an absent edge never reads as an absent dependency. */
const note = computed(() => {
  if (!painted.value.dense) return "";
  const total = allEdges.value.length;
  if (lit.value) return `Showing the selected path. ${total} dependencies in all; clear the selection to see the paths that need attention.`;
  if (attentionIds.value.length) {
    return `Showing the paths of the ${plural(attentionIds.value.length, "record")} the attention list names, not all ${total} dependencies. Select a record to see its path.`;
  }
  return `${total} dependencies are too many to draw at once. Select a record to see its path.`;
});

// ── facts per drawn item ────────────────────────────────────────────────────

function factsOf(item: MapItem): ChipFacts {
  if (item.kind === "node") return props.facts(item.key);
  if (item.kind === "more") {
    return {
      tone: "neutral",
      state: "",
      figure: "",
      title: item.open ? "Fold this column back to its first entries." : `${plural(item.ids.length, STAGE_NOUN[item.stage])} not shown. Show every one.`,
    };
  }
  const noun = STAGE_NOUN[item.stage];
  const facts = item.ids.map((id) => props.facts(id));
  const errors = facts.filter((f) => f.tone === "error").length;
  const warnings = facts.filter((f) => f.tone === "warning").length;
  const worst = facts.find((f) => f.tone === "error") ?? facts.find((f) => f.tone === "warning");
  const toggle = item.open ? "Fold them" : "Show them";
  if (item.stage === "file") {
    const published = facts.filter((f) => f.tone === "healthy").length;
    // Healthy only when every file in the group is served: one published file
    // out of nine is not a green group.
    const all = published === item.ids.length;
    return {
      tone: worst?.tone ?? (all ? "healthy" : "neutral"),
      state: worst?.state ?? (all ? "all published" : `${published} of ${item.ids.length} published`),
      figure: plural(item.ids.length, noun),
      title: `${plural(item.ids.length, noun)} named ${item.label}, ${published} published. ${toggle}.`,
    };
  }
  const healthy = facts.every((f) => f.tone === "healthy");
  const trouble = errors + warnings;
  return {
    tone: worst?.tone ?? (healthy ? "healthy" : "neutral"),
    state: worst ? `${trouble} of ${item.ids.length} need attention` : healthy ? "all ok" : `${plural(item.ids.length, noun)}`,
    figure: plural(item.ids.length, noun),
    title: `${plural(item.ids.length, noun)} named ${item.label}${trouble ? `, ${trouble} with a problem` : ""}. ${toggle}.`,
  };
}

/**
 * What an item feeds, for the stage lists: "merge-cd-openjobs, for-cdcd-*".
 * Records folded under a column's "more" are counted, not named, and the
 * count is only the ones this item feeds: "2 more shares".
 */
function feeds(item: MapItem): string[] {
  const shown = new Set<string>();
  const folded = new Map<string, Set<string>>();
  for (const id of item.ids) {
    for (const to of props.lineage.downstream.get(id) ?? []) {
      const at = layout.value.anchor.get(to) ?? to;
      if (at.startsWith("more:")) (folded.get(at) ?? folded.set(at, new Set()).get(at)!).add(to);
      else shown.add(at);
    }
  }
  const named = [...shown].map((key) => labelOf.value.get(key) ?? props.lineage.nodes.get(key)?.label ?? key);
  for (const [key, ids] of folded) {
    const stage = key.slice("more:".length) as Stage;
    named.push(`${ids.size} more ${ids.size === 1 ? STAGE_NOUN[stage] : `${STAGE_NOUN[stage]}s`}`);
  }
  return named;
}

function onChip(item: MapItem): void {
  if (item.kind === "more") {
    const next = new Set(expanded.value);
    if (next.has(item.stage)) next.delete(item.stage);
    else next.add(item.stage);
    expanded.value = next;
    return;
  }
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
/** Room under the columns for the lanes of edges that skip a column. */
const laneRoom = ref(0);

/** How far below a column's last chip a lane runs, and how far apart lanes are. */
const LANE_CLEAR = 20;
const LANE_STEP = 8;

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
  const cols = [...root.querySelectorAll<HTMLElement>("[data-map-col]")].map((el) => {
    const box = el.getBoundingClientRect();
    const chips = [...el.querySelectorAll<HTMLElement>("[data-map-key]")].map((chip) => chip.getBoundingClientRect());
    return {
      left: box.left - base.left,
      right: box.right - base.left,
      bottom: chips.length ? Math.max(...chips.map((c) => c.bottom)) - base.top : 0,
    };
  });
  const f = (n: number) => n.toFixed(1);
  let lanes = 0;
  let deepest = 0;
  const out: typeof paths.value = [];
  for (const edge of painted.value.edges) {
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
      // Skipping a column: turn down in the gap after the source's column,
      // cross under every skipped column's last chip with room to spare, and
      // turn up in the gap before the target's. One lane per such edge.
      const skipped = cols.slice(from + 1, to);
      const clear = Math.max(...skipped.map((c) => c.bottom)) + LANE_CLEAR;
      // Already below every skipped chip: stay level. Otherwise take a lane.
      let lane = y1;
      if (y1 < clear) {
        lane = clear + lanes * LANE_STEP;
        lanes += 1;
      }
      deepest = Math.max(deepest, lane);
      const gapA = (cols[from]!.right + skipped[0]!.left) / 2;
      const gapB = (skipped[skipped.length - 1]!.right + cols[to]!.left) / 2;
      d = orthogonalPath([[x1, y1], [gapA, y1], [gapA, lane], [gapB, lane], [gapB, y2], [x2, y2]], 10);
    } else {
      const dx = Math.max(24, (x2 - x1) / 2);
      d = `M${f(x1)} ${f(y1)} C${f(x1 + dx)} ${f(y1)} ${f(x2 - dx)} ${f(y2)} ${f(x2)} ${f(y2)}`;
    }
    out.push({ key: `${edge.from}>${edge.to}`, on: edge.on, tag: edge.tag, d });
  }
  // The canvas grows to hold its lowest lane rather than clip it.
  const contentBottom = base.height - laneRoom.value;
  const need = deepest ? Math.max(0, Math.ceil(deepest + 12 - contentBottom)) : 0;
  if (need !== laneRoom.value) laneRoom.value = need;
  // Lit edges last, so they are painted over the ones they cross.
  paths.value = out.sort((p, q) => Number(p.on) - Number(q.on));
}

/**
 * A polyline through the given points with each corner rounded, skipping
 * points that do not turn. The radius shrinks on a short segment so a corner
 * never overshoots the next one.
 */
function orthogonalPath(points: Array<[number, number]>, radius: number): string {
  const f = (n: number) => n.toFixed(1);
  const pts = points.filter((point, index) => {
    const prev = points[index - 1];
    return !prev || prev[0] !== point[0] || prev[1] !== point[1];
  });
  const turns = pts.filter((point, index) => {
    if (index === 0 || index === pts.length - 1) return true;
    const a = pts[index - 1]!;
    const b = pts[index + 1]!;
    return !((a[0] === point[0] && point[0] === b[0]) || (a[1] === point[1] && point[1] === b[1]));
  });
  let d = `M${f(turns[0]![0])} ${f(turns[0]![1])}`;
  for (let index = 1; index < turns.length - 1; index += 1) {
    const [px, py] = turns[index - 1]!;
    const [cx, cy] = turns[index]!;
    const [nx, ny] = turns[index + 1]!;
    const inLen = Math.hypot(cx - px, cy - py);
    const outLen = Math.hypot(nx - cx, ny - cy);
    const r = Math.min(radius, inLen / 2, outLen / 2);
    const ax = cx - ((cx - px) / (inLen || 1)) * r;
    const ay = cy - ((cy - py) / (inLen || 1)) * r;
    const bx = cx + ((nx - cx) / (outLen || 1)) * r;
    const by = cy + ((ny - cy) / (outLen || 1)) * r;
    d += ` L${f(ax)} ${f(ay)} Q${f(cx)} ${f(cy)} ${f(bx)} ${f(by)}`;
  }
  const last = turns[turns.length - 1]!;
  return `${d} L${f(last[0])} ${f(last[1])}`;
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
watch([columns, painted, narrow, canvas], () => void nextTick(measure), { flush: "post" });

const summary = computed(() => {
  const c = props.lineage.columns;
  const edges = props.lineage.edges.length;
  return `Lineage: ${plural(c.source.length, "source")}, ${plural(c.combination.length, "combination")}, ${plural(c.file.length, "file")}, ${plural(c.share.length, "share")}, ${edges} ${edges === 1 ? "dependency" : "dependencies"}`;
});

function chipTitle(item: MapItem): string {
  return item.kind === "more" ? factsOf(item).title : `${item.label}. ${factsOf(item).title}`;
}
</script>

<template>
  <figure class="lineage" :aria-label="summary" :data-lit="lit ? 'true' : undefined">
    <p v-if="note && !narrow" class="lineage-note">{{ note }}</p>
    <!-- Wide: four columns and the edges between them. -->
    <div v-if="!narrow" ref="canvas" class="lineage-canvas" :style="laneRoom ? { paddingBottom: `calc(var(--space-4) + ${laneRoom}px)` } : undefined" @click.self="emit('select', '')">
      <svg
        class="lineage-edges"
        role="img"
        :aria-label="`${painted.edges.length} of ${allEdges.length} dependencies drawn${lit ? `, ${allEdges.filter((e) => e.on).length} on the selected path` : ''}`"
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
          :aria-pressed="item.kind === 'more' ? undefined : item.key === selected ? 'true' : 'false'"
          :aria-expanded="item.kind === 'node' ? undefined : item.open ? 'true' : 'false'"
          :title="chipTitle(item)"
          @click="onChip(item)"
        >
          <ChevronRight v-if="item.kind === 'group'" class="lineage-chevron" :size="13" aria-hidden="true" />
          <PcStateDot v-if="item.kind !== 'more'" :tone="factsOf(item).tone" :label="''" :title="factsOf(item).state" class="lineage-dot" />
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
              :aria-pressed="item.kind === 'more' ? undefined : item.key === selected ? 'true' : 'false'"
              :aria-expanded="item.kind === 'node' ? undefined : item.open ? 'true' : 'false'"
              :title="chipTitle(item)"
              @click="onChip(item)"
            >
              <ChevronRight v-if="item.kind === 'group'" class="lineage-chevron" :size="13" aria-hidden="true" />
              <PcStateDot v-if="item.kind !== 'more'" :tone="factsOf(item).tone" :label="''" :title="factsOf(item).state" class="lineage-dot" />
              <span class="lineage-name">{{ item.label }}</span>
              <span v-if="factsOf(item).figure" class="lineage-figure">{{ factsOf(item).figure }}</span>
            </button>
            <p v-if="item.kind !== 'more' && feeds(item).length && !(item.kind === 'group' && item.open)" class="lineage-feeds">
              <span aria-hidden="true">→ </span><span class="pc-sr-only">feeds </span>{{ feeds(item).join(", ") }}
            </p>
          </li>
        </ul>
      </section>
    </div>
  </figure>
</template>
