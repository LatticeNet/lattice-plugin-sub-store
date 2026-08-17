import { ref } from "vue";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { HANDSHAKE_TIMEOUT_MS, useHandshakeTimeout } from "./handshakeTimeout";

/**
 * Standalone open hangs on "Loading…" forever unless something says so. These
 * pin the timeout that turns the silence into a statement — and the reset that
 * keeps a slow-but-real host from getting stuck behind the notice.
 */

describe("useHandshakeTimeout", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("expires after the timeout when no handshake arrives", () => {
    const init = ref(undefined);
    const expired = useHandshakeTimeout(init, 1000);
    expect(expired.value).toBe(false);
    vi.advanceTimersByTime(999);
    expect(expired.value).toBe(false);
    vi.advanceTimersByTime(1);
    expect(expired.value).toBe(true);
  });

  it("stays quiet when the handshake lands in time", async () => {
    const init = ref<unknown>(undefined);
    const expired = useHandshakeTimeout(init, 1000);
    vi.advanceTimersByTime(500);
    init.value = { version: "1" };
    // The watcher flushes on a microtask, which advancing timers does not run.
    await Promise.resolve();
    vi.advanceTimersByTime(5000);
    expect(expired.value).toBe(false);
    expect(vi.getTimerCount()).toBe(0);
  });

  it("never arms when the handshake is already here", () => {
    const expired = useHandshakeTimeout(ref({ version: "1" }), 1000);
    vi.advanceTimersByTime(5000);
    expect(expired.value).toBe(false);
  });

  // A host that takes longer than the timeout is slow, not absent: the notice
  // shows, then the handshake lands and the real UI replaces it.
  it("resets when a late handshake finally arrives", async () => {
    const init = ref<unknown>(undefined);
    const expired = useHandshakeTimeout(init, 1000);
    vi.advanceTimersByTime(1500);
    expect(expired.value).toBe(true);
    init.value = { version: "1" };
    await Promise.resolve();
    expect(expired.value).toBe(false);
  });

  it("defaults to the production timeout", () => {
    expect(HANDSHAKE_TIMEOUT_MS).toBe(3000);
  });
});
