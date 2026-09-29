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
import TargetSheet from "../components/TargetSheet.vue";
import UsageBar from "../components/UsageBar.vue";
import { useHost } from "../host";
import { copyText } from "../hostClipboard";
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
  viewOfKind,
} from "../pipeline";
import { editorLanguageForContentType } from "../previewLanguage";
import { formatRelativeTime } from "../rowStatus";
import { refreshStateFor, shareStateOf, stateTone } from "../shareState";
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
const emit = defineEmits<{ back: []; edit: [id: string] }>();

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
    ? [{ id: "output", label: "Output" }]
    : [{ id: "nodes", label: "Nodes" }];
  list.push({ id: "steps", label: "Steps" }, { id: "source", label: "Source" }, { id: "publishing", label: "Publishing" });
  return list;
});

const readAt = ref("");
async function load(): Promise<void> {
  if (!props.id) return;
  reveal.hide();
  tab.value = isFile.value ? "output" : "nodes";
  rendered.value = { output: null, error: "", busy: false };
  await chain.load(props.id);
  const now = new Date();
  readAt.value = [now.getHours(), now.getMinutes(), now.getSeconds()].map((n) => String(n).padStart(2, "0")).join(":");
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
  if (kind.value === KIND_COLLECTION) return "Combination";
  if (kind.value === KIND_FILE) return "Client file";
  return `Source, ${sourceKindLabel(record).toLowerCase()}`;
});

const fromView = computed(() => viewOfKind(item.value?.kind));
const VIEW_LABEL: Record<string, string> = {
  overview: "Overview",
  sources: "Sources",
  combinations: "Combinations",
  files: "Files",
  shares: "Shares",
  settings: "Settings",
};
const backLabel = computed(() => VIEW_LABEL[props.from] ?? VIEW_LABEL[fromView.value]!);

function nameOf(id: string): string {
  const record = pipe.item(id);
  return record ? record.display_name || record.name : id;
}

const upstream = computed(() => pipe.lineage.value.upstream.get(props.id) ?? []);
const lineageLine = computed(() => usedBySentence(pipe.lineage.value, props.id));

