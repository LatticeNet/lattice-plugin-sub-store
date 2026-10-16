<script setup lang="ts">
import { computed, ref } from "vue";
import { PcNotice, PcSkeleton } from "@latticenet/plugin-bridge/chassis";

import { IDENTITY_ACTIVE, type Identity } from "../fleet";
import { formatDate, t } from "../i18n";
import type { FleetEditor } from "../useRecordEditor";

/**
 * Which identity's share to preview (s2-plan section 11): vpn-core's identity
 * list, relayed by this plugin's `identities` method, one row per identity
 * with its status and the number of lines it is bound to.
 *
 * The rows are native radios in one group, so the arrow keys move the choice
 * and Tab leaves the list, and a filter appears once the list is long enough
 * to need one. An identity that is not active is still offered: previewing
 * it is how an operator sees that its shares answer the decoy.
 */
const props = defineProps<{ fleet: FleetEditor }>();
const fleet = props.fleet;

const FILTER_FROM = 7;
const filter = ref("");

const shown = computed<Identity[]>(() => {
  const needle = filter.value.trim().toLowerCase();
  const all = [...fleet.identities.value].sort(
    (a, b) => Number(b.status === IDENTITY_ACTIVE) - Number(a.status === IDENTITY_ACTIVE) || nameOf(a).localeCompare(nameOf(b)),
  );
  if (!needle) return all;
  return all.filter((identity) => [identity.name, identity.id, identity.group].some((value) => value?.toLowerCase().includes(needle)));
});

function nameOf(identity: Identity): string {
  return identity.name || identity.id;
}

function statusLabel(status: string): string {
  return (t.fleetEditor.identity.status as Record<string, string>)[status] ?? status;
}

function tone(status: string): string {
  return status === IDENTITY_ACTIVE ? "ok" : status === "expired" || status === "disabled" ? "danger" : "warn";
}

function expiry(identity: Identity): string {
  if (!identity.expires_at) return "";
  const at = Date.parse(identity.expires_at);
  return Number.isNaN(at) ? "" : t.fleetEditor.identity.expires(formatDate(at));
}
</script>

<template>
  <div class="ip" data-testid="identity-picker">
    <PcSkeleton v-if="fleet.identitiesState.value === 'loading'" variant="rows" :count="3" :label="t.fleetEditor.identity.loading" />
    <PcNotice
      v-else-if="fleet.identitiesState.value === 'denied' || fleet.identitiesState.value === 'unavailable'"
      tone="info"
      :title="t.fleetEditor.denied.title"
      data-testid="identity-denied"
    >
      {{ t.fleetEditor.denied.body }}
    </PcNotice>
    <PcNotice v-else-if="fleet.identitiesState.value === 'error'" tone="danger" data-testid="identity-error">
      {{ t.fleetEditor.identity.error }}
      <template #actions>
        <button type="button" class="button button-secondary button-compact" @click="fleet.loadIdentities()">{{ t.fleetEditor.retry }}</button>
      </template>
    </PcNotice>
    <p v-else-if="fleet.identitiesState.value === 'ready' && !fleet.identities.value.length" class="ip-note" data-testid="identity-none">
      {{ t.fleetEditor.identity.none }}
    </p>

    <template v-else-if="fleet.identitiesState.value === 'ready'">
      <input
        v-if="fleet.identities.value.length >= FILTER_FROM"
        v-model="filter"
        type="search"
        class="ip-filter"
        autocomplete="off"
        spellcheck="false"
        :aria-label="t.fleetEditor.identity.filter"
        :placeholder="t.fleetEditor.identity.filter"
      />
      <fieldset class="ip-list">
        <legend class="pc-sr-only">{{ t.fleetEditor.identity.listLabel }}</legend>
        <label
          v-for="identity in shown"
          :key="identity.id"
          :class="['ip-row', { 'is-on': fleet.identityId.value === identity.id }]"
          data-testid="identity-row"
        >
          <input
            type="radio"
            name="fleet-identity"
            class="ip-radio"
            :value="identity.id"
            :checked="fleet.identityId.value === identity.id"
            @change="fleet.chooseIdentity(identity.id)"
          />
          <span class="ip-name">
            <span class="ip-label">{{ nameOf(identity) }}</span>
            <span v-if="identity.name" class="ip-id mono">{{ identity.id }}</span>
          </span>
          <span class="ip-facts">
            <span :class="['ip-status', `is-${tone(identity.status)}`]" data-testid="identity-status">{{ statusLabel(identity.status) }}</span>
            <span class="ip-bound">{{ t.fleetEditor.identity.boundLines(identity.bound_lines) }}</span>
            <span v-if="expiry(identity)" class="ip-expiry">{{ expiry(identity) }}</span>
          </span>
        </label>
        <p v-if="!shown.length" class="ip-note">{{ t.fleetEditor.identity.noMatch }}</p>
      </fieldset>
    </template>
  </div>
</template>

<style scoped>
.ip {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  min-width: 0;
}

.ip-note {
  margin: 0;
  font-size: var(--lt-text-sm);
  color: var(--muted-foreground);
}

.ip-filter {
  min-height: var(--lt-control-h-sm);
  padding: 0 var(--space-2);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  background: var(--card);
  color: var(--foreground);
  font-size: var(--lt-text-sm);
  outline: none;
}

.ip-filter:focus-visible { border-color: var(--primary); box-shadow: var(--lt-focus-ring-tight); }

.ip-list {
  display: flex;
  flex-direction: column;
  max-height: 16rem;
  margin: 0;
  padding: 0;
  overflow: auto;
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
}

.ip-row {
  position: relative;
  display: grid;
  grid-template-columns: auto minmax(0, 1fr);
  grid-template-areas: "radio name" "radio facts";
  gap: 0 var(--space-2);
  align-items: center;
  padding: var(--space-1) var(--space-2);
  cursor: pointer;
}

.ip-row + .ip-row { border-top: 1px solid var(--border); }
.ip-row:hover { background: var(--muted); }
.ip-row.is-on { background: var(--lt-accent-soft); }
.ip-row:has(.ip-radio:focus-visible) { box-shadow: inset var(--lt-focus-ring-tight); }

.ip-radio {
  grid-area: radio;
  margin: 0;
  accent-color: var(--primary);
}

.ip-name {
  grid-area: name;
  display: flex;
  align-items: baseline;
  gap: var(--space-2);
  min-width: 0;
}

.ip-label {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: var(--lt-text-sm);
  font-weight: 600;
}

.ip-id {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: var(--lt-text-xs);
  color: var(--muted-foreground);
}

.ip-facts {
  grid-area: facts;
  display: flex;
  flex-wrap: wrap;
  gap: 0 var(--space-2);
  font-size: var(--lt-text-xs);
  color: var(--muted-foreground);
}

.ip-status.is-ok { color: var(--success-text); }
.ip-status.is-warn { color: var(--warning-text); }
.ip-status.is-danger { color: var(--lt-danger-ink); }
</style>
