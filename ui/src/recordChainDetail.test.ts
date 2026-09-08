import { readFileSync } from "node:fs";
import { compile, createRenderer, nextTick, ssrContextKey } from "vue";
import { compileScript, parse } from "vue/compiler-sfc";
import { describe, expect, it, vi } from "vitest";

import RecordChainDetail from "./components/RecordChainDetail.vue";
import type { StepDelta } from "./chainExplain";
import type { SubscriptionPreviewNode, SubscriptionPreviewResponse } from "./client";
import type { ChainStep } from "./components/ProcessChain.vue";

function useTemplate(component: object, file: string, id: string): void {
  const source = readFileSync(new URL(`./components/${file}`, import.meta.url), "utf8");
  const descriptor = parse(source, { filename: file }).descriptor;
  if (!descriptor.template) throw new Error(`${file} template is missing`);
  const bindings = compileScript(descriptor, { id }).bindings;
  (component as { render: ReturnType<typeof compile> }).render = compile(descriptor.template.content, {
    bindingMetadata: bindings,
    prefixIdentifiers: true,
  });
}

useTemplate(RecordChainDetail, "RecordChainDetail.vue", "record-chain-detail-test");

vi.mock("@lucide/vue", async () => {
  const { defineComponent, h: create } = await import("vue");
  const icon = (name: string) =>
    defineComponent({ name, inheritAttrs: false, setup: () => () => create("svg", { "data-icon": name }) });
  return { ChevronLeft: icon("ChevronLeft"), ChevronRight: icon("ChevronRight"), LoaderCircle: icon("LoaderCircle") };
});

type HostNode = {
  type: string;
  props: Record<string, unknown>;
  style: Record<string, string>;
  children: HostNode[];
  text?: string;
  parent?: HostNode;
};

function renderer() {
  return createRenderer<HostNode, HostNode>({
    patchProp(node, key, _previous, value) { node.props[key] = value; },
    insert(child, parent, anchor) {
      child.parent = parent;
      const at = anchor ? parent.children.indexOf(anchor) : -1;
      if (at >= 0) parent.children.splice(at, 0, child);
      else parent.children.push(child);
    },
    remove(child) {
      if (child.parent) child.parent.children = child.parent.children.filter((item) => item !== child);
    },
    createElement(type) { return { type, props: {}, style: {}, children: [] }; },
    createText(text) { return { type: "#text", props: {}, style: {}, children: [], text }; },
    createComment(text) { return { type: "#comment", props: {}, style: {}, children: [], text }; },
    setText(node, text) { node.text = text; },
    setElementText(node, text) { node.text = text; },
    parentNode(node) { return node.parent ?? null; },
    nextSibling(node) {
      const siblings = node.parent?.children ?? [];
      return siblings[siblings.indexOf(node) + 1] ?? null;
    },
    querySelector() { return null; },
    setScopeId() {},
    cloneNode(node) {
      return { ...node, props: { ...node.props }, style: { ...node.style }, children: [...node.children] };
    },
    insertStaticContent() { throw new Error("static content is not used by the chain pane"); },
  });
}

function find(root: HostNode, predicate: (node: HostNode) => boolean): HostNode[] {
  return [
    ...(predicate(root) ? [root] : []),
    ...root.children.flatMap((child) => find(child, predicate)),
  ];
}

function textOf(node: HostNode): string {
  return [node.text ?? "", ...node.children.map(textOf)].join("");
}

function withClass(root: HostNode, name: string): HostNode[] {
  return find(root, (n) => String(n.props.class ?? "").split(/\s+/).includes(name));
}

function node(name: string, extra: Partial<SubscriptionPreviewNode> = {}): SubscriptionPreviewNode {
  return { name, type: "ss", server: `${name}.example`, port: "443", ...extra };
}

const STEPS: ChainStep[] = [
  { type: "Quick Setting Operator" },
  { type: "Regex Filter" },
  { type: "Regex Delete Operator" },
];

const DELTAS: StepDelta[] = [
  { index: 0, label: "1. Quick settings", before: 166, after: 166 },
  { index: 1, label: "2. Regex filter", before: 166, after: 25 },
  { index: 2, label: "3. Regex delete", before: 25, after: 25 },
];

