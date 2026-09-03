import { describe, expect, it, vi } from "vitest";

import { CLIPBOARD_ACK_TYPE, CLIPBOARD_MESSAGE_TYPE, copyText, readClipboardChannel, requestHostCopy } from "./hostClipboard";

const NONCE = "n".repeat(24);
const HOST = "https://lattice.example";
const HASH = `#lattice_nonce=${NONCE}&host_origin=${encodeURIComponent(HOST)}`;

/**
 * A stand-in for the frame's own window plus the host window above it. Real
 * enough for identity: a message only counts when it comes from the parent, at
 * the pinned origin, with our nonce and our request id.
 */
function makeWindow(hash = HASH) {
  const listeners = new Set<(event: MessageEvent) => void>();
  const posted: Array<Record<string, unknown>> = [];
  const parent = {} as Window;
  const win = {
    location: { hash },
    parent,
    addEventListener: (_type: string, fn: (event: MessageEvent) => void) => listeners.add(fn),
    removeEventListener: (_type: string, fn: (event: MessageEvent) => void) => listeners.delete(fn),
    setTimeout: (fn: () => void, ms: number) => setTimeout(fn, ms) as unknown as number,
    clearTimeout: (id: number) => clearTimeout(id),
    navigator: { clipboard: { writeText: vi.fn(async () => undefined) } },
  } as unknown as Window & { navigator: { clipboard: { writeText: ReturnType<typeof vi.fn> } } };
  (parent as unknown as { postMessage: (data: unknown, origin: string) => void }).postMessage = (data) => {
    posted.push(data as Record<string, unknown>);
  };
  function deliver(data: unknown, overrides: { source?: unknown; origin?: string } = {}) {
    const event = {
      source: "source" in overrides ? overrides.source : parent,
      origin: overrides.origin ?? HOST,
      data,
    } as unknown as MessageEvent;
    for (const listener of [...listeners]) listener(event);
  }
  return { win, posted, deliver, listenerCount: () => listeners.size };
}

describe("readClipboardChannel", () => {
  it("reads the nonce and the pinned host origin from the frame fragment", () => {
    const { win } = makeWindow();
    expect(readClipboardChannel(HASH, win)).toEqual({ nonce: NONCE, hostOrigin: HOST });
  });

  it("has no channel when there is no host above this document", () => {
    const win = { parent: undefined } as unknown as Window;
    (win as unknown as { parent: unknown }).parent = win;
    expect(readClipboardChannel(HASH, win)).toBeNull();
  });

  it("fails closed on a missing, short or non-http channel", () => {
    const { win } = makeWindow();
    expect(readClipboardChannel("", win)).toBeNull();
    expect(readClipboardChannel(`#lattice_nonce=short&host_origin=${encodeURIComponent(HOST)}`, win)).toBeNull();
    expect(readClipboardChannel(`#lattice_nonce=${NONCE}`, win)).toBeNull();
    expect(
      readClipboardChannel(`#lattice_nonce=${NONCE}&host_origin=${encodeURIComponent("javascript:alert(1)")}`, win),
    ).toBeNull();
  });
});

describe("requestHostCopy", () => {
  const channel = { nonce: NONCE, hostOrigin: HOST };

  it("asks the host and resolves true on an ack", async () => {
    const { win, posted, deliver } = makeWindow();
    const pending = requestHostCopy("https://example.test/sub", channel, win);
    expect(posted).toHaveLength(1);
    expect(posted[0]!.type).toBe(CLIPBOARD_MESSAGE_TYPE);
    expect(posted[0]!.nonce).toBe(NONCE);
    expect(posted[0]!.text).toBe("https://example.test/sub");

    deliver({ type: CLIPBOARD_ACK_TYPE, nonce: NONCE, id: posted[0]!.id, ok: true });
    await expect(pending).resolves.toBe(true);
  });

  // The refusal is the whole reason this returns a boolean rather than throwing:
  // the caller has to put the value on screen, which is not an error path.
  it("resolves false when the host says it could not copy", async () => {
    const { win, posted, deliver } = makeWindow();
    const pending = requestHostCopy("value", channel, win);
    deliver({ type: CLIPBOARD_ACK_TYPE, nonce: NONCE, id: posted[0]!.id, ok: false, code: "clipboard_refused" });
    await expect(pending).resolves.toBe(false);
  });

  it("ignores an ack from another window, another origin, another nonce or another request", async () => {
    const { win, posted, deliver } = makeWindow();
    const pending = requestHostCopy("value", channel, win, 40);
    const id = posted[0]!.id;

    deliver({ type: CLIPBOARD_ACK_TYPE, nonce: NONCE, id, ok: true }, { source: {} });
    deliver({ type: CLIPBOARD_ACK_TYPE, nonce: NONCE, id, ok: true }, { origin: "https://evil.example" });
    deliver({ type: CLIPBOARD_ACK_TYPE, nonce: "wrong", id, ok: true });
    deliver({ type: CLIPBOARD_ACK_TYPE, nonce: NONCE, id: "someone-else", ok: true });

    // None of those settled it, so it falls to the timeout, which is a refusal.
    await expect(pending).resolves.toBe(false);
  });

  it("treats a host that never answers as a refusal and stops listening", async () => {
    const { win, listenerCount } = makeWindow();
    await expect(requestHostCopy("value", channel, win, 20)).resolves.toBe(false);
    expect(listenerCount()).toBe(0);
  });

  it("removes its listener once answered, so copies do not accumulate handlers", async () => {
    const { win, posted, deliver, listenerCount } = makeWindow();
    const pending = requestHostCopy("value", channel, win);
    expect(listenerCount()).toBe(1);
    deliver({ type: CLIPBOARD_ACK_TYPE, nonce: NONCE, id: posted[0]!.id, ok: true });
    await pending;
    expect(listenerCount()).toBe(0);
  });

  it("gives every request its own id, so two copies cannot answer each other", async () => {
    const { win, posted, deliver } = makeWindow();
    const first = requestHostCopy("one", channel, win, 40);
    const second = requestHostCopy("two", channel, win, 40);
    expect(posted[0]!.id).not.toBe(posted[1]!.id);

    deliver({ type: CLIPBOARD_ACK_TYPE, nonce: NONCE, id: posted[1]!.id, ok: true });
    await expect(second).resolves.toBe(true);
    await expect(first).resolves.toBe(false);
  });
});

describe("copyText", () => {
  it("goes through the host when this document is framed", async () => {
    const { win, posted, deliver } = makeWindow();
    const pending = copyText("value", win);
    await Promise.resolve();
    expect(posted).toHaveLength(1);
    // The frame must not touch its own clipboard: Permissions Policy blocks it
    // and every attempt logs a violation the operator can see.
    expect(win.navigator.clipboard.writeText).not.toHaveBeenCalled();
    deliver({ type: CLIPBOARD_ACK_TYPE, nonce: NONCE, id: posted[0]!.id, ok: true });
    await expect(pending).resolves.toBe(true);
  });

  it("uses the local clipboard when there is no host to ask", async () => {
    const { win, posted } = makeWindow("");
    await expect(copyText("value", win)).resolves.toBe(true);
    expect(win.navigator.clipboard.writeText).toHaveBeenCalledWith("value");
    expect(posted).toHaveLength(0);
  });

  it("never throws when the local clipboard rejects", async () => {
    const { win } = makeWindow("");
    win.navigator.clipboard.writeText.mockRejectedValueOnce(new Error("NotAllowedError"));
    await expect(copyText("value", win)).resolves.toBe(false);
  });
});
