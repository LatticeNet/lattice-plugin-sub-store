import { readFileSync } from "node:fs";
import { createRenderer, createSSRApp, h, nextTick, ref, ssrContextKey } from "vue";
import { renderToString } from "@vue/server-renderer";
import { describe, expect, it } from "vitest";
import type { HostInit } from "@latticenet/plugin-bridge";

import GraphSubscriptionEditor from "./components/GraphSubscriptionEditor.vue";
import SubscriptionPreviewSummary from "./components/SubscriptionPreviewSummary.vue";
import SubscriptionPublishControl from "./components/SubscriptionPublishControl";
import SubscriptionsScreen from "./screens/SubscriptionsScreen.vue";
import { provideHost, type HostContext } from "./host";
import { emptyDraft } from "./useSubscriptions";
import { SOURCE_VPN_CORE_GRAPH, type GraphOptionsResponse } from "./client";

const screenSource = readFileSync(new URL("./screens/SubscriptionsScreen.vue", import.meta.url), "utf8");
const styles = readFileSync(new URL("./styles.css", import.meta.url), "utf8");
const rootA = "11111111-1111-4111-8111-111111111111";
const rootB = "22222222-2222-4222-8222-222222222222";
const options: GraphOptionsResponse = {
  schema_version: 1,
  ok: true,
  options_version: `ov1:${"a".repeat(64)}`,
  identities: [
    { id: "identity-a", label: "Primary", status: "eligible", selectable: true },
    { id: "identity-b", label: "Secondary", status: "eligible", selectable: true },
  ],
  roots: [
    { line_uuid: rootA, label: "Source A", source_node_id: "node-a", source: "managed", target_label: "Target A", status: "converged", path_summary: "Source A → Target A", eligible_identity_ids: ["identity-a"], selectable: true },
    { line_uuid: rootB, label: "Source B", source_node_id: "node-b", source: "managed", target_label: "Target B", status: "converged", path_summary: "Source B → Target B", eligible_identity_ids: ["identity-b"], selectable: true },
  ],
};

type HostNode = { type: string; props: Record<string, unknown>; children: HostNode[]; text?: string; parent?: HostNode };
function interactiveRenderer() {
  return createRenderer<HostNode, HostNode>({
    patchProp(node, key, _previous, value) { node.props[key] = value; },
    insert(child, parent) { child.parent = parent; parent.children.push(child); },
    remove(child) { if (child.parent) child.parent.children = child.parent.children.filter((item) => item !== child); },
    createElement(type) { return { type, props: {}, children: [] }; },
    createText(text) { return { type: "#text", props: {}, children: [], text }; },
    createComment(text) { return { type: "#comment", props: {}, children: [], text }; },
    setText(node, text) { node.text = text; },
    setElementText(node, text) { node.text = text; },
    parentNode(node) { return node.parent ?? null; },
    nextSibling(node) { const siblings = node.parent?.children ?? []; return siblings[siblings.indexOf(node) + 1] ?? null; },
    querySelector() { return null; },
    setScopeId() {},
    cloneNode(node) { return { ...node, props: { ...node.props }, children: [...node.children] }; },
    insertStaticContent() { throw new Error("static content is not used by this component"); },
  });
}

function findHost(node: HostNode, type: string): HostNode[] {
  return [...(node.type === type ? [node] : []), ...node.children.flatMap((child) => findHost(child, type))];
}

