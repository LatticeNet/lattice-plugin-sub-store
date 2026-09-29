<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch, type Component } from "vue";
import { ChevronDown, FileCode, Layers, Library, Link2, Plus, RefreshCw, Search, Settings, SquareArrowOutUpRight, Store, Workflow } from "@lucide/vue";
import {
  PcButton,
  PcIconButton,
  PcLensTab,
  PcLensTabs,
  PcNotice,
  PcPageHeader,
  PcProofLine,
  PcToolbar,
  PcWorkspace,
  useDocumentQueryState,
} from "@latticenet/plugin-bridge/chassis";

import { useHandshakeTimeout } from "./handshakeTimeout";
import { useHost } from "./host";
import CommandPalette from "./components/CommandPalette.vue";
import RecordSidePanel from "./components/RecordSidePanel.vue";
import { recordIntent } from "./recordIntent";
import type { ActionCapabilities, ActionId } from "./recordActions";
import type { PaletteCommandId } from "./commandPalette";
import { KIND_COLLECTION, KIND_FILE, KIND_SUB, MAX_SUBSCRIPTION_RECORDS, type SubscriptionListItem } from "./client";
import StandaloneNotice from "./components/StandaloneNotice.vue";
import OverviewScreen from "./screens/OverviewScreen.vue";
import RecordPage from "./screens/RecordPage.vue";
import SubscriptionsScreen from "./screens/SubscriptionsScreen.vue";
import FilesScreen from "./screens/FilesScreen.vue";
import SettingsScreen from "./screens/SettingsScreen.vue";
import SharesScreen from "./screens/SharesScreen.vue";
import { createLensChrome, provideLensChrome, type Facets, type TabId } from "./lensChrome";
import { SHARES_LIST_ROUTE, hostOriginFromHash, postNavigate } from "./navigate";
import { VIEW_IDS, viewOfKind } from "./pipeline";
import { publishStateFor, shareStateOf } from "./shareState";
import { usePipeline } from "./usePipeline";

/**
 * The plugin's page, with no knowledge of how the host is reached.
 *
 * The layers of design 22: Overview (what is wrong, then the pipeline as one
 * picture), then one table per kind of record (Sources, Combinations, Files,
 * Shares), then Settings. One tab row, mirrored in the address as `?view=`.
 * A row or a chip opens the record in a side panel (`?open=<id>`); "Open
 * page" gives it the whole frame (`?record=<id>`), which replaces the tab row
 * rather than stacking a second one under it. The same screens are mounted
 * against a fake host in `dev/`.
 */

const host = useHost();

/**
 * The handshake failed or never came. Outside the console that is permanent,
 * so the lenses, each of which would show "Loading…" forever, are replaced by
 * one honest notice. A handshake that lands late flips this back off.
 */
const handshakeExpired = useHandshakeTimeout(host.init);
const standalone = computed(
  () => !host.init.value && (handshakeExpired.value || !!host.bootError.value),
);

interface Layer {
  id: TabId;
  label: string;
  icon: Component;
  screen: Component;
  props?: Record<string, unknown>;
}

const tabs: Layer[] = [
  { id: "overview", label: "Overview", icon: Workflow, screen: OverviewScreen },
  { id: "sources", label: "Sources", icon: Library, screen: SubscriptionsScreen, props: { kind: KIND_SUB } },
  { id: "combinations", label: "Combinations", icon: Layers, screen: SubscriptionsScreen, props: { kind: KIND_COLLECTION } },
  { id: "files", label: "Files", icon: FileCode, screen: FilesScreen },
  // The record list from the client's side: every link the console serves.
  { id: "shares", label: "Shares", icon: Link2, screen: SharesScreen },
  { id: "settings", label: "Settings", icon: Settings, screen: SettingsScreen },
];
const TAB_IDS = new Set<string>(VIEW_IDS);

/**
 * Where the operator is, on the document query, so a link can carry the
 * layer, the open record and the facet being discussed. Reading and writing
 * are guarded: the frame runs in an opaque-origin sandbox where a history
 * write may be refused, and a refused write must cost nothing. `?lens=` is
 * the address this page used before its layers, and still lands.
 */
const query = useDocumentQueryState();
function readParam(key: string): string {
  try {
    return query.read(key)[0] ?? "";
  } catch {
    return "";
  }
}
function writeParam(key: string, value: string): void {
  try {
    query.write(key, value ? [value] : []);
  } catch {
    // A sandbox that refuses history writes keeps the state in memory only.
  }
}
const LEGACY_LENS: Record<string, TabId> = { subscriptions: "sources", files: "files", shares: "shares", settings: "settings" };
function viewFromQuery(): TabId {
  const asked = readParam("view");
  if (TAB_IDS.has(asked)) return asked as TabId;
  return LEGACY_LENS[readParam("lens")] ?? "overview";
}

