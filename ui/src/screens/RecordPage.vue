<script setup lang="ts">
import { computed, onActivated, ref, watch } from "vue";
import { ArrowLeft, Copy, Eye, EyeOff } from "@lucide/vue";
import {
  PcButton,
  PcEmptyState,
  PcKindChip,
  PcLensTab,
  PcLensTabs,
  PcNotice,
  PcPanel,
  PcPanelBody,
  PcPanelHeader,
  PcProofLine,
  PcSkeleton,
  PcStateDot,
  useOverlayEscape,
} from "@latticenet/plugin-bridge/chassis";

import { BINDINGS, callMethod, KIND_COLLECTION, KIND_FILE, KIND_SUB, type SubscriptionRenderResponse } from "../client";
import DocumentView from "../components/DocumentView.vue";
import LtManualCopy from "../components/lt/LtManualCopy.vue";
import RecordChainDetail from "../components/RecordChainDetail.vue";
import SubscriptionPreviewSummary from "../components/SubscriptionPreviewSummary.vue";
import RecordActions from "../components/RecordActions.vue";
import TargetSheet from "../components/TargetSheet.vue";
import UsageBar from "../components/UsageBar.vue";
import { useHost } from "../host";
import { copyText } from "../hostClipboard";
import { formatDate, formatTime, t } from "../i18n";
import { useLensChrome } from "../lensChrome";
import { SHARES_LIST_ROUTE, hostOriginFromHash, postNavigate, sharesRoute } from "../navigate";
import {
  clientOfFile,
  formatExpiry,
  formatUsage,
  isProviderLink,
  providerFigures,
  sourceKindLabel,
  usedBySentence,
} from "../pipeline";
import { editorLanguageForContentType } from "../previewLanguage";
import { isFlagged } from "../recordTable";
import { formatRelativeTime } from "../rowStatus";
import { refreshStateFor, shareLinkOf, shareStateOf, stateTone } from "../shareState";
import { safeErrorMessage } from "../subStoreModel";
import { maskUrl } from "../urlMask";
import { useReveal } from "../reveal";
import { usePipeline } from "../usePipeline";
import { useRecordChain } from "../useRecordChain";
import { useSubscriptions } from "../useSubscriptions";

/**
 * L2, one record on its own page (`?record=<id>`): who it is, where it sits
 * in the chain, and proof of what it produces. It replaces the layer tabs
 * rather than sitting under them, so the page has one tab row: its own.
 *
 * Nodes is the compare panel (source against result, with what each
 * operation kept). Steps is the chain with its per-step deltas and the nodes
 * each step dropped. Source says where the nodes come from, with a provider
 * link masked after the host until revealed for a minute. Output, files only,
 * is the document a client receives. Publishing is every share of it.
 */
const props = defineProps<{ id: string; from: string }>();
const emit = defineEmits<{
  back: [];
  edit: [id: string];
  /** Deleted from this page's menu; the shell goes to the record's table. */
  deleted: [kind: string, text: string, shares: string[]];
}>();

/** What the row menu's last action did, until dismissed or another record opens. */
const actionStatus = ref<{ text: string; tone: "success" | "danger" } | null>(null);
watch(() => props.id, () => {
  actionStatus.value = null;
});

const host = useHost();
const chrome = useLensChrome();
const subs = useSubscriptions(host);
const pipe = usePipeline(host);
const chain = useRecordChain(host, subs, pipe.counts);
const reveal = useReveal();
useOverlayEscape();

const item = computed(() => pipe.item(props.id));
const kind = computed(() => item.value?.kind || KIND_SUB);
const isFile = computed(() => kind.value === KIND_FILE);
const health = computed(() => pipe.health(props.id));

type RecordTab = "nodes" | "steps" | "source" | "output" | "publishing";
const tab = ref<RecordTab>("nodes");
const tabs = computed(() => {
  const list: { id: RecordTab; label: string }[] = isFile.value
    ? [{ id: "output", label: t.page.tabOutput }]
    : [{ id: "nodes", label: t.page.tabNodes }];
  list.push({ id: "steps", label: t.page.tabSteps }, { id: "source", label: t.page.tabSource }, { id: "publishing", label: t.page.tabPublishing });
  return list;
});

