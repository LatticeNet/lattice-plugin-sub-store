import { describe, expect, it } from "vitest";

import { anchorTopFrom, clampAnchorTop } from "./overlayAnchor";

describe("overlay anchoring inside a frame that is not a viewport", () => {
  it("anchors to the element that was clicked, in document space", () => {
    // A row 900px down a 1600px frame: `position: fixed` would put the sheet at
    // the top of the frame, which is off the operator's screen entirely.
    const element = {
      getBoundingClientRect: () => ({ top: 900 }) as DOMRect,
    } as unknown as Element;
    const event = { currentTarget: element } as unknown as Event;
    expect(anchorTopFrom(event)).toBe(892);
  });

  it("never opens flush against the document top", () => {
    const element = { getBoundingClientRect: () => ({ top: 2 }) as DOMRect } as unknown as Element;
    expect(anchorTopFrom({ currentTarget: element } as unknown as Event)).toBe(12);
  });

  it("falls back to the current scroll offset with no event", () => {
    expect(anchorTopFrom(null)).toBeGreaterThanOrEqual(12);
    expect(anchorTopFrom(undefined)).toBeGreaterThanOrEqual(12);
  });

  it("keeps a tall overlay inside the document", () => {
    // Opening a 600px sheet from a row near the end of a 1000px document must
    // not start it at 900, or its own content is unreachable.
    expect(clampAnchorTop(900, 600, 1000)).toBe(388);
    expect(clampAnchorTop(100, 600, 1000)).toBe(100);
    // A document shorter than the overlay still yields a usable top.
    expect(clampAnchorTop(500, 900, 400)).toBe(12);
  });
});