function preview(extra: Partial<SubscriptionPreviewResponse> = {}): SubscriptionPreviewResponse {
  const dropped = Array.from({ length: 20 }, (_, i) => node(`asia-${String(i).padStart(2, "0")}`));
  return {
    nodes: Array.from({ length: 25 }, (_, i) => node(`hk-${String(i).padStart(2, "0")}`)),
    node_count: 25,
    source_node_count: 166,
    dropped,
    dropped_count: 141,
    dropped_truncated: true,
    ...extra,
  };
}

function mount(extra: Record<string, unknown> = {}) {
  const final = (extra.final as SubscriptionPreviewResponse | undefined) ?? preview();
  const droppedBy = (extra.droppedBy as Map<string, string> | undefined) ?? new Map(final.dropped!.map((n) => [`${n.server}:${n.port}`, "2. Regex filter"]));
  const root: HostNode = { type: "root", props: {}, style: {}, children: [] };
  const app = renderer().createApp(RecordChainDetail, {
    steps: STEPS,
    deltas: DELTAS,
    dropped: final.dropped,
    droppedBy,
    droppedCount: final.dropped_count,
    droppedTruncated: final.dropped_truncated,
    canPreview: true,
    final,
    sourceKind: "Provider link",
    url: "https://vip.example/api?token=secret",
    urlMasked: "https://vip.example/…",
    pageSize: 12,
    ...extra,
  });
  app.provide(ssrContextKey, { modules: new Set<string>() });
  app.mount(root);
  return { root, app };
}

