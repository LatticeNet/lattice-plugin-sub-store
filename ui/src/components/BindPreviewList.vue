<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from "vue";
import { ExternalLink } from "@lucide/vue";
import { PcNotice, PcSkeleton } from "@latticenet/plugin-bridge/chassis";

import {
  REASON_NOT_BOUND,
  VPN_CORE_USERS_ROUTE,
  effectiveGeo,
  groupExclusions,
  identityInactive,
  shortVersion,
  type BindEntry,
  type BindExclusion,
} from "../fleet";
import { formatCount, t } from "../i18n";
import { hostOriginFromHash, postNavigate } from "../navigate";
import type { FleetEditor } from "../useRecordEditor";

/**
 * What one identity's share of this record would receive (s2-plan section
 * 11): core's bind.preview, read through the bind service this plugin's
 * manifest declares. The entries it would serve, each with its name,
 * protocol and endpoint in mono; the lines it would leave out, grouped by
 * reason with the fix core supplies; and, when core refuses the plan for the
 * identity, the decoy and why.
 *
 * bind.preview's entries carry no geo, chain or probe, so the exit country
 * beside an entry comes from the catalogue: rows are looked up by line_uuid
 * in batches of at most 256 as entries scroll into view, and cached per
 * catalogue version for the line drawer. No credential is in any of it.
 */
const props = defineProps<{ fleet: FleetEditor }>();
const emit = defineEmits<{ (e: "open-line", lineUUID: string): void }>();
const fleet = props.fleet;

const reply = computed(() => fleet.bindPreview.value);
const status = computed(() => reply.value?.identity_status || fleet.chosenIdentity.value?.status || "");
const inactive = computed(() => identityInactive(status.value));
const groups = computed(() => groupExclusions(reply.value?.excluded ?? []));
const staged = computed(() => fleet.listItem.value?.staged_revision ?? "");

const PAGE = 100;
const shownCount = ref(PAGE);
const entries = computed<BindEntry[]>(() => (reply.value?.entries ?? []).slice(0, shownCount.value));
watch(reply, () => {
  shownCount.value = PAGE;
});

function inactiveText(value: string): string {
  const table = t.fleetEditor.bind.inactive as Record<string, string>;
  return table[value] ?? table.other!;
}

function refusedText(code: string): string {
  const table = t.fleetEditor.bind.refused as Record<string, string>;
  return table[code] ?? table.other!;
}

function reasonText(reason: string): string {
  if (reason.startsWith("plan_rejected:")) return t.fleetEditor.bind.rejected(reason.slice("plan_rejected:".length));
  return (t.fleetEditor.bind.reasons as Record<string, string>)[reason] ?? reason;
}

function entryName(entry: BindEntry | BindExclusion): string {
  return ("label" in entry && entry.label) || entry.name || entry.line_uuid || "";
}

// ── the exit country, looked up as entries come into view ─────────────────────

const rowsTick = ref(0);
function exitOf(lineUUID: string | undefined): string {
  void rowsTick.value;
  if (!lineUUID) return "";
  const row = fleet.rows.get(lineUUID);
  if (!row) return "";
  const geo = effectiveGeo(row);
  return geo?.country ?? t.fleetEditor.unknownGeo;
}

const list = ref<HTMLElement | null>(null);
let observer: IntersectionObserver | null = null;
const pending = new Set<string>();
let timer: ReturnType<typeof setTimeout> | undefined;

function flush(): void {
  timer = undefined;
  const batch = [...pending];
  pending.clear();
  if (!batch.length || !fleet.canReadCatalogue.value) return;
  fleet.rows
    .ensure(batch)
    .then(() => {
      rowsTick.value++;
    })
    .catch(() => {});
}

function observe(): void {
  observer?.disconnect();
  if (typeof IntersectionObserver === "undefined" || !list.value) return;
  observer = new IntersectionObserver((records) => {
    for (const record of records) {
      if (!record.isIntersecting) continue;
      const id = (record.target as HTMLElement).dataset.lineUuid;
      if (id && !fleet.rows.get(id)) pending.add(id);
    }
    if (pending.size && !timer) timer = setTimeout(flush, 120);
  });
  for (const el of list.value.querySelectorAll<HTMLElement>("[data-line-uuid]")) observer.observe(el);
}

watch(entries, () => void nextTick(observe), { flush: "post" });
onBeforeUnmount(() => {
  observer?.disconnect();
  if (timer) clearTimeout(timer);
});

// ── vpn-core's Users view, where a binding is made ────────────────────────────

const origin = computed(() => hostOriginFromHash(typeof window === "undefined" ? "" : window.location.hash));
function openUsers(): void {
  if (origin.value) postNavigate(window, VPN_CORE_USERS_ROUTE, origin.value);
}
</script>

