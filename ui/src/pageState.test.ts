import { afterEach, describe, expect, it, vi } from "vitest";

import { addressForState, pageStateFromAddress, stateRateLimit } from "../dev/consoleAddress";
import {
  MAX_STATE_VALUE,
  RESERVED_STATE_KEYS,
  canonicalState,
  createStateSender,
  decodeShellState,
  defaultShellState,
  encodeShellState,
  validPageState,
  type ShellState,
} from "./pageState";

function state(overrides: Partial<ShellState>): ShellState {
  return { ...defaultShellState(), ...overrides };
}

describe("the contract's rules", () => {
  it("accepts string values under lowercase keys", () => {
    expect(validPageState({ view: "files", published: "no", q_2: "" })).toEqual({ view: "files", published: "no", q_2: "" });
    expect(validPageState({})).toEqual({});
  });

  it("drops the whole state when any entry breaks a rule", () => {
    const seventeen = Object.fromEntries(Array.from({ length: 17 }, (_, i) => [`k${i}`, "x"]));
    expect(validPageState(seventeen)).toBeNull();
    expect(validPageState({ view: "files", View: "x" })).toBeNull();
    expect(validPageState({ view: "files", "1view": "x" })).toBeNull();
    expect(validPageState({ view: "files", _proto: "x" })).toBeNull();
    expect(validPageState({ ["a".repeat(25)]: "x" })).toBeNull();
    expect(validPageState({ view: 3 })).toBeNull();
    expect(validPageState({ q: "x".repeat(MAX_STATE_VALUE + 1) })).toBeNull();
    expect(validPageState(null)).toBeNull();
    expect(validPageState(["view"])).toBeNull();
    expect(validPageState("view=files")).toBeNull();
  });

  it("allows sixteen keys and a 256-character value", () => {
    const sixteen = Object.fromEntries(Array.from({ length: 16 }, (_, i) => [`k${i}`, "x"]));
    expect(validPageState(sixteen)).toEqual(sixteen);
    expect(validPageState({ q: "x".repeat(MAX_STATE_VALUE) })).not.toBeNull();
  });
});

