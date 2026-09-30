<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from "vue";

import { KIND_SUB, type SubscriptionListItem } from "../client";
import { useHost } from "../host";
import { actionCapabilities, deletePrompt, rowMenuFor, type ActionId } from "../recordActions";
import { useOverlayRegistration } from "../useOverlayRegistration";
import type { Pipeline } from "../usePipeline";
import { useSubscriptions } from "../useSubscriptions";
import LtConfirmDialog from "./lt/LtConfirmDialog.vue";
import RecordMenu from "./RecordMenu.vue";
import TargetSheet from "./TargetSheet.vue";

/**
 * The row menu for a record shown on its own: in the side panel and on the
 * record page.
 *
 * A record opened from a pasted link (`record=`) had no way to be refreshed,
 * copied, exported or deleted without going back to its table first. This is
 * the table's menu, not a lookalike: the same list and gates (rowMenuFor with
 * actionCapabilities, so a read-only session sees the same disabled items and
 * the same reasons), the same delete dialog and wording (deletePrompt), the
 * same client output sheet. What differs is only where the outcome is said:
 * the surface this sits in shows `status`, and a delete hands the record's
 * kind back so the surface can go to that record's table.
 */
const props = defineProps<{ id: string; pipe: Pipeline }>();
const emit = defineEmits<{
  /** The record is gone. The kind says which table it was listed in. */
  deleted: [kind: string, text: string];
  /** What the last action did, to show beside the record. */
  status: [text: string, tone: "success" | "danger"];
}>();

const host = useHost();
const subs = useSubscriptions(host);
const record = computed(() => props.pipe.item(props.id));
const actions = computed(() => (record.value ? rowMenuFor(record.value, actionCapabilities(host)) : []));

const open = ref(false);
const busy = ref(false);
const menuKey = computed(() => `record-${props.id}`);

function menuSelector(part: "trigger" | "menu"): string {
  const escaped = menuKey.value.replace(/["\\]/g, "\\$&");
  return part === "menu" ? `.rec-menu[data-record-menu="${escaped}"]` : `.rec-menu-wrap[data-record-menu="${escaped}"] button`;
}

function close(refocus = true): void {
  if (!open.value) return;
  open.value = false;
  if (refocus) void nextTick(() => document.querySelector<HTMLElement>(menuSelector("trigger"))?.focus());
}

async function toggle(): Promise<void> {
  if (open.value) return close();
  open.value = true;
  await host.resize();
  await nextTick();
  document.querySelector<HTMLElement>(`${menuSelector("menu")} button:not(:disabled)`)?.focus();
}

// Escape belongs to the overlay stack, like every other overlay here: the
// menu closes first, then the panel under it on the next press.
useOverlayRegistration(open, () => close());

function onDocumentClick(event: MouseEvent): void {
  const target = event.target as HTMLElement | null;
  if (target?.closest(`[data-record-menu="${CSS.escape(menuKey.value)}"]`)) return;
  close(false);
}
watch(open, (value) => {
  if (value) document.addEventListener("click", onDocumentClick, true);
  else document.removeEventListener("click", onDocumentClick, true);
});
onBeforeUnmount(() => document.removeEventListener("click", onDocumentClick, true));

/** Up and down walk the open menu, as they do in a table row's. */
function onKeydown(event: KeyboardEvent): void {
  const step = event.key === "ArrowDown" ? 1 : event.key === "ArrowUp" ? -1 : 0;
  if (!step) return;
  event.preventDefault();
  const items = [...(event.currentTarget as HTMLElement).querySelectorAll<HTMLButtonElement>("button:not(:disabled)")];
  if (!items.length) return;
  const current = items.indexOf(document.activeElement as HTMLButtonElement);
  items[(current + step + items.length) % items.length]?.focus();
}

function report(ok: boolean): void {
  const text = ok ? subs.notice.value : subs.actionError.value;
  if (text) emit("status", text, ok ? "success" : "danger");
}

const sheetFor = ref<SubscriptionListItem | null>(null);
const deleting = ref(false);
const deleteBusy = ref(false);
const prompt = computed(() => deletePrompt(deleting.value ? [props.id] : [], props.pipe.items.value));

async function run(id: ActionId): Promise<void> {
  const item = record.value;
  if (!item) return;
  close(id !== "output" && id !== "delete");
  if (id === "output") {
    sheetFor.value = item;
    return;
  }
  if (id === "delete") {
    deleting.value = true;
    return;
  }
  if (busy.value) return;
  busy.value = true;
  try {
    if (id === "refresh") {
      const ok = await subs.refresh(item.id);
      // The node set may have changed: count it again, as the table does.
      props.pipe.counts.forget(item.id);
      props.pipe.requestCounts([item.id]);
      report(ok);
    } else if (id === "duplicate") {
      report((await subs.duplicate(item.id)) !== null);
    }
  } finally {
    busy.value = false;
  }
}

async function confirmDelete(): Promise<void> {
  const kind = record.value?.kind || KIND_SUB;
  deleteBusy.value = true;
  try {
    const ok = await subs.remove(props.id);
    deleting.value = false;
    if (ok) emit("deleted", kind, subs.notice.value);
    else report(false);
  } finally {
    deleteBusy.value = false;
  }
}
</script>

<template>
  <RecordMenu
    v-if="record"
    :data-record-menu="menuKey"
    :name="record.display_name || record.name"
    :actions="actions"
    :open="open"
    :aria-busy="busy || undefined"
    @toggle="toggle()"
    @run="(id) => run(id)"
    @keydown="onKeydown"
  />
  <LtConfirmDialog
    :open="deleting"
    :title="prompt.title"
    verb="Delete"
    :names="prompt.names"
    :consequences="prompt.consequences"
    :busy="deleteBusy"
    @confirm="confirmDelete()"
    @cancel="deleting = false"
  />
  <TargetSheet :open="!!sheetFor" :record="sheetFor" @close="sheetFor = null" />
</template>