<template>
  <div class="bp" data-testid="bind-preview">
    <label v-if="staged" class="bp-revision">
      <span class="bp-revision-label">{{ t.fleetEditor.identity.revision }}</span>
      <select
        class="select select-compact"
        :value="fleet.bindRevision.value"
        data-testid="bind-revision"
        @change="fleet.chooseRevision(($event.target as HTMLSelectElement).value)"
      >
        <option value="">{{ t.fleetEditor.identity.revisionLive }}</option>
        <option :value="staged">{{ t.fleetEditor.identity.revisionStaged(shortVersion(staged)) }}</option>
      </select>
    </label>

    <PcSkeleton v-if="fleet.bindState.value === 'loading'" variant="rows" :count="3" :label="t.fleetEditor.bind.loading" />
    <PcNotice
      v-else-if="fleet.bindState.value === 'denied' || fleet.bindState.value === 'unavailable'"
      tone="info"
      :title="t.fleetEditor.denied.title"
      data-testid="bind-denied"
    >
      {{ t.fleetEditor.denied.body }}
    </PcNotice>
    <PcNotice v-else-if="fleet.bindState.value === 'error'" tone="danger" data-testid="bind-error">
      {{ t.fleetEditor.bind.error }}
      <span v-if="fleet.bindError.value" class="bp-error">{{ fleet.bindError.value.message }}</span>
      <template #actions>
        <button type="button" class="button button-secondary button-compact" @click="fleet.loadBindPreview()">{{ t.fleetEditor.retry }}</button>
      </template>
    </PcNotice>

    <template v-else-if="reply">
      <PcNotice v-if="inactive" tone="warning" data-testid="bind-inactive">{{ inactiveText(status) }}</PcNotice>
      <PcNotice v-if="reply.refused" tone="danger" :title="t.fleetEditor.bind.refused.title" data-testid="bind-refused">
        {{ refusedText(reply.refused) }}
        <code class="bp-code">{{ reply.refused }}</code>
      </PcNotice>

      <p class="bp-summary mono" data-testid="bind-summary">
        <span>{{ t.fleetEditor.bind.included(reply.entries.length) }}</span>
        <span>{{ t.fleetEditor.bind.excluded(reply.excluded.length) }}</span>
        <span v-if="reply.source_version">{{ shortVersion(reply.source_version) }}</span>
      </p>

      <div v-if="reply.entries.length" ref="list" class="bp-table" role="table" :aria-label="t.fleetEditor.bind.included(reply.entries.length)">
        <div class="bp-row bp-row-head" role="row">
          <span role="columnheader">{{ t.fleetEditor.bind.columns.name }}</span>
          <span role="columnheader">{{ t.fleetEditor.bind.columns.protocol }}</span>
          <span role="columnheader">{{ t.fleetEditor.bind.columns.endpoint }}</span>
        </div>
        <div
          v-for="entry in entries"
          :key="entry.index"
          class="bp-row"
          role="row"
          data-testid="bind-entry"
          :data-line-uuid="entry.line_uuid || undefined"
        >
          <span role="cell" class="bp-name">
            <button
              v-if="entry.line_uuid"
              type="button"
              class="bp-open"
              :aria-label="t.fleetEditor.showLine(entryName(entry))"
              @click="emit('open-line', entry.line_uuid!)"
            >{{ entryName(entry) }}</button>
            <span v-else>{{ entryName(entry) }}</span>
            <span v-if="entry.provider" class="bp-tag">{{ t.fleetEditor.bind.provider }}</span>
            <span v-if="entry.clone" class="bp-tag">{{ t.fleetEditor.bind.clone(entry.clone) }}</span>
            <span v-if="exitOf(entry.line_uuid)" class="bp-exit">{{ exitOf(entry.line_uuid) }}</span>
          </span>
          <span role="cell" class="bp-proto mono">{{ entry.protocol }}</span>
          <span role="cell" class="bp-endpoint mono">{{ entry.server }}<template v-if="entry.port">:{{ entry.port }}</template></span>
        </div>
      </div>
      <p v-else class="bp-none" data-testid="bind-none">{{ t.fleetEditor.bind.noneIncluded }}</p>
      <button
        v-if="reply.entries.length > shownCount"
        type="button"
        class="button button-secondary button-compact bp-more"
        @click="shownCount += PAGE"
      >
        {{ t.fleetEditor.sampleShown(Math.min(reply.entries.length, shownCount + PAGE), reply.entries.length) }}
      </button>

      <section v-for="group in groups" :key="group.reason" class="bp-group" data-testid="bind-exclusion-group">
        <h4 class="bp-group-head">
          <span>{{ reasonText(group.reason) }}</span>
          <span class="bp-group-count">{{ formatCount(group.entries.length) }}</span>
          <code class="bp-code">{{ group.reason }}</code>
        </h4>
        <ul class="bp-excluded">
          <li v-for="entry in group.entries" :key="entry.index" class="bp-ex" data-testid="bind-excluded">
            <span class="bp-ex-name">
              <button
                v-if="entry.line_uuid"
                type="button"
                class="bp-open"
                :aria-label="t.fleetEditor.showLine(entryName(entry))"
                @click="emit('open-line', entry.line_uuid!)"
              >{{ entryName(entry) }}</button>
              <span v-else>{{ entryName(entry) }}</span>
            </span>
            <span v-if="entry.fix" class="bp-fix"><strong>{{ t.fleetEditor.bind.fix }}</strong> {{ entry.fix }}</span>
            <span v-if="entry.detail" class="bp-detail">{{ entry.detail }}</span>
          </li>
        </ul>
        <button
          v-if="group.reason === REASON_NOT_BOUND && origin"
          type="button"
          class="button button-secondary button-compact"
          data-testid="bind-open-users"
          @click="openUsers()"
        >
          <ExternalLink :size="14" aria-hidden="true" /> {{ t.fleetEditor.bind.openUsers }}
        </button>
      </section>
    </template>
  </div>
