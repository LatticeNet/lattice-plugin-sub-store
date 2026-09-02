<script setup lang="ts">
import { computed, onActivated, onMounted, ref, watch } from "vue";
import { CircleAlert, Copy, SquareArrowOutUpRight } from "@lucide/vue";

import LtButton from "../components/lt/LtButton.vue";
import LtEmptyState from "../components/lt/LtEmptyState.vue";
import LtSkeleton from "../components/lt/LtSkeleton.vue";
import { KIND_COLLECTION, KIND_FILE, KIND_SUB, type SubStoreShareRow, type SubscriptionListItem } from "../client";
import { useHost } from "../host";
import { SHARES_LIST_ROUTE, hostOriginFromHash, postNavigate } from "../navigate";
import { formatRelativeTime } from "../rowStatus";
import { shareStateOf } from "../shareState";
import { useShares } from "../useShares";
import { useSubscriptions } from "../useSubscriptions";

/**
 * The record list from the client's side: every share the host serves, the
 * record behind it, its format, expiry and whether a client fetching it gets
 * anything. Shares are created and changed in the console under Networking;
 * this lens reads them and points there.
 */
const host = useHost();
const store = useShares(host);
const subs = useSubscriptions(host);

interface ShareLine {
  share: SubStoreShareRow;
  record: SubscriptionListItem | undefined;
  state: ReturnType<typeof shareStateOf>;
}

const now = ref(Date.now());

const lines = computed<ShareLine[]>(() =>
  (store.shares.value ?? [])
    .map((share) => ({
      share,
      record: subs.items.value.find((item) => item.id === share.subscription_id),
      state: shareStateOf(share, now.value),
    }))
    .sort((a, b) => recordName(a).localeCompare(recordName(b)) || a.share.slug.localeCompare(b.share.slug)),
);

function recordName(line: ShareLine): string {
  return line.record ? line.record.display_name || line.record.name : line.share.subscription_id;
}

function kindOf(line: ShareLine): string {
  const kind = line.record?.kind || KIND_SUB;
  if (kind === KIND_COLLECTION) return "combination";
  if (kind === KIND_FILE) return "file";
  return "subscription";
}

/** The share path with its token masked: the slug identifies it, the token
 *  is what a client presents, and a list is not the place to print one. */
function maskedPath(share: SubStoreShareRow): string {
  const marker = `/${share.slug}/`;
  const at = share.path.indexOf(marker);
  return at >= 0 ? `${share.path.slice(0, at + marker.length)}…` : share.path;
}

function expiryOf(share: SubStoreShareRow): { label: string; title: string } {
  if (!share.expires_at) return { label: "never", title: "This share has no expiry." };
  const at = Date.parse(share.expires_at);
  if (!Number.isFinite(at)) return { label: share.expires_at, title: "The expiry could not be read as a date." };
  const iso = new Date(at).toISOString().slice(0, 10);
  if (at <= now.value) return { label: `expired ${formatRelativeTime(share.expires_at, now.value)}`, title: `Expired ${iso}.` };
  const days = Math.ceil((at - now.value) / 86_400_000);
  return { label: days <= 1 ? "within a day" : `in ${days} days`, title: `Expires ${iso}.` };
}

/** How many lines say what, for the heading. */
const summary = computed(() => {
  const all = lines.value;
  const live = all.filter((line) => line.state.tone === "ok").length;
  return { total: all.length, live, dead: all.length - live };
});

const listed = computed(() =>
  !host.init.value || store.loading.value && store.shares.value === undefined ? null : lines.value.length,
);

// ── copying and navigating ───────────────────────────────────────────────────

const copiedId = ref("");
let copiedTimer: ReturnType<typeof setTimeout> | undefined;
async function copyLink(line: ShareLine): Promise<void> {
  const link = line.share.url || line.share.path;
  try {
    await navigator.clipboard.writeText(link);
    copiedId.value = line.share.share_id;
    if (copiedTimer !== undefined) clearTimeout(copiedTimer);
    copiedTimer = setTimeout(() => {
      copiedId.value = "";
    }, 1500);
  } catch {
    notice.value = "The clipboard refused the link. Copy it from the console instead.";
  }
}

const notice = ref("");
const origin = computed(() => hostOriginFromHash(window.location.hash));

/**
 * Shares are edited in the console, not here. The console's view takes a
 * record name only for its create form (which the record list offers); an
 * existing share is found in the list itself, so every button here opens
 * the list.
 */
function openInNetworking(): void {
  if (!origin.value) return;
  postNavigate(window, SHARES_LIST_ROUTE, origin.value);
  notice.value = "Asked the console to open Networking → Subscription Shares.";
}

// ── loading ──────────────────────────────────────────────────────────────────

