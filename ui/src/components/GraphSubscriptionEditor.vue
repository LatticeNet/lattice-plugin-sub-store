<script setup lang="ts">
import { computed } from "vue";
import { RefreshCw } from "@lucide/vue";

import type { GraphOptionsResponse } from "../client";
import { t } from "../i18n";
import type { SubscriptionDraft } from "../useSubscriptions";

const props = defineProps<{
  draft: SubscriptionDraft;
  options: GraphOptionsResponse | null;
  loading: boolean;
  readOnly: boolean;
}>();

const emit = defineEmits<{
  reload: [];
  identity: [value: string];
  add: [root: string];
  remove: [index: number];
  move: [index: number, offset: number];
}>();

const eligibleIdentities = computed(() => props.options?.identities.filter((item) => item.selectable) ?? []);
const eligibleRoots = computed(() => props.options?.roots.filter((item) => item.selectable && item.eligible_identity_ids.includes(props.draft.vpnIdentity)) ?? []);
const unavailableRoots = computed(() => props.options?.roots.filter((item) => !item.selectable || !item.eligible_identity_ids.includes(props.draft.vpnIdentity)) ?? []);

function graphRoot(root: string) {
  return props.options?.roots.find((item) => item.line_uuid === root);
}

function changeIdentity(event: Event): void {
  emit("identity", (event.target as HTMLSelectElement).value);
}
</script>

<template>
  <div class="field field-wide graph-options">
    <div class="field-label-row">
      <span class="field-label">{{ t.graph.selection }}</span>
      <button type="button" class="button button-secondary" :disabled="readOnly || loading" @click="emit('reload')">
        <RefreshCw :size="14" :class="{ spin: loading }" aria-hidden="true" /> {{ t.graph.reload }}
      </button>
    </div>
    <p class="field-optional">{{ t.graph.versionBefore }} <code>{{ draft.optionsVersion || t.graph.notLoaded }}</code>{{ t.graph.versionAfter }}</p>

    <label class="field">
      <span class="field-label">{{ t.graph.identity }}</span>
      <select class="select" :value="draft.vpnIdentity" :disabled="readOnly || !eligibleIdentities.length" @change="changeIdentity">
        <option value="">{{ t.graph.chooseIdentity }}</option>
        <option v-for="identity in eligibleIdentities" :key="identity.id" :value="identity.id">
          {{ t.graph.identityOption(identity.label, identity.status) }}
        </option>
      </select>
    </label>

    <div class="field">
      <span class="field-label">{{ t.graph.roots }}</span>
      <p v-if="draft.entryRoots.length === 0" class="field-optional" role="status">{{ t.graph.noRoots }}</p>
      <ol v-else class="graph-root-order" :aria-label="t.graph.selectedRoots">
        <li v-for="(root, index) in draft.entryRoots" :key="root">
          <span>
            <strong :title="graphRoot(root)?.label ?? root">{{ graphRoot(root)?.label ?? root }}</strong>
            <small>{{ graphRoot(root)?.path_summary }}</small>
          </span>
          <span class="graph-root-actions">
            <button class="button button-secondary button-compact" type="button" :disabled="readOnly || index === 0" :aria-label="t.graph.moveUp(graphRoot(root)?.label ?? root)" @click="emit('move', index, -1)">{{ t.graph.up }}</button>
            <button class="button button-secondary button-compact" type="button" :disabled="readOnly || index === draft.entryRoots.length - 1" :aria-label="t.graph.moveDown(graphRoot(root)?.label ?? root)" @click="emit('move', index, 1)">{{ t.graph.down }}</button>
            <button class="button button-danger button-compact" type="button" :disabled="readOnly" :aria-label="t.graph.remove(graphRoot(root)?.label ?? root)" @click="emit('remove', index)">{{ t.graph.removeShort }}</button>
          </span>
        </li>
      </ol>
      <div class="graph-root-candidates" role="group" :aria-label="t.graph.eligibleRoots">
        <button v-for="root in eligibleRoots" :key="root.line_uuid" type="button" :disabled="readOnly || draft.entryRoots.includes(root.line_uuid)" @click="emit('add', root.line_uuid)">
          <strong>{{ root.label }}</strong>
          <span>{{ t.graph.rootSource(root.source_node_id, root.target_label || t.graph.terminal) }}</span>
          <span>{{ t.graph.rootStatus(root.status, root.path_summary) }}</span>
        </button>
      </div>
      <details v-if="unavailableRoots.length" class="graph-unavailable">
        <summary>{{ t.graph.unavailable(unavailableRoots.length) }}</summary>
        <ul>
          <li v-for="root in unavailableRoots" :key="root.line_uuid">
            <strong>{{ root.label }}</strong>{{
              t.graph.unavailableLine(
                root.source_node_id || t.graph.unknown,
                root.target_label || t.graph.unresolved,
                root.status,
                root.path_summary,
                root.reason || t.graph.notEligible,
              )
            }}
          </li>
        </ul>
      </details>
    </div>
  </div>
</template>
