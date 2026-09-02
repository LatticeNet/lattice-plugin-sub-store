import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { useReveal } from "./reveal";
import { REVEAL_MS } from "./urlMask";

describe("a reveal that ends on its own", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it("shows for a minute and masks itself again", () => {
    const reveal = useReveal();
    expect(reveal.on.value).toBe(false);
    reveal.toggle();
    expect(reveal.on.value).toBe(true);
    vi.advanceTimersByTime(REVEAL_MS - 1);
    expect(reveal.on.value).toBe(true);
    vi.advanceTimersByTime(1);
    expect(reveal.on.value).toBe(false);
  });

  it("restarts the minute on a new reveal and stops on hide", () => {
    const reveal = useReveal(1000);
    reveal.show();
    vi.advanceTimersByTime(800);
    reveal.show();
    vi.advanceTimersByTime(800);
    expect(reveal.on.value).toBe(true);
    reveal.toggle();
    expect(reveal.on.value).toBe(false);
    vi.advanceTimersByTime(5000);
    expect(reveal.on.value).toBe(false);
  });
});