describe("the shell's state on the wire", () => {
  const cases: [string, ShellState, Record<string, string>][] = [
    ["the landing", defaultShellState(), {}],
    ["a record peeked from the map", state({ open: "imported-sub-jiancai" }), { open: "imported-sub-jiancai" }],
    [
      "Sources, searched, sorted, migrated only",
      state({ view: "sources", q: "建材", sort: "name", origin: "migrated" }),
      { view: "sources", q: "建材", sort: "name", origin: "migrated" },
    ],
    [
      "Files not published, scripts only, with a record open",
      state({ view: "files", published: "no", type: "script", open: "for-cdcd-loon" }),
      { view: "files", published: "no", type: "script", open: "for-cdcd-loon" },
    ],
    ["Shares that serve nothing", state({ view: "shares", link: "dead" }), { view: "shares", link: "dead" }],
    [
      "page 3 of Files, searched",
      state({ view: "files", q: "alice", page: 3 }),
      { view: "files", q: "alice", page: "3" },
    ],
    [
      "a record page opened from filtered Files",
      state({ view: "files", record: "for-cdcd-loon", from: "files", published: "no" }),
      { view: "files", record: "for-cdcd-loon", published: "no" },
    ],
    ["a record page that was the landing", state({ record: "imported-sub-jiancai" }), { record: "imported-sub-jiancai" }],
    ["Settings", state({ view: "settings" }), { view: "settings" }],
  ];

  for (const [name, shell, wire] of cases) {
    it(`round-trips ${name}`, () => {
      const encoded = encodeShellState(shell);
      expect(encoded).toEqual(wire);
      expect(validPageState(encoded)).toEqual(encoded);
      expect(decodeShellState(encoded)).toEqual(shell);
    });
  }

  it("round-trips through the console's address, the way a reload does", () => {
    const shell = state({ view: "files", published: "no", type: "config", origin: "local", q: "loon & stash", open: "for-cdcd-loon" });
    const address = addressForState("?fixture=production&theme=dark", encodeShellState(shell));
    expect(address).not.toBeNull();
    expect(new URLSearchParams(address!).get("fixture")).toBe("production");
    expect(decodeShellState(pageStateFromAddress(address!))).toEqual(shell);
  });

  it("carries only the facets the layer on screen reads", () => {
    const shell = state({ view: "sources", published: "no", type: "script", link: "dead", origin: "local" });
    expect(encodeShellState(shell)).toEqual({ view: "sources", origin: "local" });
    expect(encodeShellState(state({ view: "overview", q: "stale", origin: "local" }))).toEqual({});
  });

  it("carries the page only for Files, and never page 1", () => {
    expect(encodeShellState(state({ view: "files", page: 1 }))).toEqual({ view: "files" });
    expect(encodeShellState(state({ view: "sources", page: 3 }))).toEqual({ view: "sources" });
    expect(encodeShellState(state({ view: "files", record: "a", from: "files", page: 3 }))).toEqual({ view: "files", record: "a" });
    expect(decodeShellState({ view: "files", page: "0" }).page).toBe(1);
  });

  it("leaves out the default sort and a closed panel", () => {
    expect(encodeShellState(state({ view: "combinations", sort: "recent", open: "" }))).toEqual({ view: "combinations" });
  });

  it("does not carry the side panel under a record page", () => {
    expect(encodeShellState(state({ record: "a", from: "sources", open: "b" }))).toEqual({ view: "sources", record: "a" });
    expect(decodeShellState({ record: "a", open: "b" }).open).toBe("");
  });

  it("clips a long search so the message stays inside the rules", () => {
    const encoded = encodeShellState(state({ view: "sources", q: "x".repeat(400) }));
    expect(encoded.q).toHaveLength(MAX_STATE_VALUE);
    expect(validPageState(encoded)).not.toBeNull();
  });

  it("reads an unknown view or facet value as unset", () => {
    expect(decodeShellState({ view: "graph", sort: "size", published: "maybe", type: "yaml", link: "gone", origin: "x" })).toEqual(defaultShellState());
  });

  it("still lands the address this page used before its layers", () => {
    expect(decodeShellState({ lens: "subscriptions" }).view).toBe("sources");
    expect(decodeShellState({ lens: "files" }).view).toBe("files");
    expect(decodeShellState({ view: "shares", lens: "files" }).view).toBe("shares");
    expect(decodeShellState({ lens: "constructor" }).view).toBe("overview");
  });

  it("never uses a key the console reserves", () => {
    expect([...RESERVED_STATE_KEYS].sort()).toEqual(["code", "mfa", "next", "redirect", "sso_error", "state", "token", "totp_challenge"]);
    const everything = state({
      view: "files", open: "x", q: "x", sort: "name", published: "no", origin: "local", type: "script", link: "dead",
    });
    const keys = new Set<string>();
    for (const view of ["overview", "sources", "combinations", "files", "shares", "settings"] as const) {
      for (const key of Object.keys(encodeShellState({ ...everything, view }))) keys.add(key);
      for (const key of Object.keys(encodeShellState({ ...everything, view, record: "r", from: view }))) keys.add(key);
    }
    expect([...keys].sort()).toEqual(["link", "open", "origin", "published", "q", "record", "sort", "type", "view"]);
    for (const key of keys) expect(RESERVED_STATE_KEYS.has(key)).toBe(false);
  });

  it("canonicalises key order", () => {
    expect(canonicalState({ b: "2", a: "1" })).toBe(canonicalState({ a: "1", b: "2" }));
  });
});

