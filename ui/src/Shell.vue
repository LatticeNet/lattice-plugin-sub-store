<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch, type Component } from "vue";
import { ChevronDown, FileCode, Layers, Plus, RefreshCw, Search, SquareArrowOutUpRight, Store } from "@lucide/vue";
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
} from "@latticenet/plugin-bridge/chassis";

import { useHandshakeTimeout } from "./handshakeTimeout";
import { useHost } from "./host";
import CommandPalette from "./components/CommandPalette.vue";
import RecordSidePanel from "./components/RecordSidePanel.vue";
import { recordIntent } from "./recordIntent";
import { actionCapabilities, type ActionCapabilities, type ActionId } from "./recordActions";
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
import { createStateSender, decodeShellState, encodeShellState, type ShellState } from "./pageState";
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

/**
 * The layers, as one underline row like vpn-core's (design 23 section 3.4):
 * a layer is a place in the page, so it reads as a tab under the header, not
 * as a boxed pill with an icon. Pills stay for modes inside a layer.
 */
interface Layer {
  id: TabId;
  label: string;
  screen: Component;
  props?: Record<string, unknown>;
}

const tabs: Layer[] = [
  { id: "overview", label: "Overview", screen: OverviewScreen },
  { id: "sources", label: "Sources", screen: SubscriptionsScreen, props: { kind: KIND_SUB } },
  { id: "combinations", label: "Combinations", screen: SubscriptionsScreen, props: { kind: KIND_COLLECTION } },
  { id: "files", label: "Files", screen: FilesScreen },
  // The record list from the client's side: every link the console serves.
  { id: "shares", label: "Shares", screen: SharesScreen },
  { id: "settings", label: "Settings", screen: SettingsScreen },
];
const TAB_IDS = new Set<string>(VIEW_IDS);

/**
 * Where the operator is: the layer, the open record, the filters. It lives in
 * the console's address, not the frame's, because the console rebuilds the
 * frame URL with no query on every reload; the bridge hands it over at the
 * handshake and takes it back, debounced, whenever it changes (see
 * pageState.ts). Nothing is sent before the handshake's state is applied, so
 * the defaults the page paints while it waits never overwrite the address the
 * operator reloaded.
 */
const activeTab = ref<TabId>("overview");
const recordId = ref("");
/**
 * The layer a record page was opened from, for its back link. Empty when the
 * page was the landing (a shared link): back then goes to the layer that
 * lists the record.
 */
const recordFrom = ref<string>("");

/** The toolbar state the visible layer filters on, and what it reports back. */
const chrome = createLensChrome();

const shellState = computed<ShellState>(() => ({
  view: activeTab.value,
  record: recordId.value,
  from: recordFrom.value,
  open: chrome.openId.value,
  q: chrome.search.value,
  sort: chrome.sort.value,
  published: chrome.facets.published,
  origin: chrome.facets.origin,
  type: chrome.facets.type,
  link: chrome.facets.link,
}));

function applyState(state: ShellState): void {
  activeTab.value = state.view;
  recordId.value = state.record;
  recordFrom.value = state.from;
  chrome.openId.value = state.open;
  chrome.search.value = state.q;
  chrome.sort.value = state.sort;
  Object.assign(chrome.facets, { published: state.published, origin: state.origin, type: state.type, link: state.link });
}

const stateSender = createStateSender((state) => host.sendState(state));
const stateApplied = ref(false);
watch(host.init, (value) => {
  if (!value || stateApplied.value) return;
  stateSender.seed(host.pageState.value);
  applyState(decodeShellState(host.pageState.value));
  stateApplied.value = true;
}, { immediate: true });
watch(shellState, (state) => {
  if (stateApplied.value) stateSender.push(encodeShellState(state));
});
onBeforeUnmount(() => stateSender.dispose());

chrome.openLens = (tab, facets?: Partial<Facets>) => {
  recordId.value = "";
  activeTab.value = tab;
  if (facets) {
    chrome.facets.published = facets.published ?? "";
    chrome.facets.origin = facets.origin ?? "";
    chrome.facets.type = facets.type ?? "";
    chrome.facets.link = facets.link ?? "";
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
      props: { id: recordId.value, from: recordFrom.value, onBack: backFromRecord, onEdit: editRecord, onDeleted: deletedFromPage },
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

/** The capabilities the palette reasons with: the same answer the rows and panels gate on. */
const caps = computed<ActionCapabilities>(() => actionCapabilities(host));

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
const NO_ORIGIN = "This frame cannot ask the console to navigate; open Platform → Publishing yourself.";

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

/**
 * A record deleted from its own page or from the side panel. The page goes to
 * the table that listed the record; the panel closes over the layer it was
 * opened from. Either way the outcome is said on that layer, since the
 * surface that ran the delete is gone.
 */
const flash = ref<{ text: string; view: TabId } | null>(null);
watch(activeTab, (tab) => {
  if (flash.value && flash.value.view !== tab) flash.value = null;
});
function deletedFromPage(kind: string, text: string): void {
  const view = viewOfKind(kind);
  recordId.value = "";
  activeTab.value = view;
  flash.value = text ? { text, view } : null;
}
function deletedFromPanel(_kind: string, text: string): void {
  chrome.openId.value = "";
  flash.value = text ? { text, view: activeTab.value } : null;
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
      class="ss-header"
      title="Sub-Store"
      description="Build subscriptions from sources, render them for each client, and publish them from Lattice itself."
    >
      <template #icon><Store :size="19" aria-hidden="true" /></template>
      <template #actions>
        <!-- Search is page-wide (every layer's records), so it sits with
             Refresh in the header rather than in one layer's toolbar. On a
             phone both stay on the title line instead of a row each. -->
        <PcIconButton
          class="tab-search"
          label="Search records and actions (Cmd+K)"
          bordered
          :disabled="standalone"
          @click="openPalette()"
        >
          <Search :size="15" aria-hidden="true" />
        </PcIconButton>
        <!-- Labelled, as vpn-core's is: an icon alone did not say what it reads again. -->
        <PcButton
          class="header-refresh"
          :busy="refreshing"
          :disabled="!host.init.value"
          title="Read the record catalogue and the share list again"
          @click="refresh()"
        >
          <template #icon><RefreshCw :size="15" aria-hidden="true" /></template>
          Refresh
        </PcButton>
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
      <PcToolbar v-if="!recordId" class="ss-layer-bar" label="Sub-Store layers">
        <template #tabs>
          <PcLensTabs v-model="activeTab" class="ss-layer-tabs" label="Sub-Store layers">
            <PcLensTab
              v-for="tab in tabs"
              :key="tab.id"
              :value="tab.id"
              :label="tab.label"
              :count="tabCounts[tab.id]"
            />
          </PcLensTabs>
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
            :title="shareOrigin ? 'Shares are created in the console under Platform → Publishing.' : NO_ORIGIN"
            @click="openShares()"
          >
            <template #icon><SquareArrowOutUpRight :size="15" aria-hidden="true" /></template>
            Open in Publishing
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
        <PcNotice
          v-if="flash && !recordId && flash.view === activeTab"
          class="shell-flash"
          tone="success"
          dismissible
          @dismiss="flash = null"
        >
          {{ flash.text }}
        </PcNotice>
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
        @deleted="deletedFromPanel"
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