const activeTab = ref<TabId>(viewFromQuery());
const recordId = ref(readParam("record"));
/**
 * The layer a record page was opened from, for its back link. Empty when the
 * page was the landing (a shared link): back then goes to the layer that
 * lists the record.
 */
const recordFrom = ref<string>(recordId.value ? "" : activeTab.value);

/** The toolbar state the visible layer filters on, and what it reports back. */
const chrome = createLensChrome();
chrome.openId.value = readParam("open");
chrome.facets.published = readParam("published");
chrome.facets.origin = readParam("origin");

watch(activeTab, (tab) => {
  writeParam("view", tab === "overview" ? "" : tab);
  writeParam("lens", "");
});
watch(recordId, (id) => writeParam("record", id));
watch(() => chrome.openId.value, (id) => writeParam("open", id));
watch(() => chrome.facets.published, (value) => writeParam("published", value));
watch(() => chrome.facets.origin, (value) => writeParam("origin", value));

chrome.openLens = (tab, facets?: Partial<Facets>) => {
  recordId.value = "";
  activeTab.value = tab;
  if (facets) {
    chrome.facets.published = facets.published ?? "";
    chrome.facets.origin = facets.origin ?? "";
  }
};
chrome.openRecord = (id) => {
  chrome.openId.value = id;
};
chrome.openPage = (id) => {
  if (!recordId.value) recordFrom.value = activeTab.value;
  chrome.openId.value = "";
  recordId.value = id;
};
provideLensChrome(chrome);

const lens = computed(() => chrome.lenses[activeTab.value]);
/** Inside an editor the list controls make no sense; the tabs stay. */
const editing = computed(() => lens.value.editing);

const current = computed<{ key: string; screen: Component; props: Record<string, unknown> }>(() => {
  if (recordId.value) {
    return {
      key: "record",
      screen: RecordPage,
      props: { id: recordId.value, from: recordFrom.value, onBack: backFromRecord, onEdit: editRecord },
    };
  }
  const tab = tabs.find((entry) => entry.id === activeTab.value) ?? tabs[0]!;
  return { key: tab.id, screen: tab.screen, props: tab.props ?? {} };
});

/**
 * One search across the layers.
 *
 * The shell is the only place that can see every record and can switch tabs,
 * so the palette lives here. It reads the shared catalogue rather than a list
 * of its own, and it hands the chosen action to the owning screen through an
 * intent rather than reaching into that screen's state.
 */
const pipe = usePipeline(host);
const catalogue = pipe.catalogue;
const intent = recordIntent(host);
const shareStore = pipe.shareStore;

const ready = computed(() => catalogue.state.value === "ready");
const records = computed(() => (ready.value ? catalogue.items.value : []));
const singles = computed(() => records.value.filter((item) => (item.kind || KIND_SUB) === KIND_SUB));
const combos = computed(() => records.value.filter((item) => item.kind === KIND_COLLECTION));
const files = computed(() => records.value.filter((item) => item.kind === KIND_FILE));

/**
 * The counts on the tabs, from the same two lists the layers render: the
 * record catalogue and the share store. Null until the list has been read,
 * and then the badge stays away rather than claiming zero.
 */
const tabCounts = computed<Record<TabId, number | null>>(() => ({
  overview: null,
  sources: ready.value ? singles.value.length : null,
  combinations: ready.value ? combos.value.length : null,
  files: ready.value ? files.value.length : null,
  shares: shareStore.shares.value ? shareStore.shares.value.length : null,
  settings: null,
}));

/**
 * Live-share and published counts, from the same two lists the layers render.
 * They land in the proof line rather than a strip of tiles.
 */
const shareFacts = computed(() => {
  const shares = shareStore.shares.value;
  if (!shares) return null;
  const now = Date.now();
  const live = shares.filter((share) => shareStateOf(share, now).tone === "ok").length;
  return { total: shares.length, live, dead: shares.length - live };
});
const publishedRecords = computed(() =>
  shareStore.shares.value === undefined
    ? null
    : records.value.filter((item) => publishStateFor(shareStore.shares.value, item.id).tone === "ok").length,
);

/** When the catalogue or the share list was last read, for the proof line. */
const observedAt = ref("");
function stamp(): void {
  const now = new Date();
  observedAt.value = [now.getHours(), now.getMinutes(), now.getSeconds()].map((n) => String(n).padStart(2, "0")).join(":");
}
watch(() => catalogue.items.value, () => { if (ready.value) stamp(); }, { flush: "sync" });
watch(() => shareStore.shares.value, (value) => { if (value) stamp(); });

