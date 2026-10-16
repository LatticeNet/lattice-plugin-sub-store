<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { PcNotice, PcSidePanel, PcSkeleton } from "@latticenet/plugin-bridge/chassis";

import { rowLabel, type CatalogueRow, type Geo } from "../fleet";
import { formatDate, formatDateTime, t } from "../i18n";
import { safeErrorMessage } from "../subStoreModel";
import type { FleetEditor } from "../useRecordEditor";

/**
 * One catalogue line, opened from the selection's sample or from an entry of
 * an identity's preview (s2-plan section 11). bind.preview's entries carry no
 * geo, chain, probe or tags, so the row is looked up by line_uuid through
 * fleet_preview's lookup form (at most 256 per call) and kept per catalogue
 * version: geography with the exit read apart from the node's own, the chain
 * block with an unresolved relay explained, tags and groups, addresses and
 * DDNS names in mono, the machine, and the probe block or "no probe data".
 *
 * The row is credential-free by the catalogue's contract; nothing here can
 * show a credential.
 */
const props = defineProps<{ fleet: FleetEditor; lineUuid: string | null }>();
const emit = defineEmits<{ (e: "close"): void }>();

const row = ref<CatalogueRow | null>(null);
const state = ref<"idle" | "loading" | "ready" | "missing" | "error">("idle");
const error = ref("");

watch(
  () => props.lineUuid,
  async (id) => {
    row.value = null;
    error.value = "";
    if (!id) {
      state.value = "idle";
      return;
    }
    const cached = props.fleet.rows.get(id);
    if (cached) {
      row.value = cached;
      state.value = "ready";
      return;
    }
    state.value = "loading";
    try {
      await props.fleet.rows.ensure([id]);
      if (props.lineUuid !== id) return;
      row.value = props.fleet.rows.get(id) ?? null;
      state.value = row.value ? "ready" : "missing";
    } catch (cause) {
      if (props.lineUuid !== id) return;
      error.value = safeErrorMessage(cause, t.fleetEditor.line.error);
      state.value = "error";
    }
  },
  { immediate: true },
);

const title = computed(() => (row.value ? rowLabel(row.value) : t.fleetEditor.line.title));

function geoRows(geo: Geo | null | undefined): { label: string; value: string }[] {
  if (!geo) return [];
  const line = t.fleetEditor.line;
  return [
    { label: line.country, value: geo.country ?? "" },
    { label: line.region, value: geo.region ?? "" },
    { label: line.city, value: geo.city ?? "" },
    { label: line.asn, value: geo.asn ? `AS${geo.asn}${geo.as_org ? ` ${geo.as_org}` : ""}` : "" },
    { label: line.provider, value: geo.provider ?? "" },
  ].filter((entry) => entry.value);
}

const exitGeo = computed(() => (row.value?.chain?.exit_geo ? row.value.chain.exit_geo : null));
const ownGeo = computed(() => row.value?.geo ?? null);

function when(iso: string | undefined, withTime = false): string {
  if (!iso) return "";
  const at = Date.parse(iso);
  if (Number.isNaN(at)) return iso;
  return withTime ? formatDateTime(at) : formatDate(at);
}

function yesNo(value: boolean | undefined): string {
  return value ? t.fleetEditor.line.yes : t.fleetEditor.line.no;
}
</script>

