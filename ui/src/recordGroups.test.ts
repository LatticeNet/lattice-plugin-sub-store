import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const screen = readFileSync(new URL("./screens/SubscriptionsScreen.vue", import.meta.url), "utf8");
const shell = readFileSync(new URL("./Shell.vue", import.meta.url), "utf8");
const tabs = readFileSync(new URL("./components/RecKindTabs.vue", import.meta.url), "utf8");

/**
 * One word, one set.
 *
 * The toolbar's first lens tab reads "Subscriptions 7" and means every record
 * on the lens. Kind is a second tablist: All / Single / Combinations. Single
 * must not reuse the lens word, or an operator scanning counts reads 7 then
 * 5 and cannot tell which set is which.
 */
describe("the kind filter", () => {
  it("does not reuse the lens tab's word for a subset of the lens", () => {
    expect(shell).toContain('{ id: "subscriptions", label: "Subscriptions"');
    expect(tabs).toContain('class="rec-kind"');
    expect(screen).toContain('id === "single"');
    expect(screen).toContain('id === "combo"');
    expect(screen).not.toMatch(/kindFilter = 'subscriptions'/);
    const start = screen.indexOf("const kindTabs");
    const kind = screen.slice(start, screen.indexOf("];", start));
    expect(kind).toContain("Single");
    expect(kind).toContain("Combinations");
    expect(kind).not.toContain("Subscriptions");
  });
});
