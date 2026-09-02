import { readFileSync } from "node:fs";
import { compile, createRenderer, nextTick, ssrContextKey } from "vue";
import { compileScript, parse } from "vue/compiler-sfc";
import { describe, expect, it, vi } from "vitest";

import SubscriptionPreviewSummary from "./components/SubscriptionPreviewSummary.vue";
import type { StepDelta } from "./chainExplain";
import type { SubscriptionPreviewNode, SubscriptionPreviewResponse } from "./client";

// Templates compiled here rather than taken from the build plugin, the way the
// other component tests do it: these run without a DOM, against a renderer that
// records nodes instead of creating them.
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

useTemplate(SubscriptionPreviewSummary, "SubscriptionPreviewSummary.vue", "preview-summary-test");

vi.mock("@lucide/vue", async () => {
  const { defineComponent, h: create } = await import("vue");
  const icon = (name: string) =>
    defineComponent({ name, inheritAttrs: false, setup: () => () => create("svg", { "data-icon": name }) });
  return { ChevronLeft: icon("ChevronLeft"), ChevronRight: icon("ChevronRight") };
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
    insertStaticContent() { throw new Error("static content is not used by the compare panel"); },
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
  return find(root, (node) => String(node.props.class ?? "").split(/\s+/).includes(name));
}

function node(name: string, extra: Partial<SubscriptionPreviewNode> = {}): SubscriptionPreviewNode {
  return { name, type: "vless", server: `${name}.example`, port: "443", ...extra };
}

function mount(preview: SubscriptionPreviewResponse, extra: Record<string, unknown> = {}) {
  const root: HostNode = { type: "root", props: {}, style: {}, children: [] };
  const app = renderer().createApp(SubscriptionPreviewSummary, { preview, ...extra });
  app.provide(ssrContextKey, { modules: new Set<string>() });
  app.mount(root);
  return { root, app };
}

const KEPT = [node("hk-01"), node("hk-02")];

describe("the compare panel sets source nodes beside what the chain made of them", () => {
  it("shows every node kept beside itself when the chain removed nothing", () => {
    const { root } = mount({ nodes: KEPT, node_count: 2, source_node_count: 2 });
    expect(textOf(root)).toContain("2 node(s)");
    const rows = find(root, (n) => n.type === "tr").slice(1);
    expect(rows).toHaveLength(2);
    expect(withClass(root, "is-dropped")).toHaveLength(0);
    expect(textOf(withClass(root, "compare-count")[0])).toBe("2");
    expect(textOf(withClass(root, "compare-count")[1])).toBe("2");
  });

  it("lists a removed node on the source side with the operation that removed it", () => {
    const { root } = mount(
      {
        nodes: KEPT,
        node_count: 2,
        source_node_count: 3,
        dropped: [node("jp-01")],
        dropped_count: 1,
      },
      { droppedBy: new Map([["jp-01.example:443", "1. Region filter"]]) },
    );
    expect(textOf(root)).toContain("kept 2 of 3 nodes");
    const dropped = withClass(root, "is-dropped");
    expect(dropped).toHaveLength(1);
    expect(textOf(dropped[0])).toContain("jp-01");
    expect(textOf(dropped[0])).toContain("removed by 1. Region filter");
    expect(textOf(withClass(root, "compare-count")[0])).toBe("3");
  });

  it("names the chain when it cannot say which operation removed a node", () => {
    const { root } = mount({ nodes: KEPT, node_count: 2, source_node_count: 3, dropped: [node("jp-01")], dropped_count: 1 });
    expect(textOf(withClass(root, "is-dropped")[0])).toContain("removed by the chain");
  });

  it("reports every removal even when it can only name some of them", () => {
    const { root } = mount({
      nodes: KEPT,
      node_count: 2,
      source_node_count: 402,
      dropped: [node("jp-01"), node("jp-02")],
      dropped_count: 400,
      dropped_truncated: true,
    });
    expect(textOf(root)).toContain("Naming the first 2 of 400 removed");
  });

  // A rename is invisible in the result alone: the new name reads as the name
  // the node always had. The source column carries the old one.
  it("shows the name a renamed node used to have, on both sides", () => {
    const { root } = mount({
      nodes: [node("edge-01", { was: "hk-01" }), node("hk-02")],
      node_count: 2,
      source_node_count: 2,
    });
    const previous = withClass(root, "node-was");
    expect(previous).toHaveLength(1);
    expect(textOf(previous[0])).toContain("was hk-01");
    expect(textOf(withClass(root, "compare-source")[0])).toContain("hk-01");
    expect(textOf(withClass(root, "compare-result")[0])).toContain("edge-01");
  });

  it("prints the per-operation strip on top when the chain was explained", () => {
    const deltas: StepDelta[] = [
      { index: 0, label: "1. Region filter", before: 3, after: 2 },
      { index: 1, label: "2. Sort", before: 2, after: 2 },
    ];
    const { root } = mount({ nodes: KEPT, node_count: 2, source_node_count: 3 }, { deltas });
    const strip = withClass(root, "chain-deltas");
    expect(strip).toHaveLength(1);
    expect(textOf(strip[0])).toContain("1. Region filter: kept 2 of 3");
    expect(withClass(strip[0], "is-cut")).toHaveLength(1);
  });

  // The document is the only scroller, so a long set is paged to what fits.
  it("pages a long set instead of scrolling it", async () => {
    const nodes = Array.from({ length: 30 }, (_, i) => node(`n-${String(i).padStart(2, "0")}`));
    const { root } = mount({ nodes, node_count: 30, source_node_count: 30 }, { pageSize: 12 });
    expect(find(root, (n) => n.type === "tr").slice(1)).toHaveLength(12);
    expect(textOf(root)).toContain("Rows 1–12 of 30");
    const next = find(root, (n) => n.props["aria-label"] === "Next page")[0]!;
    (next.props.onClick as () => void)();
    await nextTick();
    expect(textOf(root)).toContain("Rows 13–24 of 30");
    expect(textOf(root)).toContain("n-12");
    expect(textOf(root)).not.toContain("n-00");
    (next.props.onClick as () => void)();
    await nextTick();
    expect(textOf(root)).toContain("Rows 25–30 of 30");
    expect(next.props.disabled).toBe(true);
  });
});
