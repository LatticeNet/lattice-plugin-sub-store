<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch, type Component } from "vue";
import { FileCode, Layers, Library, Link2, Plus, RefreshCw, Search, Settings, SquareArrowOutUpRight, Store } from "@lucide/vue";
import {
  PcButton,
  PcIconButton,
  PcLensTab,
  PcLensTabs,
  PcNotice,
  PcPageHeader,
  PcProofLine,
  PcSearchField,
  PcSkeleton,
  PcStatCard,
  PcStatStrip,
  PcToolbar,
  PcWorkspace,
  useDocumentQueryState,
} from "@latticenet/plugin-bridge/chassis";

import { useHandshakeTimeout } from "./handshakeTimeout";
import { useHost } from "./host";
import CommandPalette from "./components/CommandPalette.vue";
import { recordCatalogue } from "./useSubscriptions";
import { recordIntent } from "./recordIntent";
import type { ActionCapabilities, ActionId } from "./recordActions";
import type { PaletteCommandId } from "./commandPalette";
import { KIND_COLLECTION, KIND_FILE, KIND_SUB, MAX_SUBSCRIPTION_RECORDS, type SubscriptionListItem } from "./client";
import StandaloneNotice from "./components/StandaloneNotice.vue";
import SubscriptionsScreen from "./screens/SubscriptionsScreen.vue";
import FilesScreen from "./screens/FilesScreen.vue";
import SettingsScreen from "./screens/SettingsScreen.vue";
import SharesScreen from "./screens/SharesScreen.vue";
import { createLensChrome, provideLensChrome, type TabId } from "./lensChrome";
import { SHARES_LIST_ROUTE, hostOriginFromHash, postNavigate } from "./navigate";
import { formatRelativeTime } from "./rowStatus";
import { publishStateFor, shareStateOf } from "./shareState";
import { useShares } from "./useShares";

/**
 * The plugin's page, with no knowledge of how the host is reached.
 *
 * It is the shared plugin chassis, part by part: the page header with the
 * plugin badge and Refresh, the proof line, the stat strip, the toolbar with
 * the lens tabs, the one search field and the page's primary action, and then
 * the lens itself, which draws the table card. Split out of App.vue so the same
 * screens can be mounted against a fake host in `dev/`.
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
 * Sub-Store's own destinations, not invented ones.
 *
 * "Pipelines" and "Convert" used to sit here. Neither is a Sub-Store concept:
 * both were scratchpads that asked the operator to paste raw text and an
 * operator JSON blob and press run. In the real product an operator chain
 * belongs to a subscription (its "operations" section) and conversion is one
 * click on a record, which is where both now live.
 */
const tabs: { id: TabId; label: string; icon: Component; screen: Component }[] = [
  { id: "subscriptions", label: "Subscriptions", icon: Library, screen: SubscriptionsScreen },
  { id: "files", label: "Files", icon: FileCode, screen: FilesScreen },
  // The record list from the client's side: every link the console serves.
  { id: "shares", label: "Shares", icon: Link2, screen: SharesScreen },
  { id: "settings", label: "Settings", icon: Settings, screen: SettingsScreen },
];
const TAB_IDS = new Set<string>(tabs.map((tab) => tab.id));

/**
 * The lens on the document query (`?lens=files`), so a link can carry the
 * lens being discussed. Reading and writing are guarded: the frame runs in an
 * opaque-origin sandbox where a history write may be refused, and a refused
 * write must cost nothing.
 */
const query = useDocumentQueryState();
function lensFromQuery(): TabId {
  try {
    const asked = query.read("lens")[0] ?? "";
    return TAB_IDS.has(asked) ? (asked as TabId) : "subscriptions";
  } catch {
    return "subscriptions";
  }
}
const activeTab = ref<TabId>(lensFromQuery());
watch(activeTab, (tab) => {
  try {
    query.write("lens", tab === "subscriptions" ? [] : [tab]);
  } catch {
    // A sandbox that refuses history writes keeps the lens in memory only.
  }
});
const activeScreen = computed(
  () => tabs.find((tab) => tab.id === activeTab.value)?.screen ?? SubscriptionsScreen,
);

/** The toolbar state the visible lens filters on, and what it reports back. */
const chrome = createLensChrome();
provideLensChrome(chrome);
const search = chrome.search;
const sort = chrome.sort;
const lens = computed(() => chrome.lenses[activeTab.value]);
/** Inside an editor the list controls make no sense; the tabs stay. */
const editing = computed(() => lens.value.editing);

/**
 * One search across the lenses.
 *
 * The shell is the only place that can see every record and can switch tabs,
 * so the palette lives here. It reads the shared catalogue rather than a list
 * of its own, and it hands the chosen action to the owning screen through an
 * intent rather than reaching into that screen's state.
 */
