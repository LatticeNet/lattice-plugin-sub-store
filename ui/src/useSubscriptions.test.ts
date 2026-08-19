import { describe, expect, it } from "vitest";
import { ref } from "vue";
import type { BridgeClient } from "@latticenet/plugin-bridge";

import {
  FILE_TYPE_PLAIN,
  FILE_TYPE_SCRIPT,
  KIND_COLLECTION,
  KIND_FILE,
  MAX_SUBSCRIPTION_INLINE_BYTES,
  SOURCE_LOCAL,
  SOURCE_REMOTE,
  SOURCE_VPN_CORE,
  SOURCE_VPN_CORE_GRAPH,
  BINDINGS,
} from "./client";
import type { HostContext } from "./host";
import {
  argumentsToText,
  draftFromRecord,
  emptyDraft,
  enabledSteps,
  knownFileType,
  parseArguments,
  reconcileGraphDraftOptions,
  slugify,
  uniqueId,
  uniqueName,
  useSubscriptions,
  validateDraft,
} from "./useSubscriptions";

function subscriptionHost(responses: Record<string, unknown>, available: (method: string) => boolean = () => true) {
  const calls: { service: string; method: string; payload: unknown }[] = [];
  const bridge = {
    call(service: string, method: string, payload: unknown) {
      calls.push({ service, method, payload });
      const key = `${service}/${method}`;
      return { promise: Promise.resolve(responses[key]), cancel: () => {} };
    },
  } as unknown as BridgeClient;
  const host: HostContext = {
    bridge,
    init: ref(undefined),
    bootError: ref(""),
    available: (binding) => available(binding.method),
    resize: async () => {},
  };
  return { host, calls };
}

const GRAPH_OPTIONS = {
  schema_version: 1 as const,
  ok: true,
  options_version: `ov1:${"a".repeat(64)}`,
  identities: [
    { id: "identity-a", label: "Primary", status: "eligible", selectable: true },
    { id: "identity-b", label: "Secondary", status: "eligible", selectable: true },
  ],
  roots: [
    { line_uuid: "11111111-1111-4111-8111-111111111111", label: "Source A", source_node_id: "node-a", source: "managed", target_label: "Exit", status: "converged", path_summary: "Source A → Exit", eligible_identity_ids: ["identity-a"], selectable: true },
    { line_uuid: "22222222-2222-4222-8222-222222222222", label: "Source B", source_node_id: "node-b", source: "managed", status: "converged", path_summary: "Source B", eligible_identity_ids: ["identity-a", "identity-b"], selectable: true },
    { line_uuid: "33333333-3333-4333-8333-333333333333", label: "Secondary only", source_node_id: "node-c", source: "managed", status: "converged", path_summary: "Secondary only", eligible_identity_ids: ["identity-b"], selectable: true },
    { line_uuid: "44444444-4444-4444-8444-444444444444", label: "Drifted", source_node_id: "node-d", source: "managed", status: "drifted", path_summary: "Drifted", reason: "graph_drifted", eligible_identity_ids: [], selectable: false },
  ],
};

