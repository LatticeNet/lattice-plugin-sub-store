<script setup lang="ts">
import { computed, onActivated, onMounted, ref, watch } from "vue";
import { Copy, SquareArrowOutUpRight } from "@lucide/vue";
import {
  PcButton,
  PcCount,
  PcEmptyState,
  PcKindChip,
  PcNotice,
  PcPanel,
  PcRow,
  PcSearchField,
  PcSkeleton,
  PcStatePill,
  PcTable,
  PcTh,
  useOverlayEscape,
} from "@latticenet/plugin-bridge/chassis";

import LtManualCopy from "../components/lt/LtManualCopy.vue";
import { KIND_COLLECTION, KIND_FILE, KIND_SUB, type SubStoreShareRow, type SubscriptionListItem } from "../client";
import { copyText } from "../hostClipboard";
import { useHost } from "../host";
import { compareText, formatDate, t } from "../i18n";
import { useLensChrome } from "../lensChrome";
import { SHARES_LIST_ROUTE, hostOriginFromHash, postNavigate } from "../navigate";
import { normalizeQuery } from "../recordSearch";
import { formatExpiry } from "../pipeline";
import { shareLinkOf, shareStateOf, stateTone } from "../shareState";
import { useShares } from "../useShares";
import { useSubscriptions } from "../useSubscriptions";

/**
 * The record list from the client's side: every share the host serves, the
 * record behind it, its format, expiry and whether a client fetching it gets
 * anything. Shares are created and changed in the console under Platform → Publishing;
 * this lens reads them and points there.
 */
const host = useHost();
const store = useShares(host);
const subs = useSubscriptions(host);
const chrome = useLensChrome();
const search = chrome.search;
// The side panel opens over this layer, and nothing else here answers Escape.
useOverlayEscape();

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
      state: shareStateOf(share, now.value, subs.state.value !== "ready" || subs.items.value.some((item) => item.id === share.subscription_id)),
    }))
    .sort((a, b) => compareText(recordName(a), recordName(b)) || compareText(a.share.slug, b.share.slug)),
);

const searchedLines = computed(() => {
  const query = normalizeQuery(search.value);
  if (!query) return allLines.value;
  return allLines.value.filter((line) =>
    [recordName(line), line.share.subscription_id, line.share.slug, line.share.default_format ?? "", line.state.label]
      .some((value) => value.toLowerCase().includes(query)),
  );
});

type ShareKindFilter = "all" | "live" | "dead";
/** The shell keeps it (as `link`), so a reload or a pasted link lands filtered. */
const kindFilter = computed<ShareKindFilter>({
  get: () => (chrome.facets.link === "live" || chrome.facets.link === "dead" ? chrome.facets.link : "all"),
  set: (value) => {
    chrome.facets.link = value === "all" ? "" : value;
  },
});

function isLive(line: ShareLine): boolean {
  return line.state.tone === "ok";
}

function setKindFilter(id: string): void {
  if (id === "all" || id === "live" || id === "dead") kindFilter.value = id;
}

const kindCounts = computed(() => {
  const rows = searchedLines.value;
  const live = rows.filter(isLive).length;
  return { all: rows.length, live, dead: rows.length - live };
});

const lines = computed(() => {
  if (kindFilter.value === "all") return searchedLines.value;
  if (kindFilter.value === "live") return searchedLines.value.filter(isLive);
  return searchedLines.value.filter((line) => !isLive(line));
});
const filtersActive = computed(() => !!search.value.trim() || kindFilter.value !== "all");

function clearFilters(): void {
  search.value = "";
  kindFilter.value = "all";
}

/** The row opens the record behind the share, when it is still in the store. */
function openRow(line: ShareLine, event: MouseEvent): void {
  if ((event.target as HTMLElement | null)?.closest("button")) return;
  if (line.record) chrome.openRecord(line.record.id);
}

function recordName(line: ShareLine): string {
  return line.record ? line.record.display_name || line.record.name : line.share.subscription_id;
}

function kindOf(line: ShareLine): string {
  const kind = line.record?.kind || KIND_SUB;
  if (kind === KIND_COLLECTION) return t.shares.kind.combination;
  if (kind === KIND_FILE) return t.shares.kind.file;
  return t.shares.kind.source;
}

/** The share path with its token masked: the slug identifies it, the token
 *  is what a client presents, and a list is not the place to print one. */