const catalogue = recordCatalogue(host);
const intent = recordIntent(host);
const shareStore = useShares(host);

const ready = computed(() => catalogue.state.value === "ready");
const records = computed(() => (ready.value ? catalogue.items.value.filter((item) => item.kind !== KIND_FILE) : []));
const files = computed(() => (ready.value ? catalogue.items.value.filter((item) => item.kind === KIND_FILE) : []));
const singles = computed(() => records.value.filter((item) => (item.kind || KIND_SUB) === KIND_SUB));

/**
 * The counts on the lenses, from the same two lists the lenses render: the
 * record catalogue and the share store. A lens counting for itself is how the
 * badges came to disagree across tabs. Null until the list has been read, and
 * then the badge stays away rather than claiming zero.
 */
const tabCounts = computed<Record<TabId, number | null>>(() => ({
  subscriptions: ready.value ? records.value.length : null,
  files: ready.value ? files.value.length : null,
  shares: shareStore.shares.value ? shareStore.shares.value.length : null,
  settings: null,
}));

/**
 * The stat strip, from the same two lists. Every tile is a fact about the
 * store as last read; nothing here is a claim about records the shell has not
 * seen, which is why the strip is a skeleton until the catalogue has landed.
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
const budgetLeft = computed(() => Math.max(0, MAX_SUBSCRIPTION_RECORDS - catalogue.items.value.length));
const lastFetch = computed(() => {
  const newest = catalogue.items.value
    .map((item) => item.last_fetch_at ?? "")
    .filter(Boolean)
    .sort()
    .at(-1);
  return newest ? formatRelativeTime(newest) : "";
});

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
  const parts = [`observed at ${observedAt.value || "..."}`, `${records.value.length} records`, `${files.value.length} files`];
  const shares = shareFacts.value;
  if (shares) parts.push(`${shares.live} share${shares.live === 1 ? "" : "s"} live`);
  return parts;
});

/**
 * The shell reads both lists itself when the handshake lands, so the stat
 * strip and the proof line are true whichever lens opened first: a frame
 * opened on Settings would otherwise wait for a catalogue no lens asked for.
 * A lens asking at the same moment joins the same read.
 */
watch(host.init, (value) => {
  if (!value) return;
  void shareStore.load();
  if (catalogue.state.value === "idle") void catalogue.reload();
}, { immediate: true });

/** Read the catalogue and the share list again; the lens on screen follows. */
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
 * The page's one primary action per lens, and why it may be missing.
 *
 * A verb the session may not perform is absent, not disabled, and the reason
 * takes its place as a note; a verb the store cannot take right now (the
 * record budget is spent) stays, disabled, with the reason as its title.
 */
const atRecordLimit = computed(() => ready.value && catalogue.items.value.length >= MAX_SUBSCRIPTION_RECORDS);
const NEEDS_MUTATE = "This session cannot create or delete records here. Either the installed bundle does not declare those methods, or your token lacks the scope.";
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
  if (event.key !== "k" || !(event.metaKey || event.ctrlKey)) return;
  event.preventDefault();
  paletteOpen.value = !paletteOpen.value;
}

onMounted(() => document.addEventListener("keydown", onKeydown));
onBeforeUnmount(() => document.removeEventListener("keydown", onKeydown));

function runFromPalette(record: SubscriptionListItem, action: ActionId): void {
  activeTab.value = record.kind === "file" ? "files" : "subscriptions";
  intent.value = { recordId: record.id, action };
}

function runCommand(command: PaletteCommandId): void {
  activeTab.value = command === "new-file" ? "files" : "subscriptions";
  intent.value = { command };
}

function openShares(): void {
  if (!shareOrigin.value) return;
  postNavigate(window, SHARES_LIST_ROUTE, shareOrigin.value);
}

const searchPlaceholder: Record<TabId, string> = {
  subscriptions: "Filter by name, id, remark, tag",
  files: "Filter by name, id, remark, tag",
  shares: "Filter by record, slug, format",
  settings: "",
};
</script>

