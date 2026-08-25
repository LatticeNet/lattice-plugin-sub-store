<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from "vue";
import { CircleAlert, FileCode, Library, Search, Settings, Store } from "@lucide/vue";

import { useHandshakeTimeout } from "./handshakeTimeout";
import { useHost } from "./host";
import CommandPalette from "./components/CommandPalette.vue";
import { recordCatalogue } from "./useSubscriptions";
import { recordIntent } from "./recordIntent";
import type { ActionCapabilities, ActionId } from "./recordActions";
import type { PaletteCommandId } from "./commandPalette";
import type { SubscriptionListItem } from "./client";
import StandaloneNotice from "./components/StandaloneNotice.vue";
import SubscriptionsScreen from "./screens/SubscriptionsScreen.vue";
import FilesScreen from "./screens/FilesScreen.vue";
import SettingsScreen from "./screens/SettingsScreen.vue";

/**
 * The plugin's chrome and its tabs, with no knowledge of how the host is
 * reached.
 *
 * Split out of App.vue so the same screens can be mounted against a fake host
 * in `dev/`. Until that existed the UI had only ever been checked by building
 * it and reading the markup, which is how an operator picker that rendered
 * empty and a data load that never ran both reached production.
 */

const host = useHost();

/**
 * The handshake failed or never came. Outside the console that is permanent,
 * so the tabs. Each of which would show "Loading…" forever, are replaced by
 * one honest notice. A handshake that lands late flips this back off.
 */
const handshakeExpired = useHandshakeTimeout(host.init);
const standalone = computed(
  () => !host.init.value && (handshakeExpired.value || !!host.bootError.value),
);

type TabId = "subscriptions" | "files" | "settings";

/**
 * Sub-Store's own destinations, not invented ones.
 *
 * "Pipelines" and "Convert" used to sit here. Neither is a Sub-Store concept:
 * both were scratchpads that asked the operator to paste raw text and an
 * operator JSON blob and press run. In the real product an operator chain
 * belongs to a subscription (its "operations" section) and conversion is one
 * click on a record, which is where both now live. Removing them takes two
 * destinations out of the top level and puts the work where the record is.
 */
const tabs: { id: TabId; label: string; icon: unknown; screen: unknown }[] = [
  { id: "subscriptions", label: "Subscriptions", icon: Library, screen: SubscriptionsScreen },
  { id: "files", label: "Files", icon: FileCode, screen: FilesScreen },
  { id: "settings", label: "Settings", icon: Settings, screen: SettingsScreen },
];

/**
 * Arrow-key movement across the tablist.
 *
 * A tablist that only responds to clicks is a tablist in name only: the role
 * promises arrow keys, and a keyboard operator who lands on it otherwise has to
 * Tab through every panel to reach the next section.
 */
const tabButtons = ref<HTMLElement[]>([]);
function setTabRef(el: unknown, index: number): void {
  if (el instanceof HTMLElement) tabButtons.value[index] = el;
}
function onTabKeydown(event: KeyboardEvent, index: number): void {
  const step = event.key === "ArrowRight" ? 1 : event.key === "ArrowLeft" ? -1 : 0;
  if (!step) return;
  event.preventDefault();
  const next = (index + step + tabs.length) % tabs.length;
  activeTab.value = tabs[next]!.id;
  tabButtons.value[next]?.focus();
}

const activeTab = ref<TabId>("subscriptions");
const activeScreen = computed(
  () => tabs.find((tab) => tab.id === activeTab.value)?.screen ?? SubscriptionsScreen,
);

/**
 * One search across both tabs.
 *
 * The shell is the only place that can see every record and can switch tabs,
 * so the palette lives here. It reads the shared catalogue rather than a list
 * of its own, and it hands the chosen action to the owning screen through an
 * intent rather than reaching into that screen's state.
 */
const catalogue = recordCatalogue(host);
const intent = recordIntent(host);
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

function openPalette(): void {
  paletteOpen.value = true;
}

/**
 * Cmd/Ctrl+K, inside this frame only.
 *
 * The console binds the same key on its own document. A cross-origin sandboxed
 * frame does not propagate key events to its parent, so whichever surface has
 * focus answers, and the two palettes never contend. The visible button beside
 * the tabs is the entry that does not depend on that: a feature reachable only
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
</script>

<template>
  <main class="workspace">
    <header class="page-header">
      <div class="title-mark" aria-hidden="true"><Store :size="19" /></div>
      <div class="title-copy">
        <h1>Sub-Store</h1>
        <p>Store subscriptions, process them, and publish them from Lattice itself.</p>
      </div>
    </header>

    <StandaloneNotice v-if="standalone" :detail="host.bootError.value" />

    <template v-else>
      <div v-if="host.bootError.value" class="alert" role="alert">
        <CircleAlert :size="17" aria-hidden="true" />
        <span>{{ host.bootError.value }}</span>
      </div>

      <div class="tab-row">
      <nav class="tab-bar" role="tablist" aria-label="Sub-Store sections">
        <button
          v-for="(tab, index) in tabs"
          :id="`tab-${tab.id}`"
          :key="tab.id"
          :ref="(el) => setTabRef(el, index)"
          class="tab"
          type="button"
          role="tab"
          :aria-selected="activeTab === tab.id"
          :aria-controls="`panel-${tab.id}`"
          :data-active="activeTab === tab.id"
          :tabindex="activeTab === tab.id ? 0 : -1"
          @keydown="onTabKeydown($event, index)"
          @click="activeTab = tab.id"
        >
          <component :is="tab.icon" :size="15" aria-hidden="true" />
          {{ tab.label }}
        </button>
      </nav>
      <!-- Outside the tablist: a button in there announces itself as a tab and
           joins the arrow-key order. Not only a shortcut either, because a
           palette reachable only by Cmd+K is one most operators never find. -->
      <button class="tab-search" type="button" @click="openPalette()">
        <Search :size="15" aria-hidden="true" />
        Search
        <kbd class="palette-hint">⌘K</kbd>
      </button>
      </div>

      <!-- The panel attributes live on a real wrapper element.
           Passing them to <component :is> put them on a screen whose root is a
           fragment, where Vue drops them: aria-controls pointed at nothing, and
           there was no tabpanel at all. -->
      <div :id="`panel-${activeTab}`" role="tabpanel" :aria-labelledby="`tab-${activeTab}`" tabindex="-1">
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
  </main>
</template>