describe("the expanded record is a chain pane, not extra table rows", () => {
  it("states the cut as a proof line and lists operations as a process", () => {
    const { root } = mount();
    expect(textOf(withClass(root, "rec-chain-proof")[0])).toContain("166 → 25");
    expect(textOf(withClass(root, "rec-chain-proof")[0])).toContain("166 → 25 · 3 operations · 141 dropped");
    const steps = withClass(root, "rec-chain-steps")[0];
    expect(textOf(steps)).toContain("Quick settings");
    expect(textOf(steps)).toContain("Regex filter");
    expect(textOf(steps)).toContain("dropped 141");
    expect(textOf(steps)).toContain("kept all");
    expect(find(root, (n) => n.type === "tr")).toHaveLength(0);
  });

  it("does not dump dropped names until a cut is chosen", () => {
    const { root } = mount();
    expect(withClass(root, "rec-chain-dropped")).toHaveLength(0);
    expect(textOf(root)).toContain("141 dropped. Choose a cut to name them");
  });

  it("names the drop reason once, as the group heading, not on every node", async () => {
    const { root } = mount();
    const cuts = find(root, (n) => n.type === "button" && String(n.props.class ?? "").split(/\s+/).includes("rec-chain-step"));
    expect(cuts).toHaveLength(1);
    (cuts[0]!.props.onClick as () => void)();
    await nextTick();
    expect(textOf(withClass(root, "rec-chain-dropped-head")[0])).toContain("Dropped by 2. Regex filter");
    expect(textOf(root)).not.toContain("removed by");
    const names = withClass(root, "rec-chain-dropped-name");
    expect(names).toHaveLength(12);
    expect(textOf(names[0])).toContain("asia-00");
  });

  it("pages a long dropped set instead of dumping it", async () => {
    const { root } = mount();
    const cuts = find(root, (n) => n.type === "button" && String(n.props.class ?? "").split(/\s+/).includes("rec-chain-step"));
    (cuts[0]!.props.onClick as () => void)();
    await nextTick();
    expect(textOf(root)).toContain("1 to 12 of 20");
    expect(textOf(root)).toContain("Naming the first 20 of 141");
    const next = find(root, (n) => n.props["aria-label"] === "Next page")[0]!;
    (next.props.onClick as () => void)();
    await nextTick();
    expect(textOf(root)).toContain("13 to 20 of 20");
    expect(textOf(root)).toContain("asia-12");
    expect(textOf(root)).not.toContain("asia-00");
  });

  it("clicking a cut operation shows that group's names", async () => {
    const dropped = [node("us-01"), node("us-02"), node("sg-01")];
    const droppedBy = new Map([
      ["us-01.example:443", "1. Region filter"],
      ["us-02.example:443", "1. Region filter"],
      ["sg-01.example:443", "2. Regex filter"],
    ]);
    const { root } = mount({
      steps: [{ type: "Region Filter" }, { type: "Regex Filter" }],
      deltas: [
        { index: 0, label: "1. Region filter", before: 10, after: 8 },
        { index: 1, label: "2. Regex filter", before: 8, after: 7 },
      ],
      dropped,
      droppedBy,
      droppedCount: 3,
      droppedTruncated: false,
      final: preview({ dropped, dropped_count: 3, dropped_truncated: false, node_count: 7, source_node_count: 10 }),
    });
    expect(withClass(root, "rec-chain-dropped")).toHaveLength(0);
    const cuts = find(root, (n) => n.type === "button" && String(n.props.class ?? "").split(/\s+/).includes("rec-chain-step"));
    expect(cuts).toHaveLength(2);
    (cuts[1]!.props.onClick as () => void)();
    await nextTick();
    expect(textOf(withClass(root, "rec-chain-dropped-head")[0])).toContain("2. Regex filter");
    expect(textOf(root)).toContain("sg-01");
    expect(textOf(root)).not.toContain("us-01");
  });

  it("says it is reading while the record is in flight", () => {
    const { root } = mount({
      loading: true,
      steps: [],
      deltas: [],
      dropped: [],
      droppedBy: new Map(),
      droppedCount: 0,
      droppedTruncated: false,
      final: null,
    });
    expect(textOf(root)).toContain("Reading the record");
    expect(textOf(root)).not.toContain("No operations");
    expect(withClass(root, "rec-chain-steps")).toHaveLength(0);
  });

  it("does not claim there are no operations when the record could not be read", () => {
    const { root } = mount({
      error: "The record could not be read",
      steps: [],
      deltas: [],
      dropped: [],
      droppedBy: new Map(),
      droppedCount: 0,
      droppedTruncated: false,
      final: null,
      url: "",
      sourceKind: "Provider link",
    });
    expect(textOf(root)).toContain("The record could not be read");
    expect(textOf(root)).not.toContain("No operations");
    expect(withClass(root, "rec-chain-source")).toHaveLength(0);
    expect(withClass(root, "rec-chain-steps")).toHaveLength(0);
  });

  it("says the source is served as-is when there are no operations", () => {
    const final = preview({ dropped: [], dropped_count: 0, dropped_truncated: false, node_count: 8, source_node_count: 8 });
    const { root } = mount({
      steps: [],
      deltas: [],
      dropped: [],
      droppedBy: new Map(),
      droppedCount: 0,
      droppedTruncated: false,
      final,
    });
    expect(textOf(root)).toContain("No operations");
    expect(textOf(root)).toContain("8 of them");
    expect(withClass(root, "rec-chain-dropped")).toHaveLength(0);
  });

  it("does not invent a dropped list when the chain kept everyone", () => {
    const final = preview({ dropped: [], dropped_count: 0, dropped_truncated: false, node_count: 8, source_node_count: 8 });
    const { root } = mount({
      final,
      dropped: [],
      droppedBy: new Map(),
      droppedCount: 0,
      droppedTruncated: false,
      deltas: [{ index: 0, label: "1. Quick settings", before: 8, after: 8 }],
      steps: [STEPS[0]],
    });
    expect(withClass(root, "rec-chain-dropped")).toHaveLength(0);
    expect(textOf(root)).toContain("kept all");
  });

  it("names dropped nodes for a combination, which has no per-step cuts", () => {
    const dropped = [node("us-01"), node("us-02")];
    const { root } = mount({
      isCombination: true,
      steps: [{ type: "Regex Filter" }],
      deltas: [],
      dropped,
      droppedBy: new Map([
        ["us-01.example:443", "combination"],
        ["us-02.example:443", "combination"],
      ]),
      droppedCount: 2,
      droppedTruncated: false,
      final: preview({ dropped, dropped_count: 2, dropped_truncated: false, node_count: 5, source_node_count: 7 }),
    });
    expect(textOf(withClass(root, "rec-chain-dropped-head")[0])).toContain("Dropped by combination");
    expect(textOf(root)).toContain("us-01");
  });
});