const proof = computed(() => {
  const parts: string[] = [];
  parts.push(readAt.value ? `read at ${readAt.value}` : "reading the record");
  const state = pipe.counts.stateOf(props.id);
  if (state?.status === "ready") parts.push(`preview ${formatRelativeTime(new Date(state.at).toISOString(), pipe.now.value) || "just now"}`);
  if (state?.status === "failed") parts.push("preview failed");
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
const rendered = ref<{ output: SubscriptionRenderResponse | null; error: string; busy: boolean }>({ output: null, error: "", busy: false });
async function renderOutput(): Promise<void> {
  if (!host.bridge || !canRender.value) return;
  rendered.value = { output: null, error: "", busy: true };
  try {
    const output = await callMethod<SubscriptionRenderResponse>(host.bridge, BINDINGS.subRender, { subscription_id: props.id }).promise;
    rendered.value = { output, error: "", busy: false };
  } catch (cause) {
    rendered.value = { output: null, error: safeErrorMessage(cause, "The file could not be rendered"), busy: false };
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
  if (await copyText(text)) copiedNote.value = "Copied the document.";
  else manualCopy.value = text;
}

// ── publishing ──────────────────────────────────────────────────────────────

const shares = computed(() => (pipe.shares.value ?? []).filter((share) => share.subscription_id === props.id));
const shareOrigin = computed(() => hostOriginFromHash(typeof window === "undefined" ? "" : window.location.hash));
function publish(): void {
  if (!shareOrigin.value || !item.value) return;
  postNavigate(window, shares.value.length ? SHARES_LIST_ROUTE : sharesRoute(item.value.name), shareOrigin.value);
  copiedNote.value = "Asked the console to open its share form.";
}
async function copyLink(link: string): Promise<void> {
  manualCopy.value = "";
  if (await copyText(link)) copiedNote.value = "Copied the link.";
  else manualCopy.value = link;
}

const outputSheet = ref(false);
</script>

<template>
  <section class="record-page" aria-labelledby="record-title">
    <nav class="record-crumbs" aria-label="Back">
      <PcButton compact @click="emit('back')">
        <template #icon><ArrowLeft :size="14" aria-hidden="true" /></template>
        {{ backLabel }}
      </PcButton>
    </nav>

    <PcPanel v-if="!host.init.value || pipe.catalogue.state.value !== 'ready'" label="Loading the record">
      <PcSkeleton :count="4" label="Reading the record" />
    </PcPanel>

    <PcPanel v-else-if="!item" label="Record not found">
      <PcEmptyState kind="no-match" title="This record is not in the store">
        <p>Nothing here has the id <span class="pc-mono">{{ id }}</span>. It may have been deleted since the link was made.</p>
        <template #actions><PcButton @click="emit('back')">Back to {{ backLabel }}</PcButton></template>
      </PcEmptyState>
    </PcPanel>

    <template v-else>
      <header class="record-head">
        <div class="record-title-line">
          <h2 id="record-title">{{ item.display_name || item.name }}</h2>
          <PcKindChip :label="kindLabel" />
          <PcKindChip v-if="item.imported" label="migrated" title="Imported from a standalone Sub-Store" />
          <PcStateDot :tone="health.tone" :label="health.label" :title="health.title" />
          <div class="record-actions">
            <PcButton v-if="subs.canRender.value" @click="outputSheet = true">Client output</PcButton>
            <PcButton :disabled="!subs.canMutate.value" :title="subs.canMutate.value ? 'Change this record' : 'This session cannot change records here.'" @click="emit('edit', id)">Edit</PcButton>
          </div>
        </div>
        <p class="record-lineage">
          <template v-if="upstream.length">
            from
            <template v-for="(parent, index) in upstream" :key="parent">
              <button type="button" class="peek-link" @click="chrome.openPage(parent)">{{ nameOf(parent) }}</button><template v-if="index < upstream.length - 1">, </template>
            </template>
            <template v-if="lineageLine"> · </template>
          </template>
          <span v-if="lineageLine">{{ lineageLine }}</span>
          <span v-if="!upstream.length && !lineageLine" class="peek-note">Nothing feeds it and nothing uses it</span>
        </p>
        <PcProofLine :segments="proof" :refreshing="chain.loading.value" />
      </header>

      <PcNotice v-if="copiedNote" tone="success" dismissible @dismiss="copiedNote = ''">{{ copiedNote }}</PcNotice>
      <div v-if="manualCopy" class="manual-copy-strip">
        <div class="manual-copy-strip__head">
          <span class="manual-copy-strip__label">The clipboard refused; select and copy it here</span>
          <PcButton compact @click="manualCopy = ''">Dismiss</PcButton>
        </div>
        <LtManualCopy :value="manualCopy" subject="text" :multiline="manualCopy.includes('\n')" />
      </div>

      <PcLensTabs v-model="tab" label="Record sections" class="record-tabs">
        <PcLensTab v-for="entry in tabs" :key="entry.id" :value="entry.id" :label="entry.label" />
      </PcLensTabs>

      <div :id="`pc-panel-${tab}`" class="record-body" role="tabpanel" :aria-labelledby="`pc-tab-${tab}`">
        <!-- Nodes: the compare panel -->
        <PcPanel v-if="tab === 'nodes'" label="Nodes">
          <PcPanelHeader title="Source against result" description="What the source provides, what the chain keeps, and which operation removed the rest." />
          <PcPanelBody>
            <p v-if="!pipe.canPreview.value" class="rec-chain-note">This session cannot run a preview, so the nodes are not shown.</p>
            <PcSkeleton v-else-if="chain.loading.value || (!chain.explanation.value && !chain.error.value)" :count="3" label="Running the preview" />
            <PcNotice v-else-if="chain.error.value && !chain.explanation.value?.final" tone="danger" title="The preview failed">
              {{ chain.error.value }}
              <template #actions><PcButton compact @click="load()">Try again</PcButton></template>
            </PcNotice>
            <template v-else-if="chain.explanation.value?.final">
              <PcNotice v-if="chain.error.value" tone="warning" title="The preview stopped part way">{{ chain.error.value }}</PcNotice>
              <SubscriptionPreviewSummary
                :preview="chain.explanation.value.final"
                :deltas="chain.explanation.value.deltas"
                :dropped-by="chain.explanation.value.droppedBy"
              />
            </template>
          </PcPanelBody>
        </PcPanel>

        <!-- Steps: the chain -->
        <PcPanel v-else-if="tab === 'steps'" label="Steps">
          <PcPanelHeader title="Operations" :description="isFile ? 'Run in order over the nodes before they are placed into the document.' : 'Run in order over the source nodes. Each line says what it kept.'" />
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
        <PcPanel v-else-if="tab === 'source'" label="Source">
          <PcPanelHeader title="Where it comes from" />
          <PcPanelBody>
            <dl class="peek-facts record-facts">
              <template v-if="kind === KIND_SUB">
                <dt>Kind</dt>
                <dd>{{ sourceKindLabel(item) }}</dd>
                <template v-if="url">
                  <dt>Link</dt>
                  <dd class="record-url">
                    <code :title="reveal.on.value ? 'Masks itself again after a minute' : 'Masked after the host: the rest carries the provider token'">{{ reveal.on.value ? url : maskUrl(url) }}</code>
                    <PcButton compact :aria-pressed="reveal.on.value ? 'true' : 'false'" @click="reveal.toggle()">
                      <template #icon><EyeOff v-if="reveal.on.value" :size="14" aria-hidden="true" /><Eye v-else :size="14" aria-hidden="true" /></template>
                      {{ reveal.on.value ? "Mask" : "Reveal for 60s" }}
                    </PcButton>
                  </dd>
                </template>
                <template v-if="figures">
                  <dt>Traffic</dt>
                  <dd class="peek-usage">
                    <UsageBar :figures="figures" />
                    <span v-if="formatUsage(figures)" class="peek-note">{{ formatUsage(figures) }}</span>
                  </dd>
                  <dt>Expiry</dt>
                  <dd>{{ formatExpiry(figures, pipe.now.value) || "The provider does not say" }}</dd>
                </template>
                <template v-if="refresh">
                  <dt>Last refresh</dt>
                  <dd><PcStateDot :tone="stateTone(refresh.tone)" :label="refresh.label" :title="refresh.title || refresh.label" /></dd>
                </template>
                <template v-if="!url && pastedLines">
                  <dt>Pasted</dt>
                  <dd>{{ pastedLines }} line{{ pastedLines === 1 ? "" : "s" }} of nodes, edited in the editor</dd>
                </template>
                <template v-if="chain.record.value?.vpn_identity">
                  <dt>Identity</dt>
                  <dd class="peek-mono">{{ chain.record.value.vpn_identity }}</dd>
                </template>
              </template>

              <template v-else-if="kind === KIND_COLLECTION">
                <dt>Members</dt>
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
                  <dt>By tag</dt>
                  <dd>{{ item.member_tags.join(", ") }}</dd>
                </template>
                <dt>If a member fails</dt>
                <dd>{{ chain.record.value?.failure_mode === "skip-failed" ? "Serve the others" : "Serve nothing (strict)" }}</dd>
              </template>

              <template v-else>
                <dt>Renders</dt>
                <dd>
                  <button v-if="upstream[0]" type="button" class="peek-link" @click="chrome.openPage(upstream[0]!)">{{ nameOf(upstream[0]!) }}</button>
                  <span v-else-if="broken[0]" class="peek-broken">{{ broken[0].ref }} <span>{{ broken[0].reason }}</span></span>
                  <span v-else class="peek-note">Nothing: the document is served as written</span>
                </dd>
                <dt>Client</dt>
                <dd>{{ client?.label ?? "Not named in the file's name" }}</dd>
                <dt>Kind</dt>
                <dd>{{ item.file_type === "plain" ? "Plain text, served as written" : item.file_type === "script" ? "Built by a script" : "Client configuration, proxy list filled in" }}</dd>
                <template v-if="url">
                  <dt>Template</dt>
                  <dd class="record-url">
                    <code>{{ reveal.on.value ? url : maskUrl(url) }}</code>
                    <PcButton compact @click="reveal.toggle()">{{ reveal.on.value ? "Mask" : "Reveal for 60s" }}</PcButton>
                  </dd>
                </template>
              </template>
            </dl>
          </PcPanelBody>
        </PcPanel>

        <!-- Output: what a client receives (files) -->
        <PcPanel v-else-if="tab === 'output'" label="Output">
          <PcPanelHeader title="What a client receives" :description="rendered.output ? `${rendered.output.content_type} · ${rendered.output.content.length} characters` : ''">
            <PcButton v-if="rendered.output" compact @click="copyOutput()">
              <template #icon><Copy :size="14" aria-hidden="true" /></template>
              Copy document
            </PcButton>
          </PcPanelHeader>
          <PcPanelBody>
            <p v-if="!canRender" class="rec-chain-note">Rendering needs the admin scope, which this session does not have.</p>
            <PcSkeleton v-else-if="rendered.busy || (!rendered.output && !rendered.error)" :count="4" label="Rendering the document" />
            <PcNotice v-else-if="rendered.error" tone="danger" title="The file could not be rendered">
              {{ rendered.error }}
              <template #actions><PcButton compact @click="renderOutput()">Try again</PcButton></template>
            </PcNotice>
            <DocumentView v-else-if="rendered.output" :text="rendered.output.content" :language="outputLanguage" />
          </PcPanelBody>
        </PcPanel>

        <!-- Publishing -->
        <PcPanel v-else label="Publishing">
          <PcPanelHeader title="Shares" description="A share is the link a client fetches. Shares are created and changed in the console under Networking.">
            <PcButton v-if="shareOrigin" compact @click="publish()">{{ shares.length ? "Open in Networking" : "Publish" }}</PcButton>
          </PcPanelHeader>
          <PcPanelBody>
            <p v-if="pipe.shares.value === undefined" class="rec-chain-note">{{ pipe.shareStore.error.value || "The share list has not been read yet." }}</p>
            <p v-else-if="!shares.length" class="rec-chain-note">Not published. No client can fetch this record until a share exists for it.</p>
            <ul v-else class="record-shares">
              <li v-for="share in shares" :key="share.share_id">
                <span class="peek-mono">/{{ share.slug }}</span>
                <PcStateDot :tone="stateTone(shareStateOf(share, pipe.now.value).tone)" :label="shareStateOf(share, pipe.now.value).label" :title="shareStateOf(share, pipe.now.value).title" />
                <span class="peek-note">{{ share.default_format ? `as ${share.default_format}` : "as the client asks" }}</span>
                <span v-if="share.expires_at" class="peek-note">until {{ share.expires_at.slice(0, 10) }}</span>
                <PcButton compact @click="copyLink(share.url || share.path)">Copy link</PcButton>
              </li>
            </ul>
          </PcPanelBody>
        </PcPanel>
      </div>

      <TargetSheet :open="outputSheet" :record="outputSheet ? item : null" @close="outputSheet = false" />
    </template>
  </section>
</template>