<template>
  <PcSidePanel
    :open="!!lineUuid"
    :title="title"
    :description="lineUuid ?? ''"
    :close-label="t.fleetEditor.line.close"
    @close="emit('close')"
  >
    <div class="ld" data-testid="line-drawer">
      <PcSkeleton v-if="state === 'loading'" variant="rows" :count="4" :label="t.fleetEditor.line.loading" />
      <PcNotice v-else-if="state === 'error'" tone="danger" data-testid="line-error">
        {{ t.fleetEditor.line.error }} <span class="ld-error">{{ error }}</span>
      </PcNotice>
      <PcNotice v-else-if="state === 'missing'" tone="warning" data-testid="line-missing">{{ t.fleetEditor.line.missing }}</PcNotice>

      <template v-else-if="row">
        <dl class="ld-facts">
          <div class="ld-fact">
            <dt>{{ t.fleetEditor.line.protocol }}</dt>
            <dd class="mono">{{ row.protocol }}<template v-if="row.transport"> / {{ row.transport }}</template><template v-if="row.security"> / {{ row.security }}</template></dd>
          </div>
          <div class="ld-fact">
            <dt>{{ t.fleetEditor.line.service }}</dt>
            <dd>{{ row.service_state || t.fleetEditor.line.none }}<template v-if="row.status"> · {{ row.status }}</template></dd>
          </div>
          <div v-if="row.template" class="ld-fact">
            <dt>{{ t.fleetEditor.line.endpoint }}</dt>
            <dd class="mono">{{ row.template.host }}:{{ row.template.port }}</dd>
          </div>
        </dl>

        <section class="ld-section">
          <h3>{{ t.fleetEditor.line.geo }}</h3>
          <p v-if="row.chain?.unresolved" class="ld-warn" data-testid="line-unresolved">
            <strong>{{ t.fleetEditor.line.unresolved }}.</strong> {{ t.fleetEditor.line.unresolvedNote }}
          </p>
          <dl v-else-if="geoRows(exitGeo ?? ownGeo).length" class="ld-facts">
            <div v-for="entry in geoRows(exitGeo ?? ownGeo)" :key="entry.label" class="ld-fact">
              <dt>{{ entry.label }}</dt>
              <dd>{{ entry.value }}</dd>
            </div>
          </dl>
          <p v-else class="ld-muted">{{ t.fleetEditor.unknownGeo }}</p>
          <template v-if="exitGeo && ownGeo">
            <h4>{{ t.fleetEditor.line.entryGeo }}</h4>
            <dl class="ld-facts">
              <div v-for="entry in geoRows(ownGeo)" :key="entry.label" class="ld-fact">
                <dt>{{ entry.label }}</dt>
                <dd>{{ entry.value }}</dd>
              </div>
            </dl>
          </template>
        </section>

        <section class="ld-section">
          <h3>{{ t.fleetEditor.line.chain }}</h3>
          <dl class="ld-facts">
            <div class="ld-fact">
              <dt>{{ t.fleetEditor.line.role }}</dt>
              <dd class="mono">{{ row.chain?.role }}</dd>
            </div>
            <div class="ld-fact">
              <dt>{{ t.fleetEditor.line.root }}</dt>
              <dd>{{ yesNo(row.chain?.root) }}</dd>
            </div>
            <div v-if="row.chain?.path_state" class="ld-fact">
              <dt>{{ t.fleetEditor.line.pathState }}</dt>
              <dd><span class="mono">{{ row.chain.path_state }}</span> <span class="ld-muted">({{ t.fleetEditor.line.committedOnly }})</span></dd>
            </div>
            <div v-if="row.chain?.path_service_state" class="ld-fact">
              <dt>{{ t.fleetEditor.line.pathService }}</dt>
              <dd class="mono">{{ row.chain.path_service_state }}</dd>
            </div>
            <div v-if="row.chain?.downstream_line_uuid" class="ld-fact">
              <dt>→</dt>
              <dd class="mono ld-wrap">{{ row.chain.downstream_line_uuid }}</dd>
            </div>
          </dl>
        </section>

        <section class="ld-section">
          <h3>{{ t.fleetEditor.line.tags }}</h3>
          <p v-if="row.node_tags?.length" class="ld-chips"><span v-for="tag in row.node_tags" :key="tag" class="ld-chip">{{ tag }}</span></p>
          <p v-else class="ld-muted">{{ t.fleetEditor.line.none }}</p>
          <h3>{{ t.fleetEditor.line.groups }}</h3>
          <p v-if="row.group_ids?.length" class="ld-chips"><span v-for="group in row.group_ids" :key="group" class="ld-chip mono">{{ group }}</span></p>
          <p v-else class="ld-muted">{{ t.fleetEditor.line.none }}</p>
        </section>

        <section class="ld-section">
          <h3>{{ t.fleetEditor.line.addresses }}</h3>
          <ul v-if="row.addresses?.length" class="ld-list mono">
            <li v-for="address in row.addresses" :key="address" class="ld-wrap">{{ address }}</li>
          </ul>
          <p v-else class="ld-muted">{{ t.fleetEditor.line.none }}</p>
          <h3>{{ t.fleetEditor.line.ddns }}</h3>
          <ul v-if="row.ddns_names?.length" class="ld-list">
            <li v-for="name in row.ddns_names" :key="name.name">
              <span class="mono ld-wrap">{{ name.name }}</span>
              <span :class="['ld-flag', name.verified ? 'is-ok' : 'is-warn']">{{ name.verified ? t.fleetEditor.line.verified : t.fleetEditor.line.unverified }}</span>
              <span v-if="name.target" class="ld-muted">{{ t.fleetEditor.line.target(name.target) }}</span>
            </li>
          </ul>
          <p v-else class="ld-muted">{{ t.fleetEditor.line.none }}</p>
        </section>

        <section v-if="row.machine" class="ld-section">
          <h3>{{ t.fleetEditor.line.machine }}</h3>
          <p class="ld-plain">
            {{ [row.machine.vendor, row.machine.region].filter(Boolean).join(" · ") }}
            <template v-if="row.machine.next_renewal"> · {{ t.fleetEditor.line.renews(when(row.machine.next_renewal)) }}</template>
          </p>
        </section>

        <section class="ld-section" data-testid="line-probe">
          <h3>{{ t.fleetEditor.line.probe }}</h3>
          <p v-if="!row.probe" class="ld-muted">{{ t.fleetEditor.line.noProbe }}</p>
          <ul v-else class="ld-list">
            <li>{{ t.fleetEditor.line.probeVerdict(row.probe.verdict, when(row.probe.at, true)) }}<template v-if="row.probe.vantage"> · <span class="mono">{{ row.probe.vantage }}</span></template></li>
            <li>{{ t.fleetEditor.line.probeFailures(row.probe.consecutive_failures) }}</li>
            <li v-if="row.probe.cold_p50_ms">{{ t.fleetEditor.line.probeCold(row.probe.cold_p50_ms) }}</li>
            <li v-if="row.probe.udp_ok !== undefined">{{ t.fleetEditor.line.probeUdp(row.probe.udp_ok) }}</li>
          </ul>
        </section>
      </template>
    </div>
  </PcSidePanel>
