import { readFileSync } from "node:fs";
import { compile, createRenderer, defineComponent, h, nextTick, ref, ssrContextKey } from "vue";
import { compileScript, parse } from "vue/compiler-sfc";
import { describe, expect, it, vi } from "vitest";

import type { BridgeClient, HostInit } from "@latticenet/plugin-bridge";

import { BINDINGS, KIND_SUB, type SubscriptionListItem } from "./client";
import { provideHost, type HostContext } from "./host";

// The sheet's own behaviour is what these cover: which document reaches the
// output and when. How the viewer colours it is DocumentView's business and has
// its own tests.
vi.mock("./components/DocumentView.vue", async () => {
  const { defineComponent, h } = await import("vue");
  return {
    default: defineComponent({
      name: "DocumentViewStub",
      props: {
        text: { type: String, required: true },
        language: { type: String, default: "plain" },
        rows: { type: Number, default: 24 },
        ariaLabelledby: { type: String, default: undefined },
      },
      setup(props) {
        return () => h("output", {
          "data-document-view": "true",
          "data-language": props.language,
          "aria-labelledby": props.ariaLabelledby,
        }, props.text);
      },
    }),
  };
});

vi.mock("./components/lt/LtButton.vue", async () => {
  const { defineComponent, h } = await import("vue");
  return {
    default: defineComponent({
      name: "LtButtonStub",
      inheritAttrs: false,
      props: {
        disabled: { type: Boolean, default: false },
        variant: { type: String, default: "ghost" },
      },
      setup(props, { attrs, slots }) {
        return () => h("button", { ...attrs, disabled: props.disabled }, slots.default?.());
      },
    }),
  };
});

import TargetSheet from "./components/TargetSheet.vue";

const targetSheetSource = readFileSync(new URL("./components/TargetSheet.vue", import.meta.url), "utf8");
const targetSheetDescriptor = parse(targetSheetSource, { filename: "TargetSheet.vue" }).descriptor;
if (!targetSheetDescriptor.template) throw new Error("TargetSheet template is missing");
const targetSheetBindings = compileScript(targetSheetDescriptor, { id: "target-sheet-test" }).bindings;
(TargetSheet as typeof TargetSheet & { render: ReturnType<typeof compile> }).render = compile(
  targetSheetDescriptor.template.content,
  { bindingMetadata: targetSheetBindings, prefixIdentifiers: true },
);

type HostNode = {
  type: string;
  props: Record<string, unknown>;
  style: Record<string, string>;
  children: HostNode[];
  text?: string;
  parent?: HostNode;
  focus(): void;
  addEventListener(): void;
  removeEventListener(): void;
};

function hostNode(type: string, text?: string): HostNode {
  return {
    type,
    props: {},
    style: {},
    children: [],
    text,
    focus: vi.fn(),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
  };
}

function renderer() {
  return createRenderer<HostNode, HostNode>({
    patchProp(node, key, _previous, value) { node.props[key] = value; },
    insert(child, parent) { child.parent = parent; parent.children.push(child); },
    remove(child) {
      if (child.parent) child.parent.children = child.parent.children.filter((item) => item !== child);
    },
    createElement(type) { return hostNode(type); },
    createText(text) { return hostNode("#text", text); },
    createComment(text) { return hostNode("#comment", text); },
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
    insertStaticContent() { throw new Error("static content is not used by TargetSheet"); },
  });
}

function find(node: HostNode, predicate: (candidate: HostNode) => boolean): HostNode[] {
  return [
    ...(predicate(node) ? [node] : []),
    ...node.children.flatMap((child) => find(child, predicate)),
  ];
}

function textOf(node: HostNode): string {
  return [node.text ?? "", ...node.children.map(textOf)].join("");
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (cause: unknown) => void;
  const promise = new Promise<T>((yes, no) => {
    resolve = yes;
    reject = no;
  });
  return { promise, resolve, reject };
}

async function settle(): Promise<void> {
  await Promise.resolve();
  await nextTick();
  await new Promise((resolve) => setTimeout(resolve, 0));
  await nextTick();
}

