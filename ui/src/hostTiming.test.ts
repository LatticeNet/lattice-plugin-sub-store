import { readFileSync, readdirSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";

/**
 * Every screen that loads data must wait for the bridge handshake.
 *
 * `host.available()` reads the interfaces the host declares for this frame, and
 * that arrives asynchronously. A screen that loads only in `onMounted` runs
 * before it: `available()` is false, the load silently no-ops, and nothing ever
 * retries. The symptom is a screen that looks empty and permissionless while
 * the backend is perfectly healthy — which is exactly what shipped, twice.
 *
 * This is asserted as a source check rather than a behavioural test because the
 * failure is structural: the screen is wired to the wrong signal, and no amount
 * of mocking a bridge catches a screen that simply never asks again.
 */

const SCREENS_DIR = new URL("./screens", import.meta.url).pathname;

function screenFiles(): string[] {
  return readdirSync(SCREENS_DIR).filter((name) => name.endsWith(".vue"));
}

describe("screens wait for the bridge handshake", () => {
  it("has screens to check", () => {
    expect(screenFiles().length).toBeGreaterThan(0);
  });

  for (const file of screenFiles()) {
    it(`${file} does not load only on mount`, () => {
      const source = readFileSync(join(SCREENS_DIR, file), "utf8");
      const loadsOnMount = source.includes("onMounted");
      if (!loadsOnMount) return;

      // A screen that loads on mount must also react to the handshake landing.
      expect(
        source.includes("watch(host.init"),
        `${file} loads in onMounted but never watches host.init, so it will no-op ` +
          `when the handshake has not arrived yet and will never retry`,
      ).toBe(true);
    });
  }
});

/**
 * The other half of the timing contract: when the handshake never comes at
 * all (the frame opened standalone), the shell must say so instead of leaving
 * every screen on "Loading…" forever. Structural for the same reason as
 * above — what matters is that the shell is wired to the timeout at all.
 */
describe("the shell degrades a missing handshake", () => {
  it("replaces the perpetual loading state with the standalone notice", () => {
    const shell = readFileSync(join(new URL(".", import.meta.url).pathname, "Shell.vue"), "utf8");
    expect(shell.includes("useHandshakeTimeout")).toBe(true);
    expect(shell.includes("StandaloneNotice")).toBe(true);
  });
});