/* Declared before load(): the watcher below runs load() during setup once
 * the handshake is in, and load() resets this. */
const rendered = ref<{ output: SubscriptionRenderResponse | null; error: string; busy: boolean }>({ output: null, error: "", busy: false });
const readAt = ref(0);
async function load(): Promise<void> {
  if (!props.id) return;
  reveal.hide();
  tab.value = isFile.value ? "output" : "nodes";
  rendered.value = { output: null, error: "", busy: false };
  await chain.load(props.id);
  readAt.value = Date.now();
}

watch(
  () => [props.id, host.init.value] as const,
  ([id, init]) => {
    if (id && init) void load();
  },
  { immediate: true },
);
watch(item, (value, old) => {
  if (value && !old && tab.value === "nodes" && value.kind === KIND_FILE) tab.value = "output";
});
onActivated(() => {
  if (props.id && host.init.value && chain.id.value !== props.id) void load();
});

// ── header ──────────────────────────────────────────────────────────────────

const kindLabel = computed(() => {
  const record = item.value;
  if (!record) return "";
  if (kind.value === KIND_COLLECTION) return t.record.kindCombination;
  if (kind.value === KIND_FILE) return t.page.kindClientFile;
  return t.page.kindSource(sourceKindLabel(record));
});

/** The layer the page was opened from, else Records, which lists every record. */
const backLabel = computed(() => {
  const layers: Record<string, string> = { overview: t.layers.overview, records: t.layers.records, shares: t.layers.shares, settings: t.layers.settings };
  return layers[props.from] ?? t.layers.records;
});

function nameOf(id: string): string {
  const record = pipe.item(id);
  return record ? record.display_name || record.name : id;
}

const upstream = computed(() => pipe.lineage.value.upstream.get(props.id) ?? []);
const lineageLine = computed(() => usedBySentence(pipe.lineage.value, props.id));

const proof = computed(() => {
  const parts: string[] = [];
  parts.push(readAt.value ? t.page.proofReadAt(formatTime(readAt.value)) : t.page.proofReading);
  const state = pipe.counts.stateOf(props.id);
  if (state?.status === "ready") parts.push(t.page.proofPreview(formatRelativeTime(new Date(state.at).toISOString(), pipe.now.value) || t.time.justNow));
  if (state?.status === "failed") parts.push(t.page.proofPreviewFailed);
  const record = item.value;
  if (record && isProviderLink(record)) parts.push(refreshStateFor(record, pipe.now.value).label.toLowerCase());
  parts.push(props.id);
  return parts;
});

// ── source ──────────────────────────────────────────────────────────────────

const figures = computed(() => (item.value && isProviderLink(item.value) ? providerFigures(item.value) : null));
const refresh = computed(() => (item.value && isProviderLink(item.value) ? refreshStateFor(item.value, pipe.now.value) : null));
const url = computed(() => chain.record.value?.url ?? "");
const pastedLines = computed(() => {
  const content = chain.record.value?.content ?? "";
  if (!content.trim()) return 0;
  return content.split("\n").filter((line) => line.trim()).length;
});
const broken = computed(() => pipe.lineage.value.broken.filter((ref) => ref.owner === props.id));
const client = computed(() => (item.value && isFile.value ? clientOfFile(item.value.name) : null));

// ── output (files) ──────────────────────────────────────────────────────────