function mountSheet(options: {
  canRender?: boolean;
  canListShares?: boolean;
  shareRejects?: boolean;
  renderThrows?: boolean;
} = {}) {
  const canRender = options.canRender ?? true;
  const canListShares = options.canListShares ?? true;
  const shareRejects = options.shareRejects ?? false;
  const renderThrows = options.renderThrows ?? false;
  const shareCalls: Array<{ service: string; method: string }> = [];
  const renderCalls: Array<{
    payload: Record<string, unknown>;
    result: ReturnType<typeof deferred<{ content: string; content_type: string }>>;
    cancel: ReturnType<typeof vi.fn>;
  }> = [];
  const previewCalls: Array<{
    payload: Record<string, unknown>;
    result: ReturnType<typeof deferred<{
      nodes: Array<{ name: string; type: string }>;
      node_count: number;
      source_node_count: number;
    }>>;
    cancel: ReturnType<typeof vi.fn>;
  }> = [];
  const bridge = {
    call(service: string, method: string, payload: unknown) {
      if (service === BINDINGS.sharesList.service && method === BINDINGS.sharesList.method) {
        shareCalls.push({ service, method });
        if (shareRejects) {
          return { promise: Promise.reject(new Error("share lookup failed")), cancel: vi.fn() };
        }
        return { promise: Promise.resolve({ shares: [] }), cancel: vi.fn() };
      }
      if (method === BINDINGS.subRender.method) {
        if (renderThrows) throw new Error("bridge rejected render synchronously");
        const call = {
          payload: payload as Record<string, unknown>,
          result: deferred<{ content: string; content_type: string }>(),
          cancel: vi.fn(),
        };
        renderCalls.push(call);
        return { promise: call.result.promise, cancel: call.cancel };
      }
      if (method === BINDINGS.subPreview.method) {
        const call = {
          payload: payload as Record<string, unknown>,
          result: deferred<{
            nodes: Array<{ name: string; type: string }>;
            node_count: number;
            source_node_count: number;
          }>(),
          cancel: vi.fn(),
        };
        previewCalls.push(call);
        return { promise: call.result.promise, cancel: call.cancel };
      }
      return { promise: Promise.reject(new Error(`unexpected ${service}/${method}`)), cancel: vi.fn() };
    },
  } as unknown as BridgeClient;
  const host: HostContext = {
    bridge,
    init: ref({} as HostInit),
    bootError: ref(""),
    available: (binding) => {
      if (binding.method === BINDINGS.subRender.method) return canRender;
      if (binding.method === BINDINGS.sharesList.method) return canListShares;
      return true;
    },
    resize: async () => {},
  };
  const record: SubscriptionListItem = {
    id: "openjobs-host",
    kind: KIND_SUB,
    name: "openjobs-host",
    has_url: true,
    has_inline_content: false,
    step_count: 1,
    disabled_step_count: 0,
    imported: false,
  };
  const root = hostNode("root");
  const app = renderer().createApp(defineComponent({
    setup() {
      provideHost(host);
      return () => h(TargetSheet, { open: true, record });
    },
  }));
  app.provide(ssrContextKey, { modules: new Set<string>() });
  app.mount(root);
  return { app, root, renderCalls, previewCalls, shareCalls };
}