async function loadAll(): Promise<void> {
  now.value = Date.now();
  await Promise.all([store.load(), subs.state.value === "idle" ? subs.load() : Promise.resolve()]);
}

onMounted(() => {
  if (host.init.value) void loadAll();
});
onActivated(() => {
  if (host.init.value) void loadAll();
});
watch(host.init, (value) => {
  if (value) void loadAll();
});
</script>

<template>
  <section class="configuration" aria-labelledby="shares-title">
    <div class="section-heading">
      <div>
        <h2 id="shares-title">Shares</h2>
        <p v-if="listed === null">Every link a client can subscribe to, read from the console.</p>
        <p v-else-if="!listed">No share exists yet, so no client can fetch any record here.</p>
        <p v-else>
          {{ summary.live }} of {{ summary.total }} share{{ summary.total === 1 ? "" : "s" }} {{ summary.live === 1 ? "is" : "are" }} live<template v-if="summary.dead">; {{ summary.dead }} {{ summary.dead === 1 ? "is" : "are" }} disabled or expired and {{ summary.dead === 1 ? "returns" : "return" }} nothing</template>.
        </p>
      </div>
      <div class="heading-actions">
        <span class="badge mono" :title="listed === null ? 'Counting.' : `${listed} share${listed === 1 ? '' : 's'} on this deployment.`">{{ listed ?? "—" }}</span>
        <LtButton
          variant="primary"
          :disabled="!origin"
          :title="origin ? 'Shares are created in the console under Networking.' : 'This frame cannot ask the console to navigate; open Networking → Subscription Shares yourself.'"
          @click="openInNetworking()"
        >
          <SquareArrowOutUpRight :size="14" aria-hidden="true" /> Open in Networking
        </LtButton>
      </div>
    </div>

    <div v-if="store.error.value" class="alert" role="alert">
      <CircleAlert :size="16" aria-hidden="true" /> {{ store.error.value }}
    </div>
    <div v-else-if="notice" class="alert alert-ok" role="status">{{ notice }}</div>

    <LtSkeleton v-if="listed === null && !store.error.value" :rows="4" :columns="5" />

    <LtEmptyState
      v-else-if="!store.available.value"
      kind="error"
      title="This session cannot read the share list"
      detail="The installed bundle does not declare shares.list, or your token lacks the scope."
    />

    <LtEmptyState
      v-else-if="store.error.value"
      kind="error"
      title="The share list could not be read"
      :detail="store.error.value"
    >
      <LtButton variant="primary" @click="loadAll()">Retry</LtButton>
    </LtEmptyState>

    <LtEmptyState
      v-else-if="!lines.length"
      title="Nothing is shared"
      detail="Publish a share for a record in the console under Networking, then Subscription Shares, and it appears here with its link."
    />

    <!-- A real table: the columns are values on every row. It keeps its
         columns at any width and scrolls sideways inside itself, with the
         record column pinned. -->
    <div v-else class="shares-scroll">
      <table class="shares-table">
        <thead>
          <tr>
            <th scope="col">Record</th>
            <th scope="col">Share</th>
            <th scope="col">Format</th>
            <th scope="col">Expires</th>
            <th scope="col">State</th>
            <th scope="col"><span class="visually-hidden">Actions</span></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="line in lines" :key="line.share.share_id" :class="`is-${line.state.tone}`">
            <th scope="row" class="shares-record">
              <span class="shares-record-name" :title="line.share.subscription_id">{{ recordName(line) }}</span>
              <span class="shares-record-kind">{{ line.record ? kindOf(line) : "record not in this store" }}</span>
            </th>
            <td class="mono shares-path" :title="`Slug ${line.share.slug}. The token is not shown; Copy link copies the whole link.`">{{ maskedPath(line.share) }}</td>
            <td class="mono">{{ line.share.default_format || "as the client asks" }}</td>
            <td class="mono" :title="expiryOf(line.share).title">{{ expiryOf(line.share).label }}</td>
            <td>
              <span :class="`shares-state is-${line.state.tone}`" :title="line.state.title">{{ line.state.label }}</span>
            </td>
            <td class="shares-actions">
              <button
                type="button"
                class="button button-secondary button-compact"
                :disabled="!line.share.url && !line.share.path"
                :title="line.share.url ? 'Copy the link a client subscribes to' : 'The server did not report its public base; the path alone is copied'"
                @click="copyLink(line)"
              >
                <Copy :size="13" aria-hidden="true" /> {{ copiedId === line.share.share_id ? "Copied" : "Copy link" }}
              </button>
              <button
                type="button"
                class="button button-secondary button-compact"
                :disabled="!origin"
                title="Opens Networking → Subscription Shares in the console, where this share is changed"
                @click="openInNetworking()"
              >
                <SquareArrowOutUpRight :size="13" aria-hidden="true" /> Open in Networking
              </button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </section>
</template>