describe("the sender", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it("sends the last state once the page has been still for the delay", () => {
    vi.useFakeTimers();
    const sent: Record<string, string>[] = [];
    const sender = createStateSender((value) => sent.push(value), 250);
    sender.push({ view: "sources", q: "j" });
    vi.advanceTimersByTime(100);
    sender.push({ view: "sources", q: "ji" });
    vi.advanceTimersByTime(100);
    sender.push({ view: "sources", q: "jia" });
    vi.advanceTimersByTime(249);
    expect(sent).toEqual([]);
    vi.advanceTimersByTime(1);
    expect(sent).toEqual([{ view: "sources", q: "jia" }]);
  });

  it("does not echo what the console already holds, nor repeat itself", () => {
    vi.useFakeTimers();
    const sent: Record<string, string>[] = [];
    const sender = createStateSender((value) => sent.push(value), 250);
    sender.seed({ view: "files", published: "no" });
    sender.push({ published: "no", view: "files" });
    vi.advanceTimersByTime(250);
    expect(sent).toEqual([]);
    sender.push({ view: "shares" });
    vi.advanceTimersByTime(250);
    sender.push({ view: "shares" });
    vi.advanceTimersByTime(250);
    expect(sent).toEqual([{ view: "shares" }]);
  });

  it("sends an empty state, which clears the console's query", () => {
    vi.useFakeTimers();
    const sent: Record<string, string>[] = [];
    const sender = createStateSender((value) => sent.push(value), 250);
    sender.seed({ view: "files" });
    sender.push({});
    vi.advanceTimersByTime(250);
    expect(sent).toEqual([{}]);
  });

  it("sends nothing after dispose", () => {
    vi.useFakeTimers();
    const sent: Record<string, string>[] = [];
    const sender = createStateSender((value) => sent.push(value), 250);
    sender.push({ view: "files" });
    sender.dispose();
    vi.advanceTimersByTime(1000);
    expect(sent).toEqual([]);
  });
});

// The page state in init and the outbound message are the bridge client's
// (HostInit.pageState, BridgeClient.sendState) and are tested there.

describe("the harness console", () => {
  it("hands over its query without the harness switches, one key at a time", () => {
    expect(pageStateFromAddress("?fixture=large&theme=dark&state=empty&conflict=1&view=files&published=no")).toEqual({ view: "files", published: "no" });
    expect(pageStateFromAddress("?view=files&View=x&q=" + "x".repeat(300))).toEqual({ view: "files" });
    expect(pageStateFromAddress("?view=files&view=shares&open=x")).toEqual({ open: "x" });
    expect(pageStateFromAddress("?view=files&redirect=%2Fhome&next=a&code=1&token=t&sso_error=e&totp_challenge=c&mfa=1")).toEqual({ view: "files" });
    expect(pageStateFromAddress("?constructor=x")).toEqual({ constructor: "x" });
    expect(pageStateFromAddress("")).toEqual({});
  });

  it("writes a state message over the query, keeping the harness switches", () => {
    expect(addressForState("?fixture=large&view=files&published=no", { view: "shares", link: "dead" })).toBe("?fixture=large&view=shares&link=dead");
    expect(addressForState("?fixture=large&view=files", {})).toBe("?fixture=large");
    expect(addressForState("?view=files", {})).toBe("");
  });

  it("keeps the console's reserved keys and never takes one from the page", () => {
    expect(addressForState("?redirect=%2Fhome&view=files", { view: "shares", token: "t" })).toBe("?redirect=%2Fhome&view=shares");
  });

  it("drops a message that breaks the rules", () => {
    expect(addressForState("?view=files", { View: "x" })).toBeNull();
    expect(addressForState("?view=files", { view: 1 })).toBeNull();
  });

  it("allows sixty state messages a minute", () => {
    let now = 0;
    const allow = stateRateLimit(() => now);
    const first = Array.from({ length: 61 }, () => allow());
    expect(first.filter(Boolean)).toHaveLength(60);
    now = 59_999;
    expect(allow()).toBe(false);
    now = 60_000;
    expect(allow()).toBe(true);
  });
});
