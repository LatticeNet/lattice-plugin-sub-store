import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const screen = readFileSync(new URL("./screens/SubscriptionsScreen.vue", import.meta.url), "utf8");
const shell = readFileSync(new URL("./Shell.vue", import.meta.url), "utf8");

/**
 * One word, one set.
 *
 * The toolbar's first lens tab reads "Subscriptions 7" and means every record
 * on the lens. The first group row inside the table read "Subscriptions 5",
 * meaning the records that are not combinations, 250px below. An operator
 * scanning for how many subscriptions exist read 7, then 5, and had to expand
 * Combinations and add to reconcile them.
 */
describe("the kind groups", () => {
  const groups = screen.slice(screen.indexOf("const groups = computed"), screen.indexOf("/** \"5 records"));

  it("do not reuse the lens tab's word for a subset of the lens", () => {
    expect(shell).toContain('{ id: "subscriptions", label: "Subscriptions"');
    expect(groups).toContain('label: "Single subscriptions"');
    expect(groups).not.toContain('label: "Subscriptions"');
    expect(groups).toContain('label: "Combinations"');
  });
});
