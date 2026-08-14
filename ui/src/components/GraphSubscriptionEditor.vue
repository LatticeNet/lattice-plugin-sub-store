<script setup lang="ts">
import { computed } from "vue";
import { RefreshCw } from "@lucide/vue";

import type { GraphOptionsResponse } from "../client";
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
      <span class="field-label">Converged graph selection</span>
      <button type="button" class="button button-secondary" :disabled="readOnly || loading" @click="emit('reload')">
        <RefreshCw :size="14" :class="{ spin: loading }" aria-hidden="true" /> Reload
      </button>
    </div>
    <p class="field-optional">Options version <code>{{ draft.optionsVersion || "not loaded" }}</code>. A changed projection must be reviewed before saving.</p>

    <label class="field">
      <span class="field-label">VPN identity</span>
      <select class="select" :value="draft.vpnIdentity" :disabled="readOnly || !eligibleIdentities.length" @change="changeIdentity">
        <option value="">Choose an eligible identity</option>
        <option v-for="identity in eligibleIdentities" :key="identity.id" :value="identity.id">
          {{ identity.label }} — {{ identity.status }}
        </option>
      </select>
    </label>

    <div class="field">
      <span class="field-label">Ordered entry roots</span>
      <p v-if="draft.entryRoots.length === 0" class="field-optional" role="status">No roots selected. Add at least one eligible source.</p>
      <ol v-else class="graph-root-order" aria-label="Selected graph roots">
        <li v-for="(root, index) in draft.entryRoots" :key="root">
          <span><strong>{{ graphRoot(root)?.label ?? root }}</strong><small>{{ graphRoot(root)?.path_summary }}</small></span>
          <span class="graph-root-actions">
            <button type="button" :disabled="readOnly || index === 0" :aria-label="`Move ${graphRoot(root)?.label ?? root} up`" @click="emit('move', index, -1)">Up</button>
            <button type="button" :disabled="readOnly || index === draft.entryRoots.length - 1" :aria-label="`Move ${graphRoot(root)?.label ?? root} down`" @click="emit('move', index, 1)">Down</button>
            <button type="button" :disabled="readOnly" :aria-label="`Remove ${graphRoot(root)?.label ?? root}`" @click="emit('remove', index)">Remove</button>
          </span>
        </li>
      </ol>
      <div class="graph-root-candidates" aria-label="Eligible graph roots">
        <button v-for="root in eligibleRoots" :key="root.line_uuid" type="button" :disabled="readOnly || draft.entryRoots.includes(root.line_uuid)" @click="emit('add', root.line_uuid)">
          <strong>{{ root.label }}</strong>
          <span>Source {{ root.source_node_id }} · Target {{ root.target_label || "terminal" }}</span>
          <span>Status {{ root.status }} · Path {{ root.path_summary }}</span>
        </button>
      </div>
      <details v-if="unavailableRoots.length">
        <summary>Unavailable roots ({{ unavailableRoots.length }})</summary>
        <ul>
          <li v-for="root in unavailableRoots" :key="root.line_uuid"><strong>{{ root.label }}</strong> — Source {{ root.source_node_id || "unknown" }} · Target {{ root.target_label || "unresolved" }} · Status {{ root.status }} · Path {{ root.path_summary }} · Reason {{ root.reason || "not eligible for the selected identity" }}</li>
        </ul>
      </details>
    </div>
  </div>
</template>