describe("subscription draft validation", () => {
  // The operator types a name; the id is derived from it. Asking for both was
  // asking for a detail with no decision attached.
  it("requires a name rather than an id", () => {
    expect(validateDraft({ ...emptyDraft(), source: SOURCE_VPN_CORE })).toMatch(/name/i);
    expect(validateDraft({ ...emptyDraft(), name: "Home", source: SOURCE_VPN_CORE })).toBe("");
  });

  // The fleet's own nodes need nothing supplied, which is the whole point of
  // that source: it is the one every Lattice deployment can use immediately.
  it("asks for nothing else when the source is this fleet", () => {
    expect(validateDraft({ ...emptyDraft(), name: "Fleet", source: SOURCE_VPN_CORE })).toBe("");
  });

  it("requires an authoritative identity and ordered roots for a graph source", () => {
    const draft = { ...emptyDraft(), name: "Graph", source: SOURCE_VPN_CORE_GRAPH };
    expect(validateDraft(draft)).toMatch(/identity/i);
    draft.vpnIdentity = "identity-a";
    expect(validateDraft(draft)).toMatch(/root/i);
    draft.entryRoots = ["11111111-1111-4111-8111-111111111111"];
    expect(validateDraft(draft)).toMatch(/reload/i);
    draft.optionsVersion = "ov1:" + "a".repeat(64);
    expect(validateDraft(draft)).toBe("");
    draft.entryRoots.push(draft.entryRoots[0]);
    expect(validateDraft(draft)).toMatch(/unique/i);
  });

  it("asks for a link when the source is a provider", () => {
    const draft = { ...emptyDraft(), name: "P", source: SOURCE_REMOTE };
    expect(validateDraft(draft)).toMatch(/link/i);
    expect(validateDraft({ ...draft, url: "https://example.invalid/sub" })).toBe("");
  });

  it("asks for nodes when the source is a paste", () => {
    const draft = { ...emptyDraft(), name: "M", source: SOURCE_LOCAL };
    expect(validateDraft(draft)).toMatch(/nodes/i);
    expect(validateDraft({ ...draft, content: "vless://x" })).toBe("");
  });

  it("asks a combination for members", () => {
    const draft = { ...emptyDraft(), name: "All", kind: KIND_COLLECTION };
    expect(validateDraft(draft)).toMatch(/at least one|subscription|tag/i);
    expect(validateDraft({ ...draft, members: ["a"] })).toBe("");
    expect(validateDraft({ ...draft, memberTags: ["home"] })).toBe("");
  });

  /** The backend limit is bytes. Measuring characters would let a subscription
   *  of non-ASCII names pass here and fail there. */
  it("measures the paste cap in bytes, not characters", () => {
    const multibyte = "\u7bc0".repeat(MAX_SUBSCRIPTION_INLINE_BYTES / 3 + 1);
    expect(multibyte.length).toBeLessThan(MAX_SUBSCRIPTION_INLINE_BYTES);
    expect(
      validateDraft({ ...emptyDraft(), name: "M", source: SOURCE_LOCAL, content: multibyte }),
    ).toMatch(/limit/i);
  });

  it("accepts a paste at the cap", () => {
    const atCap = "x".repeat(MAX_SUBSCRIPTION_INLINE_BYTES);
    expect(
      validateDraft({ ...emptyDraft(), name: "M", source: SOURCE_LOCAL, content: atCap }),
    ).toBe("");
  });
});

describe("derived identity", () => {
  it("slugifies a name into a usable key", () => {
    expect(slugify("Home nodes")).toBe("home-nodes");
    expect(slugify("  Tokyo / 東京  ")).toBe("tokyo");
    expect(slugify("!!!")).toBe("subscription");
  });

  // Renaming must not collide with an existing record, and must not silently
  // overwrite one — two subscriptions sharing a key would lose data.
  it("suffixes until the key is free", () => {
    expect(uniqueId("Home", [])).toBe("home");
    expect(uniqueId("Home", ["home"])).toBe("home-2");
    expect(uniqueId("Home", ["home", "home-2"])).toBe("home-3");
  });
});

describe("display name", () => {
  // Every list already preferred display_name and nothing could set it, so an
  // imported record showed a name its operator could not change.
  it("round-trips through the draft", () => {
    const draft = draftFromRecord({ id: "s1", name: "long-technical-name", display_name: "Home" });
    expect(draft.displayName).toBe("Home");
  });

  it("defaults to empty rather than to the name", () => {
    expect(draftFromRecord({ id: "s1", name: "n" }).displayName).toBe("");
  });
});

describe("draftFromRecord", () => {
  it("fills every editable field and never leaves undefined in the form", () => {
    const draft = draftFromRecord({ id: "s1", name: "n" });
    expect(draft).toEqual({
      id: "s1",
      name: "n",
      displayName: "",
      source: "",
      vpnIdentity: "",
      entryRoots: [],
      optionsVersion: "",
      url: "",
      content: "",
      ua: "",
      target: "",
      kind: "sub",
      remark: "",
      tags: [],
      members: [],
      memberTags: [],
      failureMode: "strict",
      fileType: "config",
      nodeSource: "",
      download: false,
      queryParams: [],
      argumentsText: "",
      process: [],
    });
  });

  it("copies the process chain rather than aliasing it", () => {
    const record = { id: "s1", name: "n", process: [{ type: "Flag Operator" }] };
    const draft = draftFromRecord(record);
    draft.process.push({ type: "another" });
    expect(record.process).toHaveLength(1);
  });

  it("preserves legacy operators when process is absent", () => {
    const draft = draftFromRecord({ id: "legacy", name: "Legacy", operators: [{ type: "Flag Operator" }] });
    expect(draft.process).toEqual([{ type: "Flag Operator" }]);
  });

  it("falls back to legacy operators when process is present but empty", () => {
    const draft = draftFromRecord({ id: "legacy-empty", name: "Legacy", process: [], operators: [{ type: "Flag Operator" }] });
    expect(draft.process).toEqual([{ type: "Flag Operator" }]);
  });

  // A disabled step is stored and shown, but must never reach the engine — a
  // preview that ran it would describe a pipeline the operator switched off.
  it("drops disabled steps from what would run", () => {
    const draft = {
      ...emptyDraft(),
      process: [{ type: "Useless Filter" }, { type: "Flag Operator", disabled: true }],
    };
    expect(enabledSteps(draft)).toHaveLength(1);
  });
});

