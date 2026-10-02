import { effectScope, nextTick, ref } from "vue";
import { afterEach, describe, expect, it, vi } from "vitest";

import { formatAge, untilNextAge, useObservedAge } from "./observedAge";

describe("the proof line's age", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it("prints the console's units", () => {
    expect(formatAge(-5)).toBe("0s");
    expect(formatAge(13_400)).toBe("13s");
    expect(formatAge(59_999)).toBe("59s");
    expect(formatAge(60_000)).toBe("1m");
    expect(formatAge(3_599_999)).toBe("59m");
    expect(formatAge(3_600_000)).toBe("1h");
    expect(formatAge(47 * 3_600_000)).toBe("47h");
    expect(formatAge(48 * 3_600_000)).toBe("2d");
  });

  it("wakes when the label would change, not more often", () => {
    expect(untilNextAge(13_400)).toBe(600);
    expect(untilNextAge(90_000)).toBe(30_000);
    expect(untilNextAge(3_600_000 + 5)).toBe(3_600_000 - 5);
  });

  it("ticks while the page is open, restarts at the next read, and stops with its scope", async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-09-30T12:00:00Z"));
    const at = ref<number>();
    const scope = effectScope();
    const observed = scope.run(() => useObservedAge(() => at.value))!;
    expect(observed.age.value).toBe("");
    expect(observed.title.value).toBe("");

    at.value = Date.now();
    await nextTick();
    expect(observed.age.value).toBe("0s");
    expect(observed.title.value).toMatch(/^observed /);
    vi.advanceTimersByTime(13_000);
    expect(observed.age.value).toBe("13s");
    vi.advanceTimersByTime(60_000);
    expect(observed.age.value).toBe("1m");

    at.value = Date.now();
    await nextTick();
    expect(observed.age.value).toBe("0s");

    scope.stop();
    expect(vi.getTimerCount()).toBe(0);
  });
});
