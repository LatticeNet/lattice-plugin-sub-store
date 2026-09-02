import { describe, expect, it } from "vitest";
import { ref } from "vue";
import type { BridgeClient } from "@latticenet/plugin-bridge";

import {
  FILE_TYPE_PLAIN,
  FILE_TYPE_SCRIPT,
  KIND_COLLECTION,
  KIND_FILE,
  KIND_SUB,
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
  recordCatalogue,
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
      // The real bridge posts the payload to the host, which structured-clones
      // it. A Proxy cannot be cloned, and every object a Vue screen holds is
      // one, so a transport that hands the payload over by reference proves
      // nothing about whether the call can leave the frame at all.
      calls.push({ service, method, payload });
      structuredClone(payload);
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
  // overwrite one, two subscriptions sharing a key would lose data.
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

  // A disabled step is stored and shown, but must never reach the engine, a
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

describe("the record catalogue", () => {
  it("is one list per host, so two screens cannot disagree about it", async () => {
    const listKey = `${BINDINGS.subList.service}/${BINDINGS.subList.method}`;
    const { host, calls } = subscriptionHost({
      [listKey]: { subscriptions: [{ id: "a", name: "A", kind: KIND_SUB }] },
    });
    // Both screens call the hook, and both are kept alive.
    const first = useSubscriptions(host);
    const second = useSubscriptions(host);
    await first.load();
    expect(first.items.value).toHaveLength(1);
    expect(second.items.value, "the second screen holds its own stale copy").toEqual(first.items.value);
    expect(calls.filter((c) => c.method === BINDINGS.subList.method)).toHaveLength(1);
  });

  it("gives a different host its own list", async () => {
    const listKey = `${BINDINGS.subList.service}/${BINDINGS.subList.method}`;
    const one = subscriptionHost({ [listKey]: { subscriptions: [{ id: "a", name: "A", kind: KIND_SUB }] } });
    const two = subscriptionHost({ [listKey]: { subscriptions: [] } });
    const a = useSubscriptions(one.host);
    await a.load();
    const b = useSubscriptions(two.host);
    expect(b.items.value, "a second host inherited the first host's records").toEqual([]);
  });

  it("hands readers the records without the editing state", async () => {
    const listKey = `${BINDINGS.subList.service}/${BINDINGS.subList.method}`;
    const { host } = subscriptionHost({
      [listKey]: { subscriptions: [{ id: "a", name: "A", kind: KIND_SUB }] },
    });
    const subs = useSubscriptions(host);
    await subs.load();
    const catalogue = recordCatalogue(host);
    expect(catalogue.items.value).toEqual(subs.items.value);
    expect(Object.keys(catalogue)).toEqual(["state", "items", "loadError"]);
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

  // Naming a source is naming a host for the control plane to read, so it goes
  // to preview_draft (substore:admin). A preview of pasted or stored content
  // stays on preview (substore:read), which is what keeps a read-only operator
  // able to preview at all.
  it("routes a draft that names a source to the admin-scoped preview method", async () => {
    const draftKey = `${BINDINGS.subPreviewDraft.service}/${BINDINGS.subPreviewDraft.method}`;
    const { host, calls } = subscriptionHost({
      [draftKey]: { source_node_count: 1, node_count: 1, nodes: [{ name: "A" }] },
    });
    const subs = useSubscriptions(host);
    await subs.runPreview({ ...emptyDraft(), source: "provider", url: "https://provider.example/sub" });
    expect(calls).toHaveLength(1);
    expect(calls[0].method).toBe("preview_draft");
    expect(calls[0].payload).toMatchObject({ source: "provider", url: "https://provider.example/sub" });
  });

  // The screen holds its draft in a ref, so everything reachable from it is a
  // reactive Proxy, and a Proxy cannot be structured-cloned. A payload carrying
  // one never leaves the frame: postMessage throws before the host sees it.
  it("sends a payload the host transport can actually carry", async () => {
    const previewKey = `${BINDINGS.subPreview.service}/${BINDINGS.subPreview.method}`;
    const { host, calls } = subscriptionHost({
      [previewKey]: { source_node_count: 2, node_count: 1, nodes: [{ name: "A" }] },
    });
    const subs = useSubscriptions(host);
    // Exactly how SubscriptionsScreen holds it: a ref, unwrapped by the
    // template before it reaches this call.
    const draft = ref({
      ...emptyDraft(),
      content: "ss://example",
      process: [{ type: "Regex Filter", args: { regex: ["drop-me"], keep: false } }],
    });
    await subs.runPreview(draft.value);
    expect(calls).toHaveLength(1);
    expect(() => structuredClone(calls[0].payload)).not.toThrow();
  });

  // The Preview button lives in the pane beside the form. A refusal that only
  // reaches the action channel prints at the bottom of the form, so the pane
  // goes on saying nothing has run yet while the reason it has not sits
  // somewhere the click never looked.
  it("refuses a draft it cannot resolve where the preview would have been", async () => {
    const draftKey = `${BINDINGS.subPreviewDraft.service}/${BINDINGS.subPreviewDraft.method}`;
    const { host, calls } = subscriptionHost(
      { [draftKey]: { source_node_count: 1, node_count: 1, nodes: [{ name: "A" }] } },
      (method) => method !== "preview_draft",
    );
    const subs = useSubscriptions(host);
    await subs.runPreview({ ...emptyDraft(), source: "provider", url: "https://provider.example/sub" });
    expect(calls).toHaveLength(0);
    expect(subs.previewError.value).toContain("admin access");
    expect(subs.actionError.value).toContain("admin access");
    expect(subs.preview.value).toBeNull();
  });

  it("keeps a pasted-content preview on the read-scoped method", async () => {
    const previewKey = `${BINDINGS.subPreview.service}/${BINDINGS.subPreview.method}`;
    const { host, calls } = subscriptionHost({
      [previewKey]: { source_node_count: 1, node_count: 1, nodes: [{ name: "A" }] },
    });
    const subs = useSubscriptions(host);
    await subs.runPreview({ ...emptyDraft(), content: "ss://example" });
    expect(calls).toHaveLength(1);
    expect(calls[0].method).toBe("preview");
    expect(calls[0].payload).not.toHaveProperty("source", expect.anything());
  });

  it("keeps a saved local file preview on the read-scoped method", async () => {
    const previewKey = `${BINDINGS.subPreview.service}/${BINDINGS.subPreview.method}`;
    const { host, calls } = subscriptionHost({
      [previewKey]: {
        document: "DOMAIN-SUFFIX,example.invalid,DIRECT",
        node_count: 0,
      },
    }, (method) => method !== "preview_draft");
    const subs = useSubscriptions(host);
    await subs.runPreview({
      ...emptyDraft(),
      id: "rules",
      name: "Rules",
      kind: KIND_FILE,
      fileType: FILE_TYPE_PLAIN,
      source: SOURCE_LOCAL,
      content: "DOMAIN-SUFFIX,example.invalid,DIRECT",
      // Switching source changes the authority, but the form deliberately
      // keeps the other source's fields so switching back does not erase work.
      // None of that stale data may turn a local preview into host resolution.
      url: "https://provider.example/stale",
      ua: "Stale provider UA",
      vpnIdentity: "stale-vpn-identity",
    });
    expect(calls).toHaveLength(1);
    expect(calls[0].method).toBe("preview");
    expect(calls[0].payload).not.toHaveProperty("source", expect.anything());
    expect(subs.preview.value?.document).toContain("example.invalid");
    expect(subs.actionError.value).toBe("");
  });

  // A read-scoped operator never sees preview_draft, so the draft path must say
  // so instead of firing a call the server will refuse.
  it("explains rather than calls when the draft method is out of scope", async () => {
    const { host, calls } = subscriptionHost({}, (method) => method !== "preview_draft");
    const subs = useSubscriptions(host);
    await subs.runPreview({ ...emptyDraft(), source: "provider", url: "https://provider.example/sub" });
    expect(calls).toHaveLength(0);
    expect(subs.actionError.value).toContain("admin");
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

// A read-scoped session cannot ask the control plane to resolve a source a
// draft names. It can ask for a saved record's stored source, which the admin
// named when they saved it, so a draft whose source is unchanged previews
// through that path with its own chain.
describe("previewing a draft without admin access", () => {
  const stored = { id: "paid", name: "paid", source: SOURCE_REMOTE, url: "https://p.example/sub?token=1", ua: "Surge", process: [{ type: "Sort Operator" }] };
  const answers = {
    "latticenet.sub-store/subscription/get": { subscription: stored },
    "latticenet.sub-store/subscription/preview": { nodes: [{ name: "a", type: "ss" }], node_count: 1, source_node_count: 3 },
  };

  it("previews a saved record's stored source with the draft's operations", async () => {
    const { host, calls } = subscriptionHost(answers, (method) => method !== "preview_draft");
    const subs = useSubscriptions(host);
    const record = await subs.get("paid");
    const draft = draftFromRecord(record!);
    draft.process = [{ type: "Region Filter" }];
    await subs.runPreview(draft);
    const preview = calls.find((call) => call.method === "preview");
    expect(preview).toBeTruthy();
    expect(calls.some((call) => call.method === "preview_draft")).toBe(false);
    const payload = preview!.payload as Record<string, unknown>;
    expect(payload.subscription_id).toBe("paid");
    expect(payload.source).toBeUndefined();
    expect(payload.url).toBeUndefined();
    expect(payload.operators).toEqual([{ type: "Region Filter" }]);
    expect(subs.preview.value?.node_count).toBe(1);
    expect(subs.previewNote.value).toContain("Previewed from the saved source");
    expect(subs.previewError.value).toBe("");
  });

  it("refuses a changed source and says what the session can do instead", async () => {
    const { host, calls } = subscriptionHost(answers, (method) => method !== "preview_draft");
    const subs = useSubscriptions(host);
    const draft = draftFromRecord((await subs.get("paid"))!);
    draft.url = "https://other.example/sub?token=2";
    await subs.runPreview(draft);
    expect(calls.some((call) => call.method === "preview" || call.method === "preview_draft")).toBe(false);
    expect(subs.previewError.value).toContain("admin access");
    expect(subs.previewError.value).toContain("saved record's stored source");
    expect(subs.previewNote.value).toBe("");
  });

  it("still names the source on the admin method when the session has it", async () => {
    const { host, calls } = subscriptionHost({ ...answers, "latticenet.sub-store/subscription/preview_draft": answers["latticenet.sub-store/subscription/preview"] });
    const subs = useSubscriptions(host);
    const draft = draftFromRecord((await subs.get("paid"))!);
    await subs.runPreview(draft);
    const call = calls.find((entry) => entry.method === "preview_draft");
    expect((call?.payload as Record<string, unknown>).url).toBe(stored.url);
    expect(subs.previewNote.value).toBe("");
  });
});