describe("file drafts", () => {
  function fileDraft(over: Partial<ReturnType<typeof emptyDraft>> = {}) {
    return { ...emptyDraft(), kind: KIND_FILE, name: "Phone config", ...over };
  }

  // A file with no document has nothing to serve, and the failure would only
  // surface as an empty response with no clue why.
  it("insists on a document, naming which kind is missing", () => {
    expect(validateDraft(fileDraft())).toContain("configuration");
    expect(validateDraft(fileDraft({ fileType: FILE_TYPE_PLAIN }))).toContain("text");
  });

  it("accepts a config with a template and no node source", () => {
    // A rules fragment the operator maintains by hand is a legitimate file.
    expect(validateDraft(fileDraft({ content: "rules:\n  - MATCH,DIRECT\n" }))).toBe("");
  });

  it("asks for the link when the template is fetched", () => {
    expect(validateDraft(fileDraft({ source: SOURCE_REMOTE }))).toContain("link");
    expect(validateDraft(fileDraft({ source: SOURCE_REMOTE, url: "https://e.invalid/t" }))).toBe("");
  });

  // The backend limit is bytes, and a client configuration is the likeliest
  // thing in this plugin to reach it.
  it("applies the size limit to a file, not only to pasted nodes", () => {
    const tooBig = "x".repeat(MAX_SUBSCRIPTION_INLINE_BYTES + 1);
    expect(validateDraft(fileDraft({ content: tooBig }))).toContain("limit");
  });

  it("reads the stored kind and type back into the draft", () => {
    const draft = draftFromRecord({
      id: "f1",
      kind: KIND_FILE,
      name: "Notes",
      file_type: FILE_TYPE_PLAIN,
      node_source: "everything",
    });
    expect(draft.kind).toBe(KIND_FILE);
    expect(draft.fileType).toBe(FILE_TYPE_PLAIN);
    expect(draft.nodeSource).toBe("everything");
  });

  // An unknown kind arriving from an older or newer bundle must render as
  // something, and a sub is the only kind whose editor works for any record.
  it("falls back to a sub for a kind it does not know", () => {
    expect(draftFromRecord({ id: "x", name: "x", kind: "something-new" }).kind).toBe("sub");
  });
});

describe("script files", () => {
  function scriptDraft(over: Partial<ReturnType<typeof emptyDraft>> = {}) {
    return { ...emptyDraft(), kind: KIND_FILE, fileType: FILE_TYPE_SCRIPT, name: "Generated", ...over };
  }

  it("asks for the program", () => {
    expect(validateDraft(scriptDraft())).toContain("script");
  });

  // A script that calls produceArtifact with nothing declared fails at request
  // time, in a log the operator is not reading.
  it("insists on a node source when the script asks for one", () => {
    const asks = scriptDraft({ content: "const p = await produceArtifact({name: 'x'}); $content = '';" });
    expect(validateDraft(asks)).toContain("nodes");
    expect(validateDraft({ ...asks, nodeSource: "everything" })).toBe("");
  });

  it("lets a script that needs no nodes save without a source", () => {
    expect(validateDraft(scriptDraft({ content: "$content = 'MATCH,DIRECT';" }))).toBe("");
  });

  it("reads the script type and its settings back", () => {
    const draft = draftFromRecord({
      id: "gen",
      kind: KIND_FILE,
      name: "Generated",
      file_type: FILE_TYPE_SCRIPT,
      query_params: ["enhanced-mode"],
      arguments: { "enhanced-mode": "fake-ip" },
    });
    expect(draft.fileType).toBe(FILE_TYPE_SCRIPT);
    expect(draft.queryParams).toEqual(["enhanced-mode"]);
    expect(draft.argumentsText).toBe("enhanced-mode = fake-ip");
  });

  it("round-trips settings through the text block", () => {
    expect(parseArguments("a = 1\n# comment\n\nb=2")).toEqual({ a: "1", b: "2" });
    expect(parseArguments(argumentsToText({ x: "y" }))).toEqual({ x: "y" });
  });

  // An unknown type from an older or newer bundle must still render.
  it("falls back to a config for a type it does not know", () => {
    expect(knownFileType("something-new")).toBe("config");
  });
});