describe("vpn-core graph editor component contract", () => {
  it("SSR renders authoritative identity and root eligibility with ordered controls", async () => {
    const draft = { ...emptyDraft(), name: "Graph", source: SOURCE_VPN_CORE_GRAPH, vpnIdentity: "identity-a", entryRoots: [rootA], optionsVersion: options.options_version };
    const html = await renderToString(createSSRApp({
      render: () => h(GraphSubscriptionEditor, { draft, options, loading: false, readOnly: false }),
    }));

    expect(html).toContain("VPN identity");
    expect(html).toContain('<select class="select" value="identity-a">');
    expect(html).toContain("Source A");
    expect(html).toContain('aria-label="Selected graph roots"');
    expect(html).toContain('aria-label="Move Source A up"');
    expect(html).toContain('aria-label="Move Source A down"');
    expect(html).toContain('aria-label="Remove Source A"');
    expect(html).toContain("Source node-b · Target Target B · Status converged · Path Source B → Target B · Reason not eligible for the selected identity");
  });

  it("SSR renders explicit empty and read-only states without mutation affordances", async () => {
    const draft = { ...emptyDraft(), name: "Graph", source: SOURCE_VPN_CORE_GRAPH, vpnIdentity: "identity-a", entryRoots: [], optionsVersion: options.options_version };
    const html = await renderToString(createSSRApp({
      render: () => h(GraphSubscriptionEditor, { draft, options, loading: false, readOnly: true }),
    }));
    expect(html).toContain("No roots selected. Add at least one eligible source.");
    expect(html).toContain('role="status"');
    expect(html).toMatch(/<button[^>]+disabled[^>]*>.*Source A/s);
  });

  it("SSR renders the real SubscriptionsScreen with read-only actions disabled", async () => {
    const host: HostContext = {
      bridge: undefined,
      init: ref({} as HostInit),
      bootError: ref(""),
      available: (binding) => binding.method !== "save" && binding.method !== "delete",
      resize: async () => {},
    };
    const app = createSSRApp({
      setup() {
        provideHost(host);
        return () => h(SubscriptionsScreen);
      },
    });
    const html = await renderToString(app);
    expect(html).toContain("Subscriptions");
    expect(html).toMatch(/<button[^>]+disabled[^>]*>.*New/s);
  });

  it("SSR consumes the exact Go preview wire names and exposes fresh graph authority", async () => {
    const wire = JSON.parse(`{"source_node_count":2,"node_count":1,"nodes":[{"name":"A","type":"vless"}],"truncated":false,"source_version":"sv1:${"c".repeat(64)}","stale":false}`);
    const html = await renderToString(createSSRApp({ render: () => h(SubscriptionPreviewSummary, { preview: wire }) }));
    expect(html).toContain("1 node(s)<span> from 2</span>");
    expect(html).toContain(`Source sv1:${"c".repeat(64)} · fresh composition`);
    expect(html).not.toContain("undefined node");
  });

  it("keeps UUID free text out of the graph editor wiring", () => {
    expect(screenSource).toContain("<GraphSubscriptionEditor");
    expect(screenSource).not.toMatch(/<input[^>]+v-model="draft\.entryRoots/);
  });

  it("SSR renders saved-only publish review, read-only, and failure states without secrets", async () => {
    const saved = await renderToString(createSSRApp({ render: () => h(SubscriptionPublishControl, { saved: true, readOnly: false, busy: false }) }));
    expect(saved).toContain("Review publish target");
    expect(saved).toContain("Destination");
    expect(saved).toContain("Publish saved definition");
    const blocked = await renderToString(createSSRApp({ render: () => h(SubscriptionPublishControl, { saved: false, readOnly: true, busy: false, error: "Publish failed; definition unchanged." }) }));
    expect(blocked).toContain("Save this definition before publishing.");
    expect(blocked).toContain("Publish failed; definition unchanged.");
    expect(blocked).toMatch(/<input[^>]+disabled/);
    expect(blocked).toMatch(/<button[^>]+disabled[^>]*>Publish saved definition/);
    expect(blocked).not.toContain("vless://");
  });

  it("interactively enables publish after a destination and emits the exact reviewed target", async () => {
    const root: HostNode = { type: "root", props: {}, children: [] };
    const published: unknown[][] = [];
    const app = interactiveRenderer().createApp({ render: () => h(SubscriptionPublishControl, {
      saved: true, readOnly: false, busy: false, onPublish: (...args: unknown[]) => published.push(args),
    }) });
    app.provide(ssrContextKey, { modules: new Set<string>() });
    app.mount(root);
    const input = findHost(root, "input")[0];
    const button = findHost(root, "button")[0];
    expect(button.props.disabled).toBe(true);
    (input.props.onInput as (event: unknown) => void)({ target: { value: "https://destination.invalid/graph" } });
    await nextTick();
    expect(button.props.disabled).toBe(false);
    const form = findHost(root, "form")[0];
    (form.props.onSubmit as (event: { preventDefault(): void }) => void)({ preventDefault() {} });
    expect(published).toEqual([["https://destination.invalid/graph", "PUT", "plain"]]);
  });

  it("keeps the graph workflow responsive and reduced-motion safe", () => {
    expect(styles).toContain("@media (max-width: 560px)");
    expect(styles).toContain("@media (prefers-reduced-motion: reduce)");
    expect(styles).toContain(".graph-root-candidates");
  });
});