const proof = computed(() => {
  if (catalogue.state.value === "error") return ["the record catalogue could not be read"];
  if (!ready.value) return ["waiting for the record catalogue"];
  const parts = [`observed at ${observedAt.value || "..."}`, `${records.value.length} records`];
  const shares = shareFacts.value;
  if (shares) parts.push(`${shares.live} share${shares.live === 1 ? "" : "s"} live`);
  else if (shareStore.error.value) parts.push("share list unread");
  return parts;
});
/** Warning ink only when the store has records and none of them is live. */
const publishedLabel = computed(() => {
  if (!ready.value || publishedRecords.value === null) return "";
  return `${publishedRecords.value} published`;
});
const publishedWarn = computed(() => publishedRecords.value === 0 && records.value.length > 0);

/**
 * The shell reads both lists itself when the handshake lands, so the proof
 * line is true whichever layer opened first: a frame opened on Settings would
 * otherwise wait for a catalogue no layer asked for. A layer asking at the
 * same moment joins the same read.
 */
watch(host.init, (value) => {
  if (!value) return;
  void shareStore.load();
  if (catalogue.state.value === "idle") void catalogue.reload();
}, { immediate: true });

/** Read the catalogue and the share list again; the layer on screen follows. */
const refreshing = ref(false);
async function refresh(): Promise<void> {
  if (refreshing.value) return;
  refreshing.value = true;
  try {
    await Promise.all([catalogue.reload(), shareStore.load()]);
  } finally {
    refreshing.value = false;
  }
}

const paletteOpen = ref(false);
const addMenuOpen = ref(false);
const addMenuAnchor = ref<HTMLElement | null>(null);
const addMenuPlace = ref<{ top: string; right: string } | null>(null);

function placeAddMenu(): void {
  const rect = addMenuAnchor.value?.getBoundingClientRect();
  if (!rect) return;
  addMenuPlace.value = {
    top: `${rect.bottom + window.scrollY + 4}px`,
    right: `${document.documentElement.clientWidth - rect.right - window.scrollX}px`,
  };
}

function closeAddMenu(): void {
  addMenuOpen.value = false;
  addMenuPlace.value = null;
}

function toggleAddMenu(): void {
  addMenuOpen.value = !addMenuOpen.value;
  if (addMenuOpen.value) {
    placeAddMenu();
  } else {
    addMenuPlace.value = null;
  }
}

/**
 * The capabilities the palette reasons with.
 *
 * The shell does not hold a subscriptions hook and should not grow one to ask
 * five booleans, so it reads the same manifest the hook does. `available` is
 * the host's own answer about what the signed bundle declares.
 */
const caps = computed<ActionCapabilities>(() => {
  const declared = (service: string, method: string) => host.available({ service, method, status: "active" });
  const S = "latticenet.sub-store/subscription";
  return {
    ready: !!host.init.value,
    mutate: declared(S, "save") && declared(S, "delete"),
    fetch: declared(S, "probe"),
    preview: declared(S, "preview"),
    render: declared(S, "render"),
    publish: declared(S, "publish"),
  };
});

/**
 * The page's one primary action per layer, and why it may be missing.
 *
 * A verb the session may not perform is absent, not disabled, and the reason
 * takes its place as a note; a verb the store cannot take right now (the
 * record budget is spent) stays, disabled, with the reason as its title.
 */
const atRecordLimit = computed(() => ready.value && catalogue.items.value.length >= MAX_SUBSCRIPTION_RECORDS);
const LIMIT_REASON = `The store holds ${MAX_SUBSCRIPTION_RECORDS} records; delete one to add another`;
const canCreate = computed(() => caps.value.ready && caps.value.mutate);
const shareOrigin = computed(() => hostOriginFromHash(typeof window === "undefined" ? "" : window.location.hash));
const NO_ORIGIN = "This frame cannot ask the console to navigate; open Networking → Subscription Shares yourself.";

function openPalette(): void {
  paletteOpen.value = true;
}

/**
 * Cmd/Ctrl+K, inside this frame only.
 *
 * The console binds the same key on its own document. A cross-origin sandboxed
 * frame does not propagate key events to its parent, so whichever surface has
 * focus answers, and the two palettes never contend. The visible button in the
 * toolbar is the entry that does not depend on that: a feature reachable only
 * by a shortcut is a feature most operators never find.
 */