describe("copying a record", () => {
  // Copying twice produced two rows reading "Home nodes copy". The id that
  // distinguishes them is not shown anywhere, so the list becomes unusable.
  it("gives each copy a name no other record is using", () => {
    expect(uniqueName("Home copy", [])).toBe("Home copy");
    expect(uniqueName("Home copy", ["Home copy"])).toBe("Home copy 2");
    expect(uniqueName("Home copy", ["Home copy", "Home copy 2"])).toBe("Home copy 3");
  });

  it("leaves an unrelated name alone", () => {
    expect(uniqueName("Office", ["Home copy"])).toBe("Office");
  });
});

describe("vpn-core graph workflow", () => {
  it("keeps a stored stale selection unchanged until the operator explicitly adopts options", () => {
    const draft = { ...emptyDraft(), source: SOURCE_VPN_CORE_GRAPH, vpnIdentity: "identity-a", entryRoots: [GRAPH_OPTIONS.roots[0].line_uuid], optionsVersion: `ov1:${"b".repeat(64)}` };
    const original = structuredClone(draft);
    expect(reconcileGraphDraftOptions(draft, GRAPH_OPTIONS, false)).toEqual({ stale: true, removedRoots: 0 });
    expect(draft).toEqual(original);
    expect(reconcileGraphDraftOptions(draft, GRAPH_OPTIONS, true)).toEqual({ stale: true, removedRoots: 0 });
    expect(draft.optionsVersion).toBe(GRAPH_OPTIONS.options_version);
  });
  it("loads and clones secret-free authoritative options", async () => {
    const key = `${BINDINGS.subGraphOptions.service}/${BINDINGS.subGraphOptions.method}`;
    const { host, calls } = subscriptionHost({ [key]: GRAPH_OPTIONS });
    const subs = useSubscriptions(host);
    expect(await subs.loadGraphOptions()).toBe(true);
    expect(calls).toEqual([{ service: BINDINGS.subGraphOptions.service, method: "graph_options", payload: {} }]);
    expect(subs.graphOptions.value?.roots[3].reason).toBe("graph_drifted");
    GRAPH_OPTIONS.roots[0].label = "mutated after response";
    expect(subs.graphOptions.value?.roots[0].label).toBe("Source A");
    GRAPH_OPTIONS.roots[0].label = "Source A";
  });

  it("saves the same explicit root order and options version", async () => {
    const saveKey = `${BINDINGS.subSave.service}/${BINDINGS.subSave.method}`;
    const listKey = `${BINDINGS.subList.service}/${BINDINGS.subList.method}`;
    const optionsKey = `${BINDINGS.subGraphOptions.service}/${BINDINGS.subGraphOptions.method}`;
    const { host, calls } = subscriptionHost({
      [optionsKey]: GRAPH_OPTIONS,
      [saveKey]: { saved: true, subscription: {} },
      [listKey]: { subscriptions: [] },
    });
    const subs = useSubscriptions(host);
    expect(await subs.loadGraphOptions()).toBe(true);
    const draft = {
      ...emptyDraft(),
      id: "graph",
      name: "Graph",
      source: SOURCE_VPN_CORE_GRAPH,
      vpnIdentity: "identity-a",
      entryRoots: [GRAPH_OPTIONS.roots[1].line_uuid, GRAPH_OPTIONS.roots[0].line_uuid],
      optionsVersion: GRAPH_OPTIONS.options_version,
    };
    expect(await subs.save(draft)).toBe(true);
    const saved = (calls[1].payload as { subscription: Record<string, unknown> }).subscription;
    expect(saved.vpn_identity).toBe("identity-a");
    expect(saved.entry_roots).toEqual([GRAPH_OPTIONS.roots[1].line_uuid, GRAPH_OPTIONS.roots[0].line_uuid]);
    expect(saved.graph_options_version).toBe(GRAPH_OPTIONS.options_version);
  });

  it("previews an unsaved graph from the exact draft selection without raw credentials", async () => {
    const previewKey = `${BINDINGS.subPreview.service}/${BINDINGS.subPreview.method}`;
    const sourceVersion = `sv1:${"c".repeat(64)}`;
    const { host, calls } = subscriptionHost({ [previewKey]: { source_node_count: 2, node_count: 2, nodes: [{ name: "B" }, { name: "A" }], source_version: sourceVersion, stale: false } });
    const subs = useSubscriptions(host);
    const draft = {
      ...emptyDraft(),
      name: "Graph",
      source: SOURCE_VPN_CORE_GRAPH,
      vpnIdentity: "identity-a",
      entryRoots: [GRAPH_OPTIONS.roots[1].line_uuid, GRAPH_OPTIONS.roots[0].line_uuid],
      optionsVersion: GRAPH_OPTIONS.options_version,
      content: "vless://must-not-be-sent",
    };
    await subs.runPreview(draft);
    expect(calls).toHaveLength(1);
    expect(calls[0].payload).toEqual({
      subscription_id: "",
      raw: undefined,
      target: "",
      operators: [],
      graph_selection: {
        schema_version: 1,
        options_version: GRAPH_OPTIONS.options_version,
        identity_id: "identity-a",
        entry_roots: [GRAPH_OPTIONS.roots[1].line_uuid, GRAPH_OPTIONS.roots[0].line_uuid],
      },
    });
    expect(JSON.stringify(calls[0].payload)).not.toContain("must-not-be-sent");
    expect(subs.preview.value?.nodes.map((node) => node.name)).toEqual(["B", "A"]);
    expect(subs.preview.value).toMatchObject({ source_node_count: 2, node_count: 2, source_version: sourceVersion, stale: false });
  });

  it("sends an explicitly empty draft operator chain when previewing a saved graph", async () => {
    const previewKey = `${BINDINGS.subPreview.service}/${BINDINGS.subPreview.method}`;
    const { host, calls } = subscriptionHost({
      [previewKey]: { source_node_count: 1, node_count: 1, nodes: [{ name: "A" }] },
    });
    const subs = useSubscriptions(host);
    await subs.runPreview({
      ...emptyDraft(),
      id: "saved-graph",
      name: "Saved graph",
      source: SOURCE_VPN_CORE_GRAPH,
      vpnIdentity: "identity-a",
      entryRoots: [GRAPH_OPTIONS.roots[0].line_uuid],
      optionsVersion: GRAPH_OPTIONS.options_version,
      process: [],
    });

    expect(calls).toHaveLength(1);
    expect(calls[0].payload).toMatchObject({
      subscription_id: "saved-graph",
      operators: [],
    });
  });

  it("sends only enabled node-stage operators when previewing a saved mixed-stage graph", async () => {
    const previewKey = `${BINDINGS.subPreview.service}/${BINDINGS.subPreview.method}`;
    const { host, calls } = subscriptionHost({
      [previewKey]: { source_node_count: 1, node_count: 1, nodes: [{ name: "A" }] },
    });
    const subs = useSubscriptions(host);
    const enabledNode = { type: "Regex Filter", args: { regex: "keep" } };
    const disabledNode = { type: "Regex Rename", args: { regex: "old", replace: "new" }, disabled: true };
    const responseTransformer = {
      type: "Response Transformer",
      args: { mode: "script", content: "function transformFunction(res) { return res; }" },
    };
    await subs.runPreview({
      ...emptyDraft(),
      id: "saved-mixed-graph",
      name: "Saved mixed graph",
      source: SOURCE_VPN_CORE_GRAPH,
      vpnIdentity: "identity-a",
      entryRoots: [GRAPH_OPTIONS.roots[0].line_uuid],
      optionsVersion: GRAPH_OPTIONS.options_version,
      process: [enabledNode, disabledNode, responseTransformer],
    });

    expect(calls).toHaveLength(1);
    expect(calls[0].payload).toMatchObject({
      subscription_id: "saved-mixed-graph",
      operators: [enabledNode],
    });
  });

  it("cannot save a root bound only to another identity", async () => {
    const optionsKey = `${BINDINGS.subGraphOptions.service}/${BINDINGS.subGraphOptions.method}`;
    const { host, calls } = subscriptionHost({ [optionsKey]: GRAPH_OPTIONS });
    const subs = useSubscriptions(host);
    expect(await subs.loadGraphOptions()).toBe(true);
    const saved = await subs.save({ ...emptyDraft(), id: "graph", name: "Graph", source: SOURCE_VPN_CORE_GRAPH,
      vpnIdentity: "identity-a", entryRoots: [GRAPH_OPTIONS.roots[2].line_uuid], optionsVersion: GRAPH_OPTIONS.options_version });
    expect(saved).toBe(false);
    expect(subs.actionError.value).toMatch(/changed|reload/i);
    expect(calls).toHaveLength(1);
  });

  it("lets read-only operators load options but not save", async () => {
    const key = `${BINDINGS.subGraphOptions.service}/${BINDINGS.subGraphOptions.method}`;
    const { host, calls } = subscriptionHost({ [key]: GRAPH_OPTIONS }, (method) => method !== "save" && method !== "delete");
    const subs = useSubscriptions(host);
    expect(await subs.loadGraphOptions()).toBe(true);
    expect(subs.canMutate.value).toBe(false);
    expect(await subs.save({ ...emptyDraft(), name: "Graph", source: SOURCE_VPN_CORE_GRAPH, vpnIdentity: "identity-a", entryRoots: [GRAPH_OPTIONS.roots[0].line_uuid], optionsVersion: GRAPH_OPTIONS.options_version })).toBe(false);
    expect(calls).toHaveLength(1);
  });

  it("publishes only a saved id with the explicit reviewed target", async () => {
    const key = `${BINDINGS.subPublish.service}/${BINDINGS.subPublish.method}`;
    const { host, calls } = subscriptionHost({ [key]: { subscription_id: "graph", bytes: 321, status_code: 204 } });
    const subs = useSubscriptions(host);
    expect(await subs.publish("", "https://destination.invalid/graph", "PUT", "plain")).toBe(false);
    expect(calls).toHaveLength(0);
    expect(await subs.publish("graph", "https://destination.invalid/graph", "patch", "sing-box")).toBe(true);
    expect(calls).toEqual([{ service: BINDINGS.subPublish.service, method: "publish", payload: {
      subscription_id: "graph", destination: "https://destination.invalid/graph", method: "PATCH", format: "sing-box",
    } }]);
    expect(subs.notice.value).toContain("321 bytes");
  });

  it("surfaces publish failure without mutating the saved definition or target", async () => {
    const key = `${BINDINGS.subPublish.service}/${BINDINGS.subPublish.method}`;
    const { host, calls } = subscriptionHost({ [key]: Promise.reject(new Error("HTTP 502 vless://credential")) });
    const subs = useSubscriptions(host);
    const before = structuredClone(GRAPH_OPTIONS);
    expect(await subs.publish("graph", "https://destination.invalid/graph", "PUT", "plain")).toBe(false);
    expect(calls).toHaveLength(1);
    expect(subs.actionError.value).toMatch(/publish failed/i);
    expect(subs.actionError.value).not.toContain("vless://");
    // The cause is kept, redacted: an operator needs to tell a refused
    // credential from an unreachable host, and neither is legible from a
    // single generic sentence.
    expect(subs.actionError.value).toContain("HTTP 502");
    expect(subs.actionError.value).toContain("[endpoint]");
    expect(GRAPH_OPTIONS).toEqual(before);
  });

  it("does not expose publish to a read-only bridge", async () => {
    const { host, calls } = subscriptionHost({}, (method) => method !== "publish");
    const subs = useSubscriptions(host);
    expect(subs.canPublish.value).toBe(false);
    expect(await subs.publish("graph", "https://destination.invalid/graph", "PUT", "plain")).toBe(false);
    expect(calls).toHaveLength(0);
  });
});