describe("TargetSheet client output behavior", () => {
  it("renders the selected client document as soon as the workspace opens", async () => {
    const { app, root, renderCalls } = mountSheet();
    await settle();
    expect(renderCalls).toHaveLength(1);
    expect(renderCalls[0]!.payload).toMatchObject({
      subscription_id: "openjobs-host",
      target: "URI",
    });
    expect(textOf(root)).toContain("Generating Universal (URI) output");
    renderCalls[0]!.result.resolve({ content: "vless://node", content_type: "text/plain" });
    await settle();
    const output = find(root, (node) => node.props["data-document-view"] === "true")[0]!;
    expect(textOf(output)).toContain("vless://node");
    app.unmount();
  });

  it("cancels and ignores stale output when the client changes", async () => {
    const { app, root, renderCalls } = mountSheet();
    await settle();
    const stash = find(root, (node) => node.props.role === "radio" && textOf(node).includes("Stash"))[0]!;
    (stash.props.onClick as () => void)();
    await settle();
    expect(renderCalls).toHaveLength(2);
    expect(renderCalls[0]!.cancel).toHaveBeenCalledOnce();
    expect(renderCalls[1]!.payload).toMatchObject({ target: "Stash" });
    const rail = find(root, (node) => node.props.class === "evidence-rail")[0]!;
    expect(textOf(rail)).toContain("Stash");
    expect(textOf(rail)).toContain("YAML");
    renderCalls[0]!.result.resolve({ content: "old URI", content_type: "text/plain" });
    renderCalls[1]!.result.resolve({ content: "fresh Stash", content_type: "text/yaml" });
    await settle();
    const output = find(root, (node) => node.props["data-document-view"] === "true")[0]!;
    expect(textOf(output)).toContain("fresh Stash");
    expect(textOf(output)).not.toContain("old URI");
    app.unmount();
  });

  it("uses redacted pipeline nodes when document rendering is out of scope", async () => {
    const { app, root, renderCalls, previewCalls } = mountSheet({ canRender: false });
    await settle();
    expect(renderCalls).toHaveLength(0);
    expect(previewCalls).toHaveLength(1);
    expect(previewCalls[0]!.payload).toMatchObject({
      subscription_id: "openjobs-host",
      target: "URI",
    });
    previewCalls[0]!.result.resolve({
      nodes: [{ name: "Hong Kong 01", type: "vless" }],
      node_count: 1,
      source_node_count: 2,
    });
    await settle();
    const nodesTab = find(root, (node) => node.props.role === "tab" && textOf(node).includes("Pipeline nodes"))[0]!;
    expect(nodesTab.props["aria-selected"]).toBe(true);
    expect(find(root, (node) => node.props["data-document-view"] === "true")).toHaveLength(0);
    expect(textOf(root)).toContain("Hong Kong 01");
    app.unmount();
  });

  it("keeps the selected client and offers retry after a render failure", async () => {
    const { app, root, renderCalls } = mountSheet();
    await settle();
    renderCalls[0]!.result.reject(new Error("provider returned status 503"));
    await settle();
    expect(textOf(root)).toContain("provider returned status 503");
    const retry = find(root, (node) => node.type === "button" && textOf(node).includes("Retry render"))[0]!;
    (retry.props.onClick as () => void)();
    await settle();
    expect(renderCalls).toHaveLength(2);
    expect(renderCalls[1]!.payload).toMatchObject({ target: "URI" });
    app.unmount();
  });

  it("turns a synchronous bridge refusal into a recoverable render error", async () => {
    const { app, root } = mountSheet({ renderThrows: true });
    await settle();
    expect(textOf(root)).toContain("bridge rejected render synchronously");
    expect(textOf(root)).toContain("Retry render");
    app.unmount();
  });

  it("does not call share lookup when the host did not grant it", async () => {
    const { app, root, shareCalls } = mountSheet({ canListShares: false });
    await settle();
    expect(shareCalls).toHaveLength(0);
    expect(textOf(root)).toContain("Publication status requires admin access");
    app.unmount();
  });

  it("reports a granted share lookup failure separately from permission", async () => {
    const { app, root, shareCalls } = mountSheet({ shareRejects: true });
    await settle();
    expect(shareCalls).toHaveLength(1);
    expect(textOf(root)).toContain("Could not check publication status");
    expect(textOf(root)).not.toContain("requires admin access");
    app.unmount();
  });

  it("shows a valid empty render as an empty document instead of an error", async () => {
    const { app, root, renderCalls } = mountSheet();
    await settle();
    renderCalls[0]!.result.resolve({ content: "", content_type: "text/yaml" });
    await settle();
    expect(textOf(root)).toContain("The render completed with an empty document");
    expect(textOf(root)).not.toContain("Retry render");
    expect(find(root, (node) => node.props["data-document-view"] === "true")).toHaveLength(0);
    const copy = find(root, (node) => node.type === "button" && textOf(node).includes("Copy document"))[0]!;
    expect(copy.props.disabled).toBe(true);
    app.unmount();
  });
});
