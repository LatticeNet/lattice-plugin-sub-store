import { describe, expect, it } from "vitest";

import { MAX_SUBSCRIPTION_INLINE_BYTES, SOURCE_VPN_CORE } from "./client";
import { draftFromRecord, emptyDraft, enabledSteps, validateDraft } from "./useSubscriptions";

describe("subscription draft validation", () => {
  it("requires an id", () => {
    expect(validateDraft({ ...emptyDraft(), url: "https://example.invalid" })).toMatch(/id is required/i);
  });

  it("rejects ids that would not survive a URL or a key", () => {
    for (const id of ["has space", "../escape", "sl/ash", "#hash"]) {
      expect(validateDraft({ ...emptyDraft(), id, url: "https://example.invalid" }), id).not.toBe("");
    }
  });

  it("accepts ordinary ids", () => {
    for (const id of ["home", "home-nodes", "home_nodes", "home.nodes", "a1"]) {
      expect(validateDraft({ ...emptyDraft(), id, url: "https://example.invalid" }), id).toBe("");
    }
  });

  /**
   * A subscription with neither source renders nothing, and the core turns a
   * render of nothing into a bodiless 404. Caught here, the operator gets a
   * sentence; caught there, they get a URL that silently does not work.
   */
  // A vpn-core subscription supplies its own content, so demanding a URL would
  // make the source unusable — which is exactly what blocked a fleet whose
  // nodes live in vpn-core from being served natively.
  it("does not demand a URL when the content comes from vpn-core", () => {
    expect(validateDraft({ ...emptyDraft(), id: "fleet", source: SOURCE_VPN_CORE })).toBe("");
  });

  it("requires either a provider URL or inline content", () => {
    expect(validateDraft({ ...emptyDraft(), id: "s1" })).toMatch(/provider URL or some inline content/i);
    expect(validateDraft({ ...emptyDraft(), id: "s1", url: "https://example.invalid" })).toBe("");
    expect(validateDraft({ ...emptyDraft(), id: "s1", content: "vless://x" })).toBe("");
  });

  /** The backend limit is bytes. Measuring characters would let a subscription
   *  of non-ASCII names pass here and fail there. */
  it("measures the inline cap in bytes, not characters", () => {
    const multibyte = "節".repeat(MAX_SUBSCRIPTION_INLINE_BYTES / 3 + 1); // 3 bytes each
    expect(multibyte.length).toBeLessThan(MAX_SUBSCRIPTION_INLINE_BYTES);
    expect(validateDraft({ ...emptyDraft(), id: "s1", content: multibyte })).toMatch(/limit/i);
  });

  it("accepts inline content at the cap", () => {
    const atCap = "x".repeat(MAX_SUBSCRIPTION_INLINE_BYTES);
    expect(validateDraft({ ...emptyDraft(), id: "s1", content: atCap })).toBe("");
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