/**
 * The composable against a stubbed host. The screens bind these refs directly,
 * so what matters is the state transitions: what loads, what closes, and what
 * a late answer may not overwrite.
 */
function stubHost(handlers: Record<string, (payload: any) => unknown>): HostContext {
  return {
    bridge: {
      call<T>(_service: string, method: string, payload: unknown) {
        const handler = handlers[method];
        return {
          promise: handler
            ? Promise.resolve().then(() => handler(payload) as T)
            : Promise.reject(new Error(`no stub for ${method}`)),
          cancel: () => {},
        };
      },
    } as unknown as HostContext["bridge"],
    init: ref(undefined),
    bootError: ref(""),
    available: () => true,
    resize: async () => {},
  };
}

function tick(): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, 0));
}

describe("row quick preview", () => {
  it("shows the first five produced nodes and the full count", async () => {
    const host = stubHost({
      preview: () => ({
        nodes: [1, 2, 3, 4, 5, 6, 7].map((n) => ({ name: `n${n}`, type: "vless" })),
        node_count: 7,
      }),
    });
    const subs = useSubscriptions(host);
    await subs.toggleRowPreview("s1");
    expect(subs.rowPreview.value?.loading).toBe(false);
    expect(subs.rowPreview.value?.nodes).toHaveLength(5);
    expect(subs.rowPreview.value?.count).toBe(7);
  });

  it("closes on a second toggle of the same row", async () => {
    const host = stubHost({ preview: () => ({ nodes: [], node_count: 0 }) });
    const subs = useSubscriptions(host);
    await subs.toggleRowPreview("s1");
    expect(subs.rowPreview.value?.id).toBe("s1");
    await subs.toggleRowPreview("s1");
    expect(subs.rowPreview.value).toBeNull();
  });

  it("puts a preview failure on the row, not on the banner", async () => {
    const host = stubHost({
      preview: () => {
        throw new Error("engine exploded");
      },
    });
    const subs = useSubscriptions(host);
    await subs.toggleRowPreview("s1");
    expect(subs.rowPreview.value?.error).toContain("engine exploded");
    expect(subs.actionError.value).toBe("");
  });

  it("ignores an answer for a row the operator has left", async () => {
    const pending = new Map<string, (value: unknown) => void>();
    const host = stubHost({
      preview: (payload: any) =>
        new Promise((resolve) => pending.set(payload.subscription_id, resolve)),
    });
    const subs = useSubscriptions(host);
    void subs.toggleRowPreview("a");
    void subs.toggleRowPreview("b");
    // The stub answers in a microtask, so the requests only exist after a tick.
    await tick();
    pending.get("b")!({ nodes: [], node_count: 0 });
    await tick();
    expect(subs.rowPreview.value?.id).toBe("b");
    // a's answer lands last; it must not relabel the open panel.
    pending.get("a")!({ nodes: [{ name: "stale", type: "vless" }], node_count: 1 });
    await tick();
    expect(subs.rowPreview.value?.id).toBe("b");
    expect(subs.rowPreview.value?.nodes).toHaveLength(0);
  });
});