function onKeydown(event: KeyboardEvent): void {
  if (event.key === "Escape" && addMenuOpen.value) {
    closeAddMenu();
    return;
  }
  if (event.key !== "k" || !(event.metaKey || event.ctrlKey)) return;
  event.preventDefault();
  paletteOpen.value = !paletteOpen.value;
}

function onDocumentClick(event: MouseEvent): void {
  if (!addMenuOpen.value) return;
  const target = event.target as HTMLElement | null;
  if (target?.closest("[data-add-menu]")) return;
  closeAddMenu();
}

onMounted(() => {
  document.addEventListener("keydown", onKeydown);
  document.addEventListener("click", onDocumentClick, true);
});
onBeforeUnmount(() => {
  document.removeEventListener("keydown", onKeydown);
  document.removeEventListener("click", onDocumentClick, true);
});
watch([activeTab, recordId], () => {
  closeAddMenu();
  fadeLens();
});

const lensFading = ref(false);
function fadeLens(): void {
  lensFading.value = false;
  requestAnimationFrame(() => {
    lensFading.value = true;
  });
}

function runFromPalette(record: SubscriptionListItem, action: ActionId): void {
  chrome.openLens(viewOfKind(record.kind));
  intent.value = { recordId: record.id, action };
}

function runCommand(command: PaletteCommandId): void {
  closeAddMenu();
  chrome.openLens(command === "new-file" ? "files" : command === "new-collection" ? "combinations" : "sources");
  intent.value = { command };
}

/** The editor belongs to the layer that lists the record; the page hands over. */
function editRecord(id: string): void {
  const record = pipe.item(id);
  if (!record) return;
  chrome.openId.value = "";
  chrome.openLens(viewOfKind(record.kind));
  intent.value = { recordId: id, action: "edit" };
}

function backFromRecord(): void {
  const from = recordFrom.value as TabId;
  const own = viewOfKind(pipe.item(recordId.value)?.kind);
  recordId.value = "";
  activeTab.value = TAB_IDS.has(from) ? from : own;
}

function openShares(): void {
  if (!shareOrigin.value) return;
  postNavigate(window, SHARES_LIST_ROUTE, shareOrigin.value);
}

const comboDisabled = computed(() => atRecordLimit.value || !singles.value.length);
const comboTitle = computed(() =>
  !singles.value.length
    ? "Create a subscription first. There is nothing to combine"
    : atRecordLimit.value
      ? LIMIT_REASON
      : "Merge several subscriptions and process the result as one",
);
</script>

