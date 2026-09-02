import { readFileSync } from "node:fs";
import { compile, createRenderer, nextTick, ssrContextKey } from "vue";
import { compileScript, parse } from "vue/compiler-sfc";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import MaskedUrlInput from "./components/MaskedUrlInput.vue";
import { REVEAL_MS } from "./urlMask";

// Compiled here rather than by the build plugin: these run without a DOM,
// against a renderer that records nodes instead of creating them.
const source = readFileSync(new URL("./components/MaskedUrlInput.vue", import.meta.url), "utf8");
const descriptor = parse(source, { filename: "MaskedUrlInput.vue" }).descriptor;
if (!descriptor.template) throw new Error("MaskedUrlInput template is missing");
const bindings = compileScript(descriptor, { id: "masked-url-test" }).bindings;
(MaskedUrlInput as { render?: ReturnType<typeof compile> }).render = compile(descriptor.template.content, {
  bindingMetadata: bindings,
  prefixIdentifiers: true,
});

type HostNode = { type: string; props: Record<string, unknown>; children: HostNode[]; text?: string; parent?: HostNode };

function renderer() {
  return createRenderer<HostNode, HostNode>({
    patchProp(node, key, _previous, value) { node.props[key] = value; },
    insert(child, parent, anchor) {
      child.parent = parent;
      const at = anchor ? parent.children.indexOf(anchor) : -1;
      if (at >= 0) parent.children.splice(at, 0, child);
      else parent.children.push(child);
    },
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
    insertStaticContent() { throw new Error("static content is not used by the masked field"); },
  });
}

function find(root: HostNode, type: string): HostNode[] {
  return [...(root.type === type ? [root] : []), ...root.children.flatMap((child) => find(child, type))];
}

const LINK = "https://sub.example-provider.com/api/v1/client/subscribe?token=9f8e7d6c&flag=clash";

function mount(modelValue: string) {
  const root: HostNode = { type: "root", props: {}, children: [] };
  const updates: string[] = [];
  const app = renderer().createApp(MaskedUrlInput, {
    modelValue,
    "onUpdate:modelValue": (value: string) => updates.push(value),
  });
  app.provide(ssrContextKey, { modules: new Set<string>() });
  app.mount(root);
  return { root, updates, input: () => find(root, "input")[0]!, button: () => find(root, "button")[0] };
}

describe("the provider link field", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it("prints the link masked after the host until it is edited", async () => {
    const { input, updates } = mount(LINK);
    expect(input().props.value).toBe("https://sub.example-provider.com/…?…");
    (input().props.onFocus as () => void)();
    await nextTick();
    expect(input().props.value).toBe(LINK);
    (input().props.onInput as (event: { target: { value: string } }) => void)({ target: { value: LINK + "&x=1" } });
    expect(updates).toEqual([LINK + "&x=1"]);
    (input().props.onBlur as () => void)();
    await nextTick();
    expect(input().props.value).toBe("https://sub.example-provider.com/…?…");
  });

  it("reveals for a minute on request and masks itself again", async () => {
    const { input, button } = mount(LINK);
    (button()!.props.onClick as () => void)();
    await nextTick();
    expect(input().props.value).toBe(LINK);
    expect(button()!.props["aria-pressed"]).toBe(true);
    vi.advanceTimersByTime(REVEAL_MS);
    await nextTick();
    expect(input().props.value).toBe("https://sub.example-provider.com/…?…");
  });

  it("offers no reveal for an empty field", () => {
    const { button, input } = mount("");
    expect(button()).toBeUndefined();
    expect(input().props.value).toBe("");
  });
});