describe("refresh", () => {
  function listedRow(lists: number) {
    return {
      id: "s1",
      kind: "sub",
      name: "S",
      has_url: true,
      has_inline_content: false,
      step_count: 0,
      disabled_step_count: 0,
      imported: false,
      // The second listing is the post-refresh one: the bookkeeping has moved.
      ...(lists > 1
        ? { last_fetch_at: "2026-08-10T09:00:00Z", last_fetch_ok: true }
        : {}),
    };
  }

  it("reloads the list afterwards, so the row status moves", async () => {
    let lists = 0;
    let fetches = 0;
    const host = stubHost({
      list: () => {
        lists += 1;
        return { subscriptions: [listedRow(lists)] };
      },
      probe: () => {
        fetches += 1;
        return { subscription_id: "s1", bytes: 42, ok: true, stale: false };
      },
    });
    const subs = useSubscriptions(host);
    await subs.load();
    expect(subs.items.value[0]?.last_fetch_at).toBeUndefined();

    await subs.refresh("s1");
    expect(fetches).toBe(1);
    expect(lists).toBe(2);
    expect(subs.items.value[0]?.last_fetch_ok).toBe(true);
    expect(subs.notice.value).toContain("42 bytes");
  });

  // A failed refresh still reloads: the failure badge on the row is the whole
  // point of recording the outcome.
  it("reloads even when the fetch fails", async () => {
    let lists = 0;
    const host = stubHost({
      list: () => {
        lists += 1;
        return { subscriptions: [listedRow(lists)] };
      },
      probe: () => {
        throw new Error("provider returned status 503");
      },
    });
    const subs = useSubscriptions(host);
    await subs.load();
    await subs.refresh("s1");
    expect(lists).toBe(2);
    expect(subs.actionError.value).toContain("503");
  });
});
