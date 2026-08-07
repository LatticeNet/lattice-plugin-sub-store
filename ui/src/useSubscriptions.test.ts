import { describe, expect, it } from "vitest";

import {
  FILE_TYPE_PLAIN,
  KIND_COLLECTION,
  KIND_FILE,
  MAX_SUBSCRIPTION_INLINE_BYTES,
  SOURCE_LOCAL,
  SOURCE_REMOTE,
  SOURCE_VPN_CORE,
} from "./client";
import {
  draftFromRecord,
  emptyDraft,
  enabledSteps,
  slugify,
  uniqueId,
  validateDraft,
} from "./useSubscriptions";

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

describe("draftFromRecord", () => {
  it("fills every editable field and never leaves undefined in the form", () => {
    const draft = draftFromRecord({ id: "s1", name: "n" });
    expect(draft).toEqual({
      id: "s1",
      name: "n",
      source: "",
      vpnIdentity: "",
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
      process: [],
    });
  });

  it("copies the process chain rather than aliasing it", () => {
    const record = { id: "s1", name: "n", process: [{ type: "Flag Operator" }] };
    const draft = draftFromRecord(record);
    draft.process.push({ type: "another" });
    expect(record.process).toHaveLength(1);
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
