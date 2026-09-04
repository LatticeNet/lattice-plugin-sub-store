import { beforeEach, describe, expect, it } from "vitest";

import { closeTopOverlay, overlayDepth, registerOverlay, resetOverlayStack } from "./overlayStack";

describe("who owns Escape", () => {
  beforeEach(resetOverlayStack);

  it("is nobody when nothing is layered", () => {
    expect(overlayDepth()).toBe(0);
    expect(closeTopOverlay()).toBe(false);
  });

  it("closes the top one and nothing under it", () => {
    // A confirm opened from inside a panel: one press cancels the confirm and
    // leaves the panel exactly where it was. The old model either closed both
    // (no `.stop`) or neither, depending on which component had focus.
    const closed: string[] = [];
    const panel = registerOverlay(() => closed.push("panel"));
    registerOverlay(() => closed.push("confirm"));

    expect(overlayDepth()).toBe(2);
    expect(closeTopOverlay()).toBe(true);
    expect(closed).toEqual(["confirm"]);
    void panel;
  });

  it("does not pop on close, because the overlay's own state does", () => {
    // Escape, a click on the scrim and a screen closing it in code are one
    // path: the close function flips the state, the component unmounts, and
    // the dispose it registered with runs. Popping here as well would drop the
    // entry twice and let the next Escape reach the screen underneath.
    const dispose = registerOverlay(() => {});
    closeTopOverlay();
    expect(overlayDepth()).toBe(1);
    dispose();
    expect(overlayDepth()).toBe(0);
  });

  it("lets an overlay under another one leave without disturbing the order", () => {
    const order: string[] = [];
    const first = registerOverlay(() => order.push("first"));
    registerOverlay(() => order.push("second"));
    first();

    expect(overlayDepth()).toBe(1);
    expect(closeTopOverlay()).toBe(true);
    expect(order).toEqual(["second"]);
  });

  it("ignores a dispose called twice", () => {
    const dispose = registerOverlay(() => {});
    dispose();
    dispose();
    expect(overlayDepth()).toBe(0);
  });
});