function maskedPath(share: SubStoreShareRow): string {
  const marker = `/${share.slug}/`;
  const at = share.path.indexOf(marker);
  return at >= 0 ? `${share.path.slice(0, at + marker.length)}…` : share.path;
}

/*
 * Through the one expiry formatter the attention list, Sources and the side
 * panel use. This column rounded up and abbreviated on its own, so one share
 * read "expires in 3 days" in attention and "in 4 days" here.
 */
function expiryOf(share: SubStoreShareRow): { label: string; title: string } {
  if (!share.expires_at) return { label: t.shares.expiryNever, title: t.shares.expiryNeverTitle };
  const at = Date.parse(share.expires_at);
  if (!Number.isFinite(at)) return { label: share.expires_at, title: t.shares.expiryUnreadable };
  const date = formatDate(at);
  return { label: formatExpiry({ expire: Math.floor(at / 1000) }, now.value), title: at <= now.value ? t.shares.expiredOn(date) : t.shares.expiresOn(date) };
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
  const link = shareLinkOf(line.share);
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
  return line ? shareLinkOf(line.share) : "";
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
  notice.value = t.records.askedPublishing;
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
    <h2 id="shares-title" class="pc-sr-only">{{ t.shares.title }}</h2>

    <PcNotice v-if="store.error.value && store.shares.value !== undefined" tone="warning" :title="t.records.staleTitle">
      {{ t.records.staleBody(store.error.value) }}
    </PcNotice>
    <PcNotice v-else-if="notice" tone="success">{{ notice }}</PcNotice>

    <div v-if="manualCopyId && manualCopyValue" class="manual-copy-strip">
      <div class="manual-copy-strip__head">
        <span class="manual-copy-strip__label">{{ t.shares.linkFor(manualCopyLabel) }}</span>
        <PcButton compact @click="manualCopyId = ''">{{ t.common.dismiss }}</PcButton>
      </div>
      <LtManualCopy :value="manualCopyValue" subject="link" />
    </div>

    <PcPanel v-if="listed === null && !store.error.value" :label="t.shares.loadingPanel">
      <PcSkeleton :count="4" :label="t.shares.loading" />
    </PcPanel>

    <!--
      A permission wall is still a place the operator can leave. The console's
      own share list does not need this frame's scope, so pointing at it is a
      real route to the thing they came for.
    -->
    <PcPanel v-else-if="!store.available.value" :label="t.shares.title">
      <PcEmptyState kind="permission" :title="t.shares.cannotReadTitle">
        <p>
          {{ t.shares.cannotReadBefore }} <span class="pc-mono">shares.list</span>{{ t.shares.cannotReadMiddle }}
          <span class="pc-mono">substore:admin</span> {{ t.shares.cannotReadAnd }} <span class="pc-mono">proxy:admin</span>{{ t.shares.cannotReadAfter }}
        </p>
        <template #actions>
          <PcButton
            variant="primary"
            :disabled="!origin"
            :title="origin ? t.shares.consoleLists : t.shell.noOrigin"
            @click="openInNetworking()"
          >
            <template #icon><SquareArrowOutUpRight :size="15" aria-hidden="true" /></template>
            {{ t.records.openInPublishing }}
          </PcButton>
        </template>
      </PcEmptyState>
    </PcPanel>

    <template v-else-if="store.error.value && store.shares.value === undefined">
      <PcNotice tone="danger" :title="t.shares.loadFailed">
        {{ store.error.value }}
        <template #actions><PcButton compact @click="loadAll()">{{ t.common.tryAgain }}</PcButton></template>
      </PcNotice>
      <PcPanel :label="t.shares.title">
        <PcEmptyState kind="error" :title="t.records.nothingLoaded">
          <p>{{ t.shares.nothingLoadedBody }}</p>
        </PcEmptyState>
      </PcPanel>
    </template>

    <PcPanel v-else :label="t.shares.title">
      <div v-if="!allLines.length">
        <PcEmptyState :title="t.shares.emptyTitle">
          <p>{{ t.shares.emptyBody }}</p>
        </PcEmptyState>
      </div>
      <div v-else class="rec-list">
        <div class="rec-tools">
          <PcSearchField v-model="search" :placeholder="t.shares.filterPlaceholder" :label="t.shares.filterLabel" />
          <label class="toolbar-sort">
            <span>{{ t.shares.stateFilter }}</span>
            <select :value="kindFilter" class="pc-select" :aria-label="t.shares.stateFilterAria" @change="setKindFilter(($event.target as HTMLSelectElement).value)">
              <option value="all">{{ t.records.allCount(kindCounts.all) }}</option>
              <option value="live">{{ t.shares.liveCount(kindCounts.live) }}</option>
              <option value="dead">{{ t.shares.deadCount(kindCounts.dead) }}</option>
            </select>
          </label>
          <PcCount
            :value="summary.total ? t.shares.summary(summary.live, summary.total) : t.shares.summaryNone"
            :label="summary.dead ? t.shares.summaryDead(summary.dead) : t.shares.summaryAllLive"
          />
        </div>

        <PcEmptyState v-if="!lines.length" kind="no-match" :title="t.shares.noMatchTitle">
          <p v-if="search.trim()">{{ t.shares.noMatchQueryBefore }} <span class="pc-mono">{{ search.trim() }}</span>{{ t.shares.noMatchQueryAfter }}</p>
          <p v-else>{{ t.shares.noMatchState }}</p>
          <template #actions>
            <PcButton :disabled="!filtersActive" @click="clearFilters()">{{ t.records.clearFilters }}</PcButton>
          </template>
        </PcEmptyState>

        <PcTable v-else :stacked="false" :min-width="820" :label="t.shares.title" class="layer-table">
          <template #head>
            <PcTh name>{{ t.shares.colPath }}</PcTh>
            <PcTh width="220px">{{ t.shares.colRecord }}</PcTh>
            <PcTh width="150px">{{ t.shares.colFormat }}</PcTh>
            <PcTh width="150px">{{ t.shares.colExpiry }}</PcTh>
            <PcTh width="110px">{{ t.shares.colState }}</PcTh>
            <PcTh actions width="120px" :aria-label="t.records.colActions" />
          </template>
          <tbody>
            <PcRow
              v-for="line in lines"
              :id="`rec-${line.share.share_id}`"
              :key="line.share.share_id"
              class="layer-row"
              :selected="manualCopyId === line.share.share_id || (!!line.record && chrome.openId.value === line.record.id)"
              @click="openRow(line, $event)"
            >
              <td class="pc-name" data-stack="name" :title="t.shares.slugTitle(line.share.slug)">
                <div class="pc-name-line"><strong class="pc-mono">/{{ line.share.slug }}</strong></div>
                <small>{{ maskedPath(line.share) }}</small>
              </td>
              <td data-stack="detail" :data-label="t.shares.colRecord" :title="recordName(line)">
                <span class="pc-td-body layer-record">
                  <span class="layer-record-name">{{ recordName(line) }}</span>
                  <PcKindChip v-if="line.record" :label="kindOf(line)" />
                  <PcKindChip v-else :label="t.shares.notInStore" :title="t.shares.notInStoreTitle" />
                </span>
              </td>
              <td class="pc-mono" data-stack="detail" :data-label="t.shares.colFormat" :title="line.share.default_format || t.shares.asClientAsks"><span class="pc-td-body">{{ line.share.default_format || t.shares.asClientAsks }}</span></td>
              <td data-stack="detail" :data-label="t.shares.colExpiry" :title="expiryOf(line.share).title"><span class="pc-td-body">{{ expiryOf(line.share).label }}</span></td>
              <td data-stack="detail" :data-label="t.shares.colState">
                <span class="pc-td-body"><PcStatePill :tone="stateTone(line.state.tone)" :label="line.state.label" :title="line.state.title" /></span>
              </td>
              <td class="pc-actions" data-stack="actions">
                <div class="pc-row-actions">
                  <PcButton
                    compact
                    :disabled="!line.share.url && !line.share.path"
                    :title="line.share.url ? t.shares.copyTitle : t.shares.copyPathTitle"
                    @click="copyLink(line)"
                  >
                    <template #icon><Copy :size="13" aria-hidden="true" /></template>
                    {{ copiedId === line.share.share_id ? t.shares.copied : t.record.copyLink }}
                  </PcButton>
                </div>
              </td>
            </PcRow>
          </tbody>
        </PcTable>
      </div>
    </PcPanel>
  </section>
</template>
