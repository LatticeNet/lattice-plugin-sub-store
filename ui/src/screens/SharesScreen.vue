<script setup lang="ts">
import { computed, onActivated, onMounted, ref, watch } from "vue";
import { Copy, SquareArrowOutUpRight } from "@lucide/vue";
import {
  PcActionsCell,
  PcButton,
  PcCount,
  PcEmptyState,
  PcIconButton,
  PcKindChip,
  PcNameCell,
  PcNotice,
  PcPanel,
  PcPanelHeader,
  PcRow,
  PcSkeleton,
  PcStatePill,
  PcTable,
  PcTd,
  PcTh,
} from "@latticenet/plugin-bridge/chassis";

import LtManualCopy from "../components/lt/LtManualCopy.vue";
import { KIND_COLLECTION, KIND_FILE, KIND_SUB, type SubStoreShareRow, type SubscriptionListItem } from "../client";
import { copyText } from "../hostClipboard";
import { useHost } from "../host";
import { useLensChrome } from "../lensChrome";
import { SHARES_LIST_ROUTE, hostOriginFromHash, postNavigate } from "../navigate";
import { normalizeQuery } from "../recordSearch";
import { formatRelativeTime } from "../rowStatus";
import { shareStateOf, stateTone } from "../shareState";
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
const chrome = useLensChrome();
const search = chrome.search;

interface ShareLine {
  share: SubStoreShareRow;
  record: SubscriptionListItem | undefined;
  state: ReturnType<typeof shareStateOf>;
}

const now = ref(Date.now());

const allLines = computed<ShareLine[]>(() =>
  (store.shares.value ?? [])
    .map((share) => ({
      share,
      record: subs.items.value.find((item) => item.id === share.subscription_id),
      state: shareStateOf(share, now.value),
    }))
    .sort((a, b) => recordName(a).localeCompare(recordName(b)) || a.share.slug.localeCompare(b.share.slug)),
);

/** The toolbar's search, matched against the record, the slug and the format. */
const lines = computed(() => {
  const query = normalizeQuery(chrome.search.value);
  if (!query) return allLines.value;
  return allLines.value.filter((line) =>
    [recordName(line), line.share.subscription_id, line.share.slug, line.share.default_format ?? "", line.state.label]
      .some((value) => value.toLowerCase().includes(query)),
  );
});
const filtersActive = computed(() => !!chrome.search.value.trim());

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

/** How many lines say what, for the card's count. */
const summary = computed(() => {
  const all = allLines.value;
  const live = all.filter((line) => line.state.tone === "ok").length;
  return { total: all.length, live, dead: all.length - live };
});

/**
 * How many shares there are, or null when that is not known.
 *
 * A failed read used to fall through to `lines.value.length`, which is zero,
 * and the heading then said "No share exists yet, so no client can fetch any
 * record here" directly above the error alert saying the list could not be
 * read. Absent is not zero: an unanswered question must not be rendered as a
 * confident count.
 */
const listed = computed(() => {
  if (!host.init.value) return null;
  if (store.loading.value && store.shares.value === undefined) return null;
  if (store.error.value && store.shares.value === undefined) return null;
  return allLines.value.length;
});

// ── copying and navigating ───────────────────────────────────────────────────

const copiedId = ref("");
/**
 * The share whose link could not be copied, if any.
 *
 * One at a time, and the reveal sits above the table with the other notices
 * rather than in a row of its own: the table scrolls sideways below about
 * 900px, so at 375 a reveal inside it scrolled out of view.
 */
const manualCopyId = ref("");
let copiedTimer: ReturnType<typeof setTimeout> | undefined;
async function copyLink(line: ShareLine): Promise<void> {
  const link = line.share.url || line.share.path;
  if (!link) return;
  manualCopyId.value = "";
  if (await copyText(link)) {
    copiedId.value = line.share.share_id;
    if (copiedTimer !== undefined) clearTimeout(copiedTimer);
    copiedTimer = setTimeout(() => {
      copiedId.value = "";
    }, 1500);
    return;
  }
  // The link is what the operator came for, so it goes on screen rather than
  // being replaced by a sentence about the clipboard.
  copiedId.value = "";
  manualCopyId.value = line.share.share_id;
  await host.resize();
}

/** The link the reveal is showing, so the strip and the list cannot disagree. */
const manualCopyValue = computed(() => {
  const line = allLines.value.find((candidate) => candidate.share.share_id === manualCopyId.value);
  return line ? line.share.url || line.share.path : "";
});