</template>

<style scoped>
.bp {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  min-width: 0;
}

.bp-revision {
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
  font-size: var(--lt-text-xs);
  color: var(--muted-foreground);
}

.bp-summary {
  display: flex;
  flex-wrap: wrap;
  gap: 0 var(--space-3);
  margin: 0;
  font-size: var(--lt-text-xs);
  color: var(--muted-foreground);
}

.bp-code {
  margin-left: var(--space-1);
  font-family: var(--font-mono);
  font-size: var(--lt-text-xs);
  font-weight: 400;
  color: var(--muted-foreground);
}

.bp-error { display: block; overflow-wrap: anywhere; }

.bp-table {
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  overflow: hidden;
}

.bp-row {
  display: grid;
  grid-template-columns: minmax(0, 2fr) minmax(0, 0.8fr) minmax(0, 1.6fr);
  gap: var(--space-2);
  align-items: center;
  min-height: 30px;
  padding: 2px var(--space-2);
  font-size: var(--lt-text-sm);
}

.bp-row + .bp-row { border-top: 1px solid var(--border); }

.bp-row-head {
  min-height: 26px;
  background: var(--muted);
  color: var(--muted-foreground);
  font-size: var(--lt-text-xs);
  font-weight: 600;
}

.bp-name {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 2px var(--space-1);
  min-width: 0;
}

.bp-open {
  min-width: 0;
  max-width: 100%;
  padding: 0;
  border: 0;
  background: transparent;
  color: var(--foreground);
  font: inherit;
  text-align: left;
  overflow-wrap: anywhere;
  cursor: pointer;
}

.bp-open:hover { text-decoration: underline; }
.bp-open:focus-visible { outline: none; box-shadow: var(--lt-focus-ring-tight); border-radius: var(--radius-sm); }

.bp-tag,
.bp-exit {
  padding: 0 var(--space-1);
  border: 1px solid var(--border);
  border-radius: 999px;
  font-size: var(--lt-text-xs);
  color: var(--muted-foreground);
  white-space: nowrap;
}

.bp-exit { font-family: var(--font-mono); }

.bp-proto,
.bp-endpoint {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: var(--lt-text-xs);
}

.bp-none {
  margin: 0;
  font-size: var(--lt-text-sm);
  color: var(--muted-foreground);
}

.bp-more { align-self: flex-start; }

.bp-group {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  padding-top: var(--space-2);
  border-top: 1px solid var(--border);
}

.bp-group-head {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: var(--space-1) var(--space-2);
  margin: 0;
  font-size: var(--lt-text-sm);
  font-weight: 650;
}

.bp-group-count {
  font-family: var(--font-mono);
  font-size: var(--lt-text-xs);
  color: var(--muted-foreground);
}

.bp-excluded {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  margin: 0;
  padding: 0;
  list-style: none;
}

.bp-ex {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: var(--space-1) var(--space-2);
  border-left: 2px solid var(--border);
  font-size: var(--lt-text-sm);
}

.bp-ex-name { min-width: 0; overflow-wrap: anywhere; }

.bp-fix,
.bp-detail {
  font-size: var(--lt-text-xs);
  line-height: var(--lt-leading);
  color: var(--muted-foreground);
}

.bp-fix strong { color: var(--foreground); font-weight: 600; }

@media (max-width: 480px) {
  .bp-row {
    grid-template-columns: minmax(0, 1fr) auto;
    grid-template-areas: "name proto" "endpoint endpoint";
    row-gap: 0;
  }

  .bp-row-head { display: none; }
  .bp-name { grid-area: name; }
  .bp-proto { grid-area: proto; }
  .bp-endpoint { grid-area: endpoint; color: var(--muted-foreground); }
}
</style>
