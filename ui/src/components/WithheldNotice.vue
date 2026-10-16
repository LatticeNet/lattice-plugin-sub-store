<script setup lang="ts">
import { computed } from "vue";
import { ArrowRightLeft } from "@lucide/vue";
import { PcButton, PcNotice } from "@latticenet/plugin-bridge/chassis";

import { BINDINGS, ERROR_CODES } from "../client";
import { useHost } from "../host";
import { t } from "../i18n";
import { useLensChrome } from "../lensChrome";
import { isLegacyRecord } from "../planFlow";
import { recordIntent } from "../recordIntent";
import { useSubscriptions } from "../useSubscriptions";

/**
 * A legacy record's output, withheld (s2-plan sections 1.2 and 11). Its
 * export carries line owner credentials, which the plugin's fetchers read
 * for core's refresh alone, so a preview, a rendered document or a fetch
 * from here answers owner_credentials_withheld. The notice says that as a
 * rule, not as a failure, and offers the way out: "Migrate to fleet" opens
 * the migration flow on the record, on the Records screen, which holds it.
 * The surface the notice sits in hears `migrate` and gives way.
 */
const props = defineProps<{
  /** The legacy record, or the combination or file reading one. */
  recordId: string;
}>();
const emit = defineEmits<{ migrate: [recordId: string] }>();

const host = useHost();
const subs = useSubscriptions(host);
const chrome = useLensChrome();
const intent = recordIntent(host);

/** The legacy record to migrate: the record itself, or the first legacy member a combination or file reads. */
const target = computed(() => {
  const items = subs.items.value;
  const own = items.find((item) => item.id === props.recordId);
  if (!own) return undefined;
  if (isLegacyRecord(own)) return own;
  const seen = new Set<string>();
  const queue = [own];
  while (queue.length) {
    const current = queue.shift()!;
    if (seen.has(current.id)) continue;
    seen.add(current.id);
    const next = [...(current.members ?? []), ...(current.node_source ? [current.node_source] : [])];
    for (const id of next) {
      const found = items.find((item) => item.id === id);
      if (!found) continue;
      if (isLegacyRecord(found)) return found;
      queue.push(found);
    }
  }
  return undefined;
});

const blocked = computed(() => {
  if (!host.available(BINDINGS.subMigrateRecord)) return t.migration.cannotMigrate;
  if (!host.available(BINDINGS.plansPropose)) return t.plans.cannotPropose;
  return "";
});

function migrate(): void {
  const record = target.value;
  if (!record) return;
  emit("migrate", record.id);
  chrome.openId.value = "";
  chrome.openLens("records");
  intent.value = { recordId: record.id, action: "migrate" };
}
</script>

<template>
  <PcNotice tone="info" :title="t.fleetEditor.withheld.title" data-testid="withheld-notice">
    {{ t.fleetEditor.withheld.body }}
    <code class="withheld-code">{{ ERROR_CODES.ownerCredentialsWithheld }}</code>
    <template v-if="target" #actions>
      <PcButton compact :disabled="!!blocked" :title="blocked || t.plans.actions.migrateTitle" data-testid="withheld-migrate" @click="migrate()">
        <template #icon><ArrowRightLeft :size="13" aria-hidden="true" /></template>
        {{ target.id === recordId ? t.plans.actions.migrate : t.migration.migrateMember(target.display_name || target.name) }}
      </PcButton>
    </template>
  </PcNotice>
</template>

<style scoped>
.withheld-code {
  margin-left: var(--space-1);
  font-family: var(--font-mono);
  font-size: var(--lt-text-xs);
}
</style>