const canRender = computed(() => host.available(BINDINGS.subRender));
async function renderOutput(): Promise<void> {
  if (!host.bridge || !canRender.value) return;
  rendered.value = { output: null, error: "", busy: true };
  try {
    const output = await callMethod<SubscriptionRenderResponse>(host.bridge, BINDINGS.subRender, { subscription_id: props.id }).promise;
    rendered.value = { output, error: "", busy: false };
  } catch (cause) {
    rendered.value = { output: null, error: safeErrorMessage(cause, t.page.renderFailed), busy: false };
  }
}
watch(tab, (value) => {
  if (value === "output" && !rendered.value.output && !rendered.value.busy && !rendered.value.error) void renderOutput();
});
watch(
  () => chain.record.value,
  (record) => {
    if (record && record.kind === KIND_FILE && tab.value === "output") void renderOutput();
  },
);
const outputLanguage = computed(() => editorLanguageForContentType(rendered.value.output?.content_type ?? ""));
const copiedNote = ref("");
const manualCopy = ref("");
async function copyOutput(): Promise<void> {
  const text = rendered.value.output?.content ?? "";
  if (!text) return;
  manualCopy.value = "";
  if (await copyText(text)) copiedNote.value = t.page.copiedDocument;
  else manualCopy.value = text;
}

// ── publishing ──────────────────────────────────────────────────────────────

const shares = computed(() => (pipe.shares.value ?? []).filter((share) => share.subscription_id === props.id));
const shareOrigin = computed(() => hostOriginFromHash(typeof window === "undefined" ? "" : window.location.hash));
function publish(): void {
  if (!shareOrigin.value || !item.value) return;
  postNavigate(window, shares.value.length ? SHARES_LIST_ROUTE : sharesRoute(item.value.id), shareOrigin.value);
  copiedNote.value = t.page.askedShareForm;
}
async function copyLink(link: string): Promise<void> {
  manualCopy.value = "";
  if (await copyText(link)) copiedNote.value = t.record.copiedLink;
  else manualCopy.value = link;
}

const outputSheet = ref(false);
</script>

