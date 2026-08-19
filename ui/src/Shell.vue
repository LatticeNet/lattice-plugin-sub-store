<script setup lang="ts">
import { computed, ref } from "vue";
import { CircleAlert, FileCode, Library, Settings, Store } from "@lucide/vue";

import { useHandshakeTimeout } from "./handshakeTimeout";
import { useHost } from "./host";
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
 * so the tabs — each of which would show "Loading…" forever — are replaced by
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
 * click on a record — which is where both now live. Removing them takes two
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

      <!-- The panel attributes live on a real wrapper element.
           Passing them to <component :is> put them on a screen whose root is a
           fragment, where Vue drops them: aria-controls pointed at nothing, and
           there was no tabpanel at all. -->
      <div :id="`panel-${activeTab}`" role="tabpanel" :aria-labelledby="`tab-${activeTab}`" tabindex="-1">
        <KeepAlive>
          <component :is="activeScreen" />
        </KeepAlive>
      </div>
    </template>
  </main>
</template>
