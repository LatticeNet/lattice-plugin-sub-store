import { readFileSync } from "node:fs";
import { compile, createRenderer, nextTick, ssrContextKey } from "vue";
import { compileScript, parse } from "vue/compiler-sfc";
import { describe, expect, it, vi } from "vitest";

import SubscriptionPreviewSummary from "./components/SubscriptionPreviewSummary.vue";
import NodeRows from "./components/NodeRows.vue";
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
useTemplate(NodeRows, "NodeRows.vue", "node-rows-test");

vi.mock("@lucide/vue", async () => {
  const { defineComponent, h: create } = await import("vue");
  const icon = (name: string) =>
    defineComponent({ name, inheritAttrs: false, setup: () => () => create("svg", { "data-icon": name }) });
  return { ChevronDown: icon("ChevronDown") };
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
    // The anchor matters here: toggling a group unmounts a fragment, and Vue
    // walks from its opening anchor to its closing one to find what to remove.
    // A renderer that appends everything reports the wrong siblings and the
    // fragment survives its own v-if.
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
    insertStaticContent() { throw new Error("static content is not used by the preview summary"); },
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
  return { name, type: "vless", server: "example.com", port: "443", ...extra };
}

function mount(preview: SubscriptionPreviewResponse) {
  const root: HostNode = { type: "root", props: {}, style: {}, children: [] };
  const app = renderer().createApp(SubscriptionPreviewSummary, { preview });
  app.provide(ssrContextKey, { modules: new Set<string>() });
  app.mount(root);
  return { root, app };
}

const KEPT = [node("hk-01"), node("hk-02", { server: "example.net" })];

describe("the preview pane compares what the chain kept against what it removed", () => {
  it("shows one plain list when the chain removed nothing", () => {
    const { root } = mount({ nodes: KEPT, node_count: 2, source_node_count: 2 });

    expect(withClass(root, "node-list")).toHaveLength(1);
    // No group chrome: labelling one group and an empty one is worse than not
    // labelling the only group there is.
    expect(withClass(root, "rec-group-head")).toHaveLength(0);
    expect(textOf(root)).toContain("2 node(s)");
  });

  it("splits kept from removed and counts both", () => {
    const { root } = mount({
      nodes: KEPT,
      node_count: 2,
      source_node_count: 3,
      dropped: [node("jp-01", { server: "dropped.example" })],
      dropped_count: 1,
    });

    expect(textOf(root)).toContain("kept 2 of 3 nodes");
    const heads = withClass(root, "rec-group-head");
    expect(heads).toHaveLength(2);
    expect(textOf(heads[0])).toContain("Kept");
    expect(textOf(heads[0])).toContain("2");
    expect(textOf(heads[1])).toContain("Removed by the chain");
    expect(textOf(heads[1])).toContain("1");
    // The removed node is named, not just counted.
    expect(textOf(root)).toContain("jp-01");
  });

  it("marks the removal count as the one worth noticing", () => {
    const { root } = mount({
      nodes: KEPT,
      node_count: 2,
      source_node_count: 3,
      dropped: [node("jp-01")],
      dropped_count: 1,
    });
    const counts = withClass(root, "rec-group-count");
    expect(counts).toHaveLength(2);
    expect(counts[0].props["data-tone"]).toBeUndefined();
    expect(counts[1].props["data-tone"]).toBe("danger");
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

    const heads = withClass(root, "rec-group-head");
    expect(textOf(heads[1])).toContain("400");
    expect(textOf(root)).toContain("Naming the first 2 of them");
  });

  // A rename is invisible in the result alone: the new name reads as the name
  // the node always had.
  it("shows the name a renamed node used to have", () => {
    const { root } = mount({
      nodes: [node("edge-01", { was: "hk-01" }), node("hk-02")],
      node_count: 2,
      source_node_count: 2,
    });

    const previous = withClass(root, "node-was");
    expect(previous).toHaveLength(1);
    expect(textOf(previous[0])).toContain("was hk-01");
  });

  it("folds a group away without losing its count", async () => {
    const { root } = mount({
      nodes: KEPT,
      node_count: 2,
      source_node_count: 3,
      dropped: [node("jp-01")],
      dropped_count: 1,
    });

    const removed = withClass(root, "rec-group-head")[1];
    expect(removed.props["aria-expanded"]).toBe(true);
    expect(textOf(root)).toContain("jp-01");

    (removed.props.onClick as () => void)();
    await nextTick();

    expect(removed.props["aria-expanded"]).toBe(false);
    expect(textOf(root)).not.toContain("jp-01");
    expect(textOf(withClass(root, "rec-group-head")[1])).toContain("1");
  });
});