<template>
  <section class="record-page" aria-labelledby="record-title">
    <nav class="record-crumbs" :aria-label="t.page.backNav">
      <PcButton compact @click="emit('back')">
        <template #icon><ArrowLeft :size="14" aria-hidden="true" /></template>
        {{ backLabel }}
      </PcButton>
    </nav>

    <PcPanel v-if="!host.init.value || pipe.catalogue.state.value !== 'ready'" :label="t.page.loadingPanel">
      <PcSkeleton :count="4" :label="t.page.reading" />
    </PcPanel>

    <PcPanel v-else-if="!item" :label="t.page.notFoundPanel">
      <PcEmptyState kind="no-match" :title="t.page.notFoundTitle">
        <p>{{ t.page.notFoundBefore }} <span class="pc-mono">{{ id }}</span>{{ t.page.notFoundAfter }}</p>
        <template #actions><PcButton @click="emit('back')">{{ t.page.backTo(backLabel) }}</PcButton></template>
      </PcEmptyState>
    </PcPanel>

    <template v-else>
      <header class="record-head">
        <div class="record-title-line">
          <h2 id="record-title">{{ item.display_name || item.name }}</h2>
          <PcKindChip :label="kindLabel" />
          <PcKindChip v-if="item.imported" :label="t.records.migratedTag" :title="t.record.importedTitle" />
          <PcStateDot :tone="health.tone" :label="health.label" :title="health.title" />
          <PcStateDot v-if="isFlagged(item)" tone="warning" :label="t.records.flagged" :title="t.records.flaggedTitle" data-testid="record-flagged" />
          <div class="record-actions">
            <PcButton v-if="subs.canRender.value" @click="outputSheet = true">{{ t.page.clientOutput }}</PcButton>
            <PcButton :disabled="!subs.canMutate.value" :title="subs.canMutate.value ? t.record.editTitle : t.record.editBlocked" @click="emit('edit', id)">{{ t.actions.edit }}</PcButton>
            <!-- The table row's menu, so a record opened from a link can be
                 refreshed, copied or deleted without going back first. -->
            <RecordActions
              :id="id"
              :pipe="pipe"
              @status="(text, tone) => (actionStatus = { text, tone })"
              @deleted="(kind, text, broken) => emit('deleted', kind, text, broken)"
            />
          </div>
        </div>
        <p class="record-lineage">
          <template v-if="upstream.length">
            {{ t.page.lineageFrom }}
            <template v-for="(parent, index) in upstream" :key="parent">
              <button type="button" class="peek-link" @click="chrome.openPage(parent)">{{ nameOf(parent) }}</button><template v-if="index < upstream.length - 1">, </template>
            </template>
            <template v-if="lineageLine"> · </template>
          </template>
          <span v-if="lineageLine">{{ lineageLine }}</span>
          <span v-if="!upstream.length && !lineageLine" class="peek-note">{{ t.page.nothingFeeds }}</span>
        </p>
        <!-- The flag's reason in words, for a pointer with no hover; Edit is the way out. -->
        <p v-if="isFlagged(item)" class="peek-why">{{ t.records.flaggedTitle }}</p>
        <PcProofLine :segments="proof" :refreshing="chain.loading.value" />
      </header>

      <PcNotice v-if="actionStatus" :tone="actionStatus.tone" dismissible @dismiss="actionStatus = null">{{ actionStatus.text }}</PcNotice>
      <PcNotice v-if="copiedNote" tone="success" dismissible @dismiss="copiedNote = ''">{{ copiedNote }}</PcNotice>
      <div v-if="manualCopy" class="manual-copy-strip">
        <div class="manual-copy-strip__head">
          <span class="manual-copy-strip__label">{{ t.page.clipboardRefused }}</span>
          <PcButton compact @click="manualCopy = ''">{{ t.common.dismiss }}</PcButton>
        </div>
        <LtManualCopy :value="manualCopy" subject="text" :multiline="manualCopy.includes('\n')" />
      </div>

      <PcLensTabs v-model="tab" :label="t.page.sections" class="record-tabs">
        <PcLensTab v-for="entry in tabs" :key="entry.id" :value="entry.id" :label="entry.label" />
      </PcLensTabs>

      <div :id="`pc-panel-${tab}`" class="record-body" role="tabpanel" :aria-labelledby="`pc-tab-${tab}`">
        <!-- Nodes: the compare panel -->
        <PcPanel v-if="tab === 'nodes'" :label="t.page.tabNodes">
          <PcPanelHeader :title="t.page.compareTitle" :description="t.page.compareDescription" />
          <PcPanelBody>
            <p v-if="!pipe.canPreview.value" class="rec-chain-note">{{ t.page.cannotPreview }}</p>
            <PcSkeleton v-else-if="chain.loading.value || (!chain.explanation.value && !chain.error.value)" :count="3" :label="t.page.runningPreview" />
            <PcNotice v-else-if="chain.error.value && !chain.explanation.value?.final" tone="danger" :title="t.page.previewFailed">
              {{ chain.error.value }}
              <template #actions><PcButton compact @click="load()">{{ t.common.tryAgain }}</PcButton></template>
            </PcNotice>
            <template v-else-if="chain.explanation.value?.final">
              <PcNotice v-if="chain.error.value" tone="warning" :title="t.page.previewPartial">{{ chain.error.value }}</PcNotice>
              <SubscriptionPreviewSummary
                :preview="chain.explanation.value.final"
                :deltas="chain.explanation.value.deltas"
                :dropped-by="chain.explanation.value.droppedBy"
              />
            </template>
          </PcPanelBody>
        </PcPanel>

        <!-- Steps: the chain -->
        <PcPanel v-else-if="tab === 'steps'" :label="t.page.tabSteps">
          <PcPanelHeader :title="t.page.operations" :description="isFile ? t.page.operationsFile : t.page.operationsNodes" />
          <PcPanelBody>
            <RecordChainDetail
              :loading="chain.loading.value"
              :error="chain.error.value"
              :steps="chain.steps.value"
              :deltas="chain.explanation.value?.deltas || []"
              :dropped="chain.explanation.value?.final?.dropped || []"
              :dropped-by="chain.explanation.value?.droppedBy"
              :dropped-count="chain.explanation.value?.final?.dropped_count || 0"
              :dropped-truncated="!!chain.explanation.value?.final?.dropped_truncated"
              :is-combination="chain.isCombination.value"
              :can-preview="pipe.canPreview.value && !isFile"
              :running="chain.running.value"
              :final="chain.explanation.value?.final || null"
            />
          </PcPanelBody>
        </PcPanel>

        <!-- Source -->
        <PcPanel v-else-if="tab === 'source'" :label="t.page.tabSource">
          <PcPanelHeader :title="t.page.sourceTitle" />
          <PcPanelBody>
            <dl class="peek-facts record-facts">
              <template v-if="kind === KIND_SUB">
                <dt>{{ t.record.kind }}</dt>
                <dd>{{ sourceKindLabel(item) }}</dd>
                <template v-if="url">
                  <dt>{{ t.record.link }}</dt>
                  <dd class="record-url">
                    <code :title="reveal.on.value ? t.page.revealOnTitle : t.page.revealOffTitle">{{ reveal.on.value ? url : maskUrl(url) }}</code>
                    <PcButton compact :aria-pressed="reveal.on.value ? 'true' : 'false'" @click="reveal.toggle()">
                      <template #icon><EyeOff v-if="reveal.on.value" :size="14" aria-hidden="true" /><Eye v-else :size="14" aria-hidden="true" /></template>
                      {{ reveal.on.value ? t.page.mask : t.page.reveal }}
                    </PcButton>
                  </dd>
                </template>
                <template v-if="figures">
                  <dt>{{ t.record.traffic }}</dt>
                  <dd class="peek-usage">
                    <UsageBar :figures="figures" />
                    <span v-if="formatUsage(figures)" class="peek-note">{{ formatUsage(figures) }}</span>
                  </dd>
                  <dt>{{ t.record.expiry }}</dt>
                  <dd>{{ formatExpiry(figures, pipe.now.value) || t.page.noExpiry }}</dd>
                </template>
                <template v-if="refresh">
                  <dt>{{ t.record.lastRefresh }}</dt>
                  <dd><PcStateDot :tone="stateTone(refresh.tone)" :label="refresh.label" :title="refresh.title || refresh.label" /></dd>
                </template>
                <template v-if="!url && pastedLines">
                  <dt>{{ t.record.pasted }}</dt>
                  <dd>{{ t.page.pastedLines(pastedLines) }}</dd>
                </template>
                <template v-if="chain.record.value?.vpn_identity">
                  <dt>{{ t.record.identity }}</dt>
                  <dd class="peek-mono">{{ chain.record.value.vpn_identity }}</dd>
                </template>
              </template>

              <template v-else-if="kind === KIND_COLLECTION">
                <dt>{{ t.record.members }}</dt>
                <dd>
                  <ul class="peek-links">
                    <li v-for="member in upstream" :key="member">
                      <button type="button" class="peek-link" @click="chrome.openPage(member)">{{ nameOf(member) }}</button>
                      <span class="peek-note">{{ pipe.nodes(member) }}</span>
                    </li>
                    <li v-for="ref in broken" :key="ref.ref" class="peek-broken">{{ ref.ref }} <span>{{ ref.reason }}</span></li>
                  </ul>
                </dd>
                <template v-if="item.member_tags?.length">
                  <dt>{{ t.record.byTag }}</dt>
                  <dd>{{ item.member_tags.join(", ") }}</dd>
                </template>
                <dt>{{ t.record.ifMemberFails }}</dt>
                <dd>{{ chain.record.value?.failure_mode === "skip-failed" ? t.page.serveOthers : t.page.serveNothing }}</dd>
              </template>

              <template v-else>
                <dt>{{ t.record.renders }}</dt>
                <dd>
                  <button v-if="upstream[0]" type="button" class="peek-link" @click="chrome.openPage(upstream[0]!)">{{ nameOf(upstream[0]!) }}</button>
                  <span v-else-if="broken[0]" class="peek-broken">{{ broken[0].ref }} <span>{{ broken[0].reason }}</span></span>
                  <span v-else class="peek-note">{{ t.record.servedAsWritten }}</span>
                </dd>
                <dt>{{ t.record.client }}</dt>
                <dd>{{ client?.label ?? t.page.clientNotInName }}</dd>
                <dt>{{ t.record.kind }}</dt>
                <dd>{{ item.file_type === "plain" ? t.page.fileKindPlain : item.file_type === "script" ? t.page.fileKindScript : t.page.fileKindConfig }}</dd>
                <template v-if="url">
                  <dt>{{ t.record.template }}</dt>
                  <dd class="record-url">
                    <code>{{ reveal.on.value ? url : maskUrl(url) }}</code>
                    <PcButton compact @click="reveal.toggle()">{{ reveal.on.value ? t.page.mask : t.page.reveal }}</PcButton>
                  </dd>
                </template>
              </template>
            </dl>
          </PcPanelBody>
        </PcPanel>

        <!-- Output: what a client receives (files) -->
        <PcPanel v-else-if="tab === 'output'" :label="t.page.tabOutput">
          <PcPanelHeader :title="t.page.outputTitle" :description="rendered.output ? t.page.outputDescription(rendered.output.content_type, rendered.output.content.length) : ''">
            <PcButton v-if="rendered.output" compact @click="copyOutput()">
              <template #icon><Copy :size="14" aria-hidden="true" /></template>
              {{ t.page.copyDocument }}
            </PcButton>
          </PcPanelHeader>
          <PcPanelBody>
            <p v-if="!canRender" class="rec-chain-note">{{ t.page.renderNeedsAdmin }}</p>
            <PcSkeleton v-else-if="rendered.busy || (!rendered.output && !rendered.error)" :count="4" :label="t.page.rendering" />
            <PcNotice v-else-if="rendered.error" tone="danger" :title="t.page.renderFailed">
              {{ rendered.error }}
              <template #actions><PcButton compact @click="renderOutput()">{{ t.common.tryAgain }}</PcButton></template>
            </PcNotice>
            <DocumentView v-else-if="rendered.output" :text="rendered.output.content" :language="outputLanguage" />
          </PcPanelBody>
        </PcPanel>

        <!-- Publishing -->
        <PcPanel v-else :label="t.page.tabPublishing">
          <PcPanelHeader :title="t.page.sharesTitle" :description="t.page.sharesDescription">
            <PcButton v-if="shareOrigin" compact @click="publish()">{{ shares.length ? t.records.openInPublishing : t.record.publish }}</PcButton>
          </PcPanelHeader>
          <PcPanelBody>
            <p v-if="pipe.shares.value === undefined" class="rec-chain-note">{{ pipe.shareStore.error.value || t.publish.unreadTitle }}</p>
            <p v-else-if="!shares.length" class="rec-chain-note">{{ t.page.notPublished }}</p>
            <ul v-else class="record-shares">
              <li v-for="share in shares" :key="share.share_id">
                <span class="peek-mono">/{{ share.slug }}</span>
                <PcStateDot :tone="stateTone(shareStateOf(share, pipe.now.value).tone)" :label="shareStateOf(share, pipe.now.value).label" :title="shareStateOf(share, pipe.now.value).title" />
                <span class="peek-note">{{ share.default_format ? t.page.asFormat(share.default_format) : t.page.asClientAsks }}</span>
                <span v-if="share.expires_at" class="peek-note">{{ t.page.until(Number.isFinite(Date.parse(share.expires_at)) ? formatDate(Date.parse(share.expires_at)) : share.expires_at.slice(0, 10)) }}</span>
                <PcButton compact :disabled="!shareLinkOf(share)" @click="copyLink(shareLinkOf(share))">{{ t.record.copyLink }}</PcButton>
              </li>
            </ul>
          </PcPanelBody>
        </PcPanel>
      </div>

      <TargetSheet :open="outputSheet" :record="outputSheet ? item : null" @close="outputSheet = false" />
    </template>
  </section>
</template>