<template>
  <PcWorkspace :batch="lens.selected > 0">
    <PcPageHeader
      title="Sub-Store"
      badge="Sub-Store plugin"
      description="Store subscriptions, process them, and publish them from Lattice itself."
    >
      <template #icon><Store :size="19" aria-hidden="true" /></template>
      <template #actions>
        <PcButton
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
        <PcProofLine :segments="proof" :refreshing="refreshing" />
      </template>
    </PcPageHeader>

    <StandaloneNotice v-if="standalone" :detail="host.bootError.value" />

    <template v-else>
      <PcNotice v-if="host.bootError.value" tone="danger" title="The console refused the handshake">
        {{ host.bootError.value }}
      </PcNotice>

      <template v-if="!editing">
        <!-- A skeleton while the catalogue is coming; nothing at all when the
             read failed, since the lens below says so and a strip of tiles
             would have to claim counts the shell never saw. -->
        <PcSkeleton v-if="!ready && catalogue.state.value !== 'error'" variant="strip" :count="5" label="Loading the store summary" />
        <PcStatStrip v-else-if="ready" :count="5" label="Store summary">
          <PcStatCard label="Records" :value="records.length" :note="`${budgetLeft} of ${MAX_SUBSCRIPTION_RECORDS} budget left, shared with files`" />
          <PcStatCard
            label="Published"
            :value="publishedRecords === null ? 'n/a' : `${publishedRecords} of ${records.length}`"
            :note="publishedRecords === null ? (shareStore.available.value ? 'share list not read yet' : 'this session cannot read shares') : publishedRecords === 0 && records.length ? 'no client can fetch any record yet' : 'records a client can fetch'"
            :tone="publishedRecords === null ? 'neutral' : publishedRecords === 0 && records.length ? 'warning' : undefined"
          />
          <PcStatCard label="Files" :value="files.length" note="documents served as written" />
          <PcStatCard
            label="Shares"
            :value="shareFacts ? `${shareFacts.live} live` : 'n/a'"
            :note="shareFacts ? (shareFacts.dead ? `${shareFacts.dead} disabled or expired` : 'none disabled or expired') : 'share list not read'"
            :tone="shareFacts ? undefined : 'neutral'"
          />
          <PcStatCard label="Last fetch" :value="lastFetch || 'n/a'" note="newest provider read" :tone="lastFetch ? undefined : 'neutral'" />
        </PcStatStrip>
      </template>

      <PcToolbar label="Sub-Store lenses">
        <template #tabs>
          <PcLensTabs v-model="activeTab" label="Sub-Store sections">
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
        <template v-if="!editing && activeTab !== 'settings'" #search>
          <PcSearchField v-model="search" :placeholder="searchPlaceholder[activeTab]" :label="`Filter ${activeTab}`" />
        </template>
        <template v-if="!editing && activeTab === 'subscriptions'" #note>
          <label class="toolbar-sort">
            <span>Sort</span>
            <select v-model="sort" class="pc-select" aria-label="Sort records">
              <option value="recent">Recently refreshed</option>
              <option value="name">Name</option>
              <option value="status">Needs attention</option>
            </select>
          </label>
          <span v-if="ready && !canCreate" :title="NEEDS_MUTATE">This session cannot create records here.</span>
        </template>
        <template v-else-if="!editing && activeTab === 'files' && ready && !canCreate" #note>
          <span :title="NEEDS_MUTATE">This session cannot create records here.</span>
        </template>
        <template #secondary>
          <PcButton
            v-if="!editing && activeTab === 'subscriptions' && canCreate"
            :disabled="atRecordLimit || !singles.length"
            :title="!singles.length ? 'Create a subscription first. There is nothing to combine' : atRecordLimit ? LIMIT_REASON : 'Merge several subscriptions and process the result as one'"
            @click="runCommand('new-collection')"
          >
            <template #icon><Layers :size="15" aria-hidden="true" /></template>
            New combination
          </PcButton>
          <!-- Not only a shortcut: a palette reachable only by Cmd+K is one most
               operators never find. Outside the tablist, because a button in
               there announces itself as a tab and joins the arrow-key order. -->
          <PcIconButton class="tab-search" label="Search records and actions (Cmd+K)" bordered @click="openPalette()">
            <Search :size="15" aria-hidden="true" />
          </PcIconButton>
        </template>
        <template v-if="!editing && activeTab === 'subscriptions' && canCreate" #primary>
          <PcButton variant="primary" :disabled="atRecordLimit" :title="atRecordLimit ? LIMIT_REASON : 'One source of nodes, processed and served'" @click="runCommand('new-subscription')">
            <template #icon><Plus :size="15" aria-hidden="true" /></template>
            New subscription
          </PcButton>
        </template>
        <template v-else-if="!editing && activeTab === 'files' && canCreate" #primary>
          <PcButton variant="primary" :disabled="atRecordLimit" :title="atRecordLimit ? LIMIT_REASON : 'A document served as it is, with its proxy list kept in step'" @click="runCommand('new-file')">
            <template #icon><Plus :size="15" aria-hidden="true" /></template>
            New file
          </PcButton>
        </template>
        <template v-else-if="activeTab === 'shares'" #primary>
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
           there was no tabpanel at all. The ids are the ones the lens tabs
           point at. -->
      <div :id="`pc-panel-${activeTab}`" class="lens-panel" role="tabpanel" :aria-labelledby="`pc-tab-${activeTab}`" tabindex="-1">
        <KeepAlive>
          <component :is="activeScreen" />
        </KeepAlive>
      </div>
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
