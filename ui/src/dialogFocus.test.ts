import { describe, expect, it, vi } from "vitest";

import { trapDialogTab } from "./dialogFocus";

function focusable() {
  return { focus: vi.fn() } as unknown as HTMLElement;
}

function harness(count = 3) {
  const elements = Array.from({ length: count }, focusable);
  const root = {
    querySelectorAll: () => elements,
    contains: (target: unknown) => elements.includes(target as HTMLElement),
    focus: vi.fn(),
  } as unknown as HTMLElement;
  const event = (target: unknown, shiftKey = false) => ({
    key: "Tab",
    shiftKey,
    target,
    preventDefault: vi.fn(),
  }) as unknown as KeyboardEvent;
  return { elements, root, event };
}

describe("trapDialogTab", () => {
  it("wraps forward from the final control", () => {
    const { elements, root, event } = harness();
    const tab = event(elements[2]);
    trapDialogTab(tab, root);
    expect(tab.preventDefault).toHaveBeenCalledOnce();
    expect(elements[0]!.focus).toHaveBeenCalledOnce();
  });

  it("wraps backward from the first control", () => {
    const { elements, root, event } = harness();
    const tab = event(elements[0], true);
    trapDialogTab(tab, root);
    expect(tab.preventDefault).toHaveBeenCalledOnce();
    expect(elements[2]!.focus).toHaveBeenCalledOnce();
  });

  it("does not interfere between the first and last control", () => {
    const { elements, root, event } = harness();
    const tab = event(elements[1]);
    trapDialogTab(tab, root);
    expect(tab.preventDefault).not.toHaveBeenCalled();
    expect(elements.every((element) => !vi.mocked(element.focus).mock.calls.length)).toBe(true);
  });
});
