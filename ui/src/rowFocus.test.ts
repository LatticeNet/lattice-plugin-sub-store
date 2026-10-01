import { describe, expect, it } from "vitest";

import { anchorAfterDelete } from "./rowFocus";

describe("anchorAfterDelete", () => {
  const order = ["a", "b", "c", "d", "e"];

  it("takes the row above the deleted one", () => {
    expect(anchorAfterDelete(order, ["c"])).toBe("b");
  });

  it("takes the first surviving row when the first row went", () => {
    expect(anchorAfterDelete(order, ["a"])).toBe("b");
    expect(anchorAfterDelete(order, ["a", "b"])).toBe("c");
  });

  it("skips rows deleted in the same batch, above the first one too", () => {
    expect(anchorAfterDelete(order, ["d", "b", "c"])).toBe("a");
    expect(anchorAfterDelete(order, ["e", "d"])).toBe("c");
  });

  it("is empty when nothing is left, or when nothing listed was deleted", () => {
    expect(anchorAfterDelete(order, order)).toBe("");
    expect(anchorAfterDelete(order, ["z"])).toBe("");
    expect(anchorAfterDelete([], ["a"])).toBe("");
  });
});