/** Which share the reveal belongs to, named so the strip is not ambiguous. */
const manualCopyLabel = computed(() => {
  const line = allLines.value.find((candidate) => candidate.share.share_id === manualCopyId.value);
  return line ? `${recordName(line)} · /${line.share.slug}` : "";
});

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
  <section class="lens" aria-labelledby="shares-title">
    <h2 id="shares-title" class="pc-sr-only">Shares</h2>

    <PcNotice v-if="store.error.value && store.shares.value !== undefined" tone="warning" title="Showing the last good read">
      The newest reload failed ({{ store.error.value }}).
    </PcNotice>
    <PcNotice v-else-if="notice" tone="success">{{ notice }}</PcNotice>

    <div v-if="manualCopyId && manualCopyValue" class="manual-copy-strip">
      <div class="manual-copy-strip__head">
        <span class="manual-copy-strip__label">Link for {{ manualCopyLabel }}</span>
        <PcButton compact @click="manualCopyId = ''">Dismiss</PcButton>
      </div>
      <LtManualCopy :value="manualCopyValue" subject="link" />
    </div>

    <PcPanel v-if="listed === null && !store.error.value" label="Loading shares">
      <PcSkeleton :count="4" label="Loading the shares" />
    </PcPanel>

    <!--
      A permission wall is still a place the operator can leave. The console's
      own share list does not need this frame's scope, so pointing at it is a
      real route to the thing they came for.
    -->
    <PcPanel v-else-if="!store.available.value" label="Shares">
      <PcEmptyState kind="permission" title="This session cannot read the share list">
        <p>
          The installed bundle does not declare <span class="pc-mono">shares.list</span>, or your token
          lacks <span class="pc-mono">substore:admin</span> and <span class="pc-mono">proxy:admin</span>.
          Shares themselves still exist; this lens just cannot read them.
        </p>
        <template #actions>
          <PcButton
            variant="primary"
            :disabled="!origin"
            :title="origin ? 'The console lists the same shares under Networking.' : 'This frame cannot ask the console to navigate; open Networking → Subscription Shares yourself.'"
            @click="openInNetworking()"
          >
            <template #icon><SquareArrowOutUpRight :size="15" aria-hidden="true" /></template>
            Open in Networking
          </PcButton>
        </template>
      </PcEmptyState>
    </PcPanel>

    <template v-else-if="store.error.value && store.shares.value === undefined">
      <PcNotice tone="danger" title="The share list could not be read">
        {{ store.error.value }}
        <template #actions><PcButton compact @click="loadAll()">Try again</PcButton></template>
      </PcNotice>
      <PcPanel label="Shares">
        <PcEmptyState kind="error" title="Nothing could be loaded">
          <p>This is not an empty share list, it is an unanswered question.</p>
        </PcEmptyState>
      </PcPanel>
    </template>

    <PcPanel v-else label="Shares">
      <PcPanelHeader
        title="Shares"
        description="Every link a client can subscribe to, read from the console. A share is created and changed there, under Networking."
      >
        <PcCount
          :value="summary.total ? `${summary.live} of ${summary.total} live` : 'none'"
          :label="summary.dead ? `${summary.dead} disabled or expired and returning nothing.` : 'Every share here is live.'"
        />
      </PcPanelHeader>

      <PcEmptyState v-if="!allLines.length" title="Nothing is shared">
        <p>
          Publish a share for a record in the console under Networking, then Subscription Shares,
          and it appears here with its link.
        </p>
      </PcEmptyState>

      <PcEmptyState v-else-if="!lines.length" kind="no-match" title="No share matches that search">
        <p>No record, slug or format here matches <span class="pc-mono">{{ search.trim() }}</span>.</p>
        <template #actions>
          <PcButton :disabled="!filtersActive" @click="search = ''">Clear the search</PcButton>
        </template>
      </PcEmptyState>

      <!-- A real table: the columns are values on every row. -->
      <PcTable v-else :min-width="900" label="Shares">
        <template #head>
          <PcTh name>Record</PcTh>
          <PcTh>Share</PcTh>
          <PcTh>Format</PcTh>
          <PcTh>Expires</PcTh>
          <PcTh>State</PcTh>
          <PcTh actions>Actions</PcTh>
        </template>
        <tbody>
          <PcRow
            v-for="line in lines"
            :key="line.share.share_id"
            :selected="manualCopyId === line.share.share_id"
          >
            <PcNameCell :name="recordName(line)" :id="line.share.subscription_id" :title="line.share.subscription_id">
              <template #after>
                <PcKindChip v-if="line.record" :label="kindOf(line)" />
                <PcKindChip v-else label="not in this store" title="The record behind this share is not in this store any more." />
              </template>
              <template #status>
                <PcStatePill :tone="stateTone(line.state.tone)" :label="line.state.label" :title="line.state.title" />
              </template>
            </PcNameCell>
            <PcTd label="Share" mono :title="`Slug ${line.share.slug}. The token is not shown; Copy link copies the whole link.`">{{ maskedPath(line.share) }}</PcTd>
            <PcTd label="Format" mono>{{ line.share.default_format || "as the client asks" }}</PcTd>
            <PcTd label="Expires" mono :title="expiryOf(line.share).title">{{ expiryOf(line.share).label }}</PcTd>
            <PcTd label="State" stack="state">
              <PcStatePill :tone="stateTone(line.state.tone)" :label="line.state.label" :title="line.state.title" />
            </PcTd>
            <PcActionsCell>
              <PcButton
                compact
                :disabled="!line.share.url && !line.share.path"
                :title="line.share.url ? 'Copy the link a client subscribes to' : 'The server did not report its public base; the path alone is copied'"
                @click="copyLink(line)"
              >
                <template #icon><Copy :size="13" aria-hidden="true" /></template>
                {{ copiedId === line.share.share_id ? "Copied" : "Copy link" }}
              </PcButton>
              <PcIconButton
                label="Open in Networking, where this share is changed"
                bordered
                :disabled="!origin"
                @click="openInNetworking()"
              >
                <SquareArrowOutUpRight :size="15" aria-hidden="true" />
              </PcIconButton>
            </PcActionsCell>
          </PcRow>
        </tbody>
      </PcTable>
    </PcPanel>
  </section>
</template>
