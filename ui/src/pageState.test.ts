import { afterEach, describe, expect, it, vi } from "vitest";

import { addressForState, pageStateFromAddress, stateRateLimit } from "../dev/consoleAddress";
import {
  MAX_STATE_VALUE,
  PAGE_STATE_MESSAGE,
  canonicalState,
  createStateSender,
  decodeShellState,
  defaultShellState,
  encodeShellState,
  listenForInitPageState,
  stateMessage,
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

  it("spells the message the way the contract does", () => {
    expect(stateMessage("n".repeat(24), { view: "files" })).toEqual({ type: PAGE_STATE_MESSAGE, nonce: "n".repeat(24), state: { view: "files" } });
    expect(PAGE_STATE_MESSAGE).toBe("lattice.plugin.state");
  });
});

describe("the page state in init", () => {
  const NONCE = "nonce-0123456789abcdef";
  const ORIGIN = "https://console.example";

  function frame() {
    const parent = {};
    let listener: ((event: MessageEvent) => void) | undefined;
    const win = {
      parent,
      addEventListener: (_type: string, fn: (event: MessageEvent) => void) => {
        listener = fn;
      },
      removeEventListener: () => {
        listener = undefined;
      },
    } as unknown as Window;
    const deliver = (event: Partial<MessageEvent>) => listener?.(event as MessageEvent);
    return { win, parent, deliver, listening: () => !!listener };
  }

  const init = (extra: Record<string, unknown>) => ({ type: "lattice.host.init", nonce: NONCE, version: "1", ...extra });

  it("reads pageState from the host's init", () => {
    const { win, parent, deliver } = frame();
    const heard: Record<string, string>[] = [];
    listenForInitPageState(win, NONCE, ORIGIN, (value) => heard.push(value));
    deliver({ source: parent as Window, origin: ORIGIN, data: init({ pageState: { view: "files", open: "for-cdcd-loon" } }) });
    expect(heard).toEqual([{ view: "files", open: "for-cdcd-loon" }]);
  });

  it("reads an older console's init, or a state that breaks the rules, as empty", () => {
    const { win, parent, deliver } = frame();
    const heard: Record<string, string>[] = [];
    listenForInitPageState(win, NONCE, ORIGIN, (value) => heard.push(value));
    deliver({ source: parent as Window, origin: ORIGIN, data: init({}) });
    deliver({ source: parent as Window, origin: ORIGIN, data: init({ pageState: { View: "files" } }) });
    expect(heard).toEqual([{}, {}]);
  });

  it("ignores anything that is not the host's init to this frame", () => {
    const { win, parent, deliver } = frame();
    const heard: Record<string, string>[] = [];
    listenForInitPageState(win, NONCE, ORIGIN, (value) => heard.push(value));
    const pageState = { view: "files" };
    deliver({ source: {} as Window, origin: ORIGIN, data: init({ pageState }) });
    deliver({ source: parent as Window, origin: "https://evil.example", data: init({ pageState }) });
    deliver({ source: parent as Window, origin: ORIGIN, data: { ...init({ pageState }), nonce: "other-nonce-0123456789" } });
    deliver({ source: parent as Window, origin: ORIGIN, data: { ...init({ pageState }), type: "lattice.host.theme" } });
    deliver({ source: parent as Window, origin: ORIGIN, data: null });
    expect(heard).toEqual([]);
  });

  it("stops listening when asked", () => {
    const { win, listening } = frame();
    const stop = listenForInitPageState(win, NONCE, ORIGIN, () => {});
    expect(listening()).toBe(true);
    stop();
    expect(listening()).toBe(false);
  });
});

describe("the harness console", () => {
  it("hands over its query without the harness switches, one key at a time", () => {
    expect(pageStateFromAddress("?fixture=large&theme=dark&state=empty&conflict=1&view=files&published=no")).toEqual({ view: "files", published: "no" });
    expect(pageStateFromAddress("?view=files&View=x&q=" + "x".repeat(300))).toEqual({ view: "files" });
    expect(pageStateFromAddress("?view=files&view=shares")).toEqual({ view: "files" });
    expect(pageStateFromAddress("?constructor=x")).toEqual({ constructor: "x" });
    expect(pageStateFromAddress("")).toEqual({});
  });

  it("writes a state message over the query, keeping the harness switches", () => {
    expect(addressForState("?fixture=large&view=files&published=no", { view: "shares", link: "dead" })).toBe("?fixture=large&view=shares&link=dead");
    expect(addressForState("?fixture=large&view=files", {})).toBe("?fixture=large");
    expect(addressForState("?view=files", {})).toBe("");
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
