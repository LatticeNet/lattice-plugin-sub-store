import { readFileSync } from "node:fs";
import { compile, createRenderer, h, nextTick, ssrContextKey } from "vue";
import { compileScript, parse } from "vue/compiler-sfc";
import { beforeEach, describe, expect, it, vi } from "vitest";

import CodeEditor from "./components/CodeEditor.vue";

const editorSource = readFileSync(new URL("./components/CodeEditor.vue", import.meta.url), "utf8");
const editorDescriptor = parse(editorSource, { filename: "CodeEditor.vue" }).descriptor;
if (!editorDescriptor.template) throw new Error("CodeEditor template is missing");
const editorBindings = compileScript(editorDescriptor, { id: "code-editor-test" }).bindings;
(CodeEditor as typeof CodeEditor & { render: ReturnType<typeof compile> }).render = compile(
  editorDescriptor.template.content,
  { bindingMetadata: editorBindings, prefixIdentifiers: true },
);

const editorMock = vi.hoisted(() => ({ createEditor: vi.fn() }));
vi.mock("./codemirror", () => ({ createEditor: editorMock.createEditor }));

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
    insert(child, parent) { child.parent = parent; parent.children.push(child); },
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
    insertStaticContent() { throw new Error("static content is not used by CodeEditor"); },
  });
}

function find(node: HostNode, type: string): HostNode[] {
  return [...(node.type === type ? [node] : []), ...node.children.flatMap((child) => find(child, type))];
}

async function settleEditor(): Promise<void> {
  await Promise.resolve();
  await nextTick();
  await new Promise((resolve) => setTimeout(resolve, 0));
  await nextTick();
}

describe("CodeEditor enhancement and fallback", () => {
  beforeEach(() => {
    editorMock.createEditor.mockReset();
  });

  it("passes the read-only preview contract to the lazy editor", async () => {
    editorMock.createEditor.mockReturnValue({
      getValue: () => "proxies: []",
      setValue: vi.fn(),
      setLanguage: vi.fn(),
      destroy: vi.fn(),
    });
    const root: HostNode = { type: "root", props: {}, style: {}, children: [] };
    const app = renderer().createApp({
      render: () => h(CodeEditor, {
        modelValue: "proxies: []",
        language: "yaml",
        rows: 10,
        readonly: true,
        preview: true,
        ariaLabelledby: "preview-label",
      }),
    });
    app.provide(ssrContextKey, { modules: new Set<string>() });
    app.mount(root);
    await settleEditor();

    expect(editorMock.createEditor).toHaveBeenCalledOnce();
    expect(editorMock.createEditor).toHaveBeenCalledWith(expect.objectContaining({
      value: "proxies: []",
      language: "yaml",
      readonly: true,
      ariaLabelledby: "preview-label",
    }));
    expect(find(root, "textarea")).toHaveLength(0);
    expect(find(root, "p")).toHaveLength(0);
    app.unmount();
  });

  it("keeps the labelled plain-text view and reports a failed enhancement", async () => {
    editorMock.createEditor.mockImplementation(() => {
      throw new Error("chunk refused");
    });
    const root: HostNode = { type: "root", props: {}, style: {}, children: [] };
    const app = renderer().createApp({
      render: () => h(CodeEditor, {
        modelValue: "DOMAIN-SUFFIX,example.invalid,DIRECT",
        readonly: true,
        preview: true,
        ariaLabelledby: "fallback-label",
      }),
    });
    app.provide(ssrContextKey, { modules: new Set<string>() });
    app.mount(root);
    await settleEditor();

    const textarea = find(root, "textarea")[0]!;
    expect(textarea.props.readonly).toBe(true);
    expect(textarea.props["aria-labelledby"]).toBe("fallback-label");
    expect(textarea.props.value).toBe("DOMAIN-SUFFIX,example.invalid,DIRECT");
    const status = find(root, "p")[0]!;
    expect(status.props.role).toBe("status");
    expect(status.text).toContain("Plain-text view shown");
    app.unmount();
  });
});