<template>
  <PcWorkspace :batch="lens.selected > 0">
    <PcPageHeader
      title="Sub-Store"
      description="Build subscriptions from sources, render them for each client, and publish them from Lattice itself."
    >
      <template #icon><Store :size="19" aria-hidden="true" /></template>
      <template #actions>
        <PcIconButton
          label="Read the record catalogue and the share list again"
          bordered
          :disabled="!host.init.value"
          @click="refresh()"
        >
          <RefreshCw :size="15" :class="{ spin: refreshing }" aria-hidden="true" />
        </PcIconButton>
      </template>
      <template #proof>
        <PcProofLine :segments="proof" :refreshing="refreshing">
          <span v-if="publishedLabel" class="proof-seg" :class="{ 'is-warn': publishedWarn }">· {{ publishedLabel }}</span>
        </PcProofLine>
      </template>
    </PcPageHeader>

    <StandaloneNotice v-if="standalone" :detail="host.bootError.value" />

    <template v-else>
      <PcNotice v-if="host.bootError.value" tone="danger" title="The console refused the handshake">
        {{ host.bootError.value }}
      </PcNotice>

      <!-- A record's own page has its own tab row; the layer tabs give way to
           it rather than stacking a second row above. -->
      <PcToolbar v-if="!recordId" label="Sub-Store layers">
        <template #tabs>
          <PcLensTabs v-model="activeTab" label="Sub-Store layers">
            <PcLensTab
              v-for="tab in tabs"
              :key="tab.id"
              :value="tab.id"
              :label="tab.label"
              :count="tabCounts[tab.id]"
            >
              <template #icon><component :is="tab.icon" :size="14" aria-hidden="true" /></template>
            </PcLensTab>
          </PcLensTabs>
        </template>
        <template v-if="!editing" #secondary>
          <!-- Not only a shortcut: a palette reachable only by Cmd+K is one most
               operators never find. Outside the tablist, because a button in
               there announces itself as a tab and joins the arrow-key order. -->
          <PcIconButton class="tab-search" label="Search records and actions (Cmd+K)" bordered @click="openPalette()">
            <Search :size="15" aria-hidden="true" />
          </PcIconButton>
        </template>
        <template v-if="!editing && activeTab === 'overview' && canCreate" #primary>
          <div class="add-split" data-add-menu>
            <PcButton variant="primary" :disabled="atRecordLimit" :title="atRecordLimit ? LIMIT_REASON : 'One source of nodes, processed and served'" @click="runCommand('new-subscription')">
              <template #icon><Plus :size="15" aria-hidden="true" /></template>
              New subscription
            </PcButton>
            <button
              ref="addMenuAnchor"
              class="add-split-caret"
              type="button"
              :aria-expanded="addMenuOpen"
              aria-haspopup="menu"
              aria-label="More things to create"
              title="More things to create"
              @click="toggleAddMenu()"
            >
              <ChevronDown :size="14" aria-hidden="true" />
            </button>
          </div>
        </template>
        <template v-else-if="!editing && activeTab === 'sources' && canCreate" #primary>
          <PcButton variant="primary" :disabled="atRecordLimit" :title="atRecordLimit ? LIMIT_REASON : 'One source of nodes, processed and served'" @click="runCommand('new-subscription')">
            <template #icon><Plus :size="15" aria-hidden="true" /></template>
            New subscription
          </PcButton>
        </template>
        <template v-else-if="!editing && activeTab === 'combinations' && canCreate" #primary>
          <PcButton variant="primary" :disabled="comboDisabled" :title="comboTitle" @click="runCommand('new-collection')">
            <template #icon><Plus :size="15" aria-hidden="true" /></template>
            New combination
          </PcButton>
        </template>
        <template v-else-if="!editing && activeTab === 'files' && canCreate" #primary>
          <PcButton variant="primary" :disabled="atRecordLimit" :title="atRecordLimit ? LIMIT_REASON : 'A document served as it is, with its proxy list kept in step'" @click="runCommand('new-file')">
            <template #icon><Plus :size="15" aria-hidden="true" /></template>
            New file
          </PcButton>
        </template>
        <template v-else-if="!editing && activeTab === 'shares'" #primary>
          <PcButton
            variant="primary"
            :disabled="!shareOrigin"
            :title="shareOrigin ? 'Shares are created in the console under Networking.' : NO_ORIGIN"
            @click="openShares()"
          >
            <template #icon><SquareArrowOutUpRight :size="15" aria-hidden="true" /></template>
            Open in Networking
          </PcButton>
        </template>
      </PcToolbar>

      <!-- The panel attributes live on a real wrapper element.
           Passing them to <component :is> put them on a screen whose root is a
           fragment, where Vue drops them: aria-controls pointed at nothing, and
           there was no tabpanel at all. The ids are the ones the layer tabs
           point at. -->
      <div
        :id="recordId ? 'record-page' : `pc-panel-${activeTab}`"
        class="lens-panel"
        :class="{ 'is-fading': lensFading }"
        :role="recordId ? undefined : 'tabpanel'"
        :aria-labelledby="recordId ? undefined : `pc-tab-${activeTab}`"
        tabindex="-1"
      >
        <KeepAlive>
          <component :is="current.screen" :key="current.key" v-bind="current.props" />
        </KeepAlive>
      </div>

      <RecordSidePanel
        :id="chrome.openId.value"
        :pipe="pipe"
        :can-edit="caps.mutate"
        @close="chrome.openId.value = ''"
        @open="(id) => (chrome.openId.value = id)"
        @page="(id) => chrome.openPage(id)"
        @edit="editRecord"
      />

      <Teleport to="body">
        <div
          v-if="addMenuOpen"
          class="rec-menu add-split-menu"
          role="menu"
          data-add-menu
          :style="addMenuPlace ?? undefined"
        >
          <button
            type="button"
            role="menuitem"
            :disabled="comboDisabled"
            :title="comboTitle"
            @click="runCommand('new-collection')"
          >
            <Layers :size="14" aria-hidden="true" />
            New combination
          </button>
          <button
            type="button"
            role="menuitem"
            :disabled="atRecordLimit"
            :title="atRecordLimit ? LIMIT_REASON : 'A client file rendered from a source or combination'"
            @click="runCommand('new-file')"
          >
            <FileCode :size="14" aria-hidden="true" />
            New file
          </button>
          <p v-if="comboDisabled" class="rec-menu-note">{{ comboTitle }}</p>
        </div>
      </Teleport>
      <CommandPalette
        :open="paletteOpen"
        :records="catalogue.items.value"
        :caps="caps"
        @close="paletteOpen = false"
        @run="runFromPalette"
        @command="runCommand"
      />
    </template>
  </PcWorkspace>
</template>