</template>

<style scoped>
.ld {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
  min-width: 0;
}

.ld-section {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  min-width: 0;
}

.ld-section h3 {
  margin: var(--space-1) 0 0;
  font-size: var(--lt-text-xs);
  font-weight: 700;
  letter-spacing: 0.06em;
  text-transform: uppercase;
  color: var(--muted-foreground);
}

.ld-section h4 {
  margin: var(--space-1) 0 0;
  font-size: var(--lt-text-xs);
  font-weight: 600;
  color: var(--muted-foreground);
}

.ld-facts {
  display: grid;
  grid-template-columns: minmax(0, 9rem) minmax(0, 1fr);
  gap: 2px var(--space-3);
  margin: 0;
}

.ld-fact { display: contents; }

.ld-fact dt {
  font-size: var(--lt-text-xs);
  color: var(--muted-foreground);
}

.ld-fact dd {
  margin: 0;
  min-width: 0;
  font-size: var(--lt-text-sm);
  overflow-wrap: anywhere;
}

.ld-muted {
  margin: 0;
  font-size: var(--lt-text-sm);
  color: var(--muted-foreground);
}

.ld-plain {
  margin: 0;
  font-size: var(--lt-text-sm);
}

.ld-warn {
  margin: 0;
  padding: var(--space-1) var(--space-2);
  border-left: 2px solid var(--warning);
  background: var(--lt-warn-soft);
  color: var(--warning-text);
  font-size: var(--lt-text-sm);
  line-height: var(--lt-leading);
}

.ld-error { overflow-wrap: anywhere; }

.ld-chips {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-1);
  margin: 0;
}

.ld-chip {
  padding: 0 var(--space-2);
  border: 1px solid var(--border);
  border-radius: 999px;
  font-size: var(--lt-text-xs);
  line-height: 20px;
}

.ld-list {
  display: flex;
  flex-direction: column;
  gap: 2px;
  margin: 0;
  padding: 0;
  list-style: none;
  font-size: var(--lt-text-sm);
}

.ld-list li {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 0 var(--space-2);
}

.ld-wrap { overflow-wrap: anywhere; min-width: 0; }

.ld-flag { font-size: var(--lt-text-xs); }
.ld-flag.is-ok { color: var(--success-text); }
.ld-flag.is-warn { color: var(--warning-text); }
</style>
