import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const SRC = new URL(".", import.meta.url);
const read = (path: string) => readFileSync(new URL(path, SRC), "utf8");

/**
 * Kind is a tab (Cloudflare filter), not a nested shelf. A record is a row
 * whose chevron trails; the chain is a well under that row. One disclosure.
 */
describe("the subscriptions list is a resource list, not a tree-in-a-table", () => {
  const screen = read("screens/SubscriptionsScreen.vue");
  const styles = read("styles.css").replace(/\/\*[\s\S]*?\*\//g, "");

  it("filters kind with tabs, not foldable group headings", () => {
    const tabs = read("components/RecKindTabs.vue");
    expect(tabs).toContain('class="rec-kind"');
    expect(tabs).toContain('role="tablist"');
    expect(screen).toContain("<RecKindTabs");
    expect(screen).toContain('id === "single"');
    expect(screen).not.toContain("<PcGroupRow");
    expect(screen).not.toContain("<PcRowToggle");
    expect(screen).not.toMatch(/<PcTable[\s\S]*Subscriptions/);
  });

  it("puts Open, then kebab, then chevron on the trailing edge of the row", () => {
    expect(screen).toContain('class="rec-chevron"');
    expect(screen).toContain('class="rec-ident"');
    expect(screen).toContain('class="rec-expand"');
    expect(screen).toContain('class="rec-open"');
    expect(screen).toContain("<RecKindTabs");
    expect(styles).toMatch(/\.rec-expand\[aria-expanded="true"\] \.rec-chevron\s*\{[^}]*rotate\(90deg\)/s);
    expect(styles).toMatch(/grid-template-columns:\s*1\.75rem minmax\(0, 1fr\) auto 1\.25rem/);
    expect(styles).not.toMatch(/\.rec-group-head/);
  });

  it("binds select-all mixed state as a vnode prop, not a watcher race", () => {
    expect(screen).toContain(':indeterminate="selectedCount > 0 && !allVisibleSelected"');
    expect(screen).not.toContain("selectAllEl");
  });

  it("opens the chain as a compact well, with Open on the row", () => {
    expect(screen).toContain('class="rec-detail"');
    expect(screen).toContain('class="rec-open"');
    expect(screen).toContain('class="rec-well"');
    expect(screen).toContain('id="`rec-chain-${row.id}`"');
    expect(screen).toContain("<RecordChainDetail");
    expect(screen).not.toContain("<PcDetailRow");
    expect(screen).not.toContain('class="rec-item-chain"');
    expect(screen).not.toContain('title="Records"');
    expect(screen).not.toContain('class="rec-detail-bar"');
  });
});

describe("the page chassis is a quiet Cloudflare header, not a KPI strip", () => {
  const shell = read("Shell.vue");

  it("has no stat strip on the list page", () => {
    expect(shell).not.toContain("PcStatStrip");
    expect(shell).not.toContain("PcStatCard");
    expect(shell).not.toContain('badge="Sub-Store plugin"');
    expect(shell).toContain("publishedLabel");
    expect(shell).toContain("proof-seg");
  });

  it("keeps one Add split on Subscriptions, with New combination in the menu", () => {
    expect(shell).toContain('class="add-split"');
    expect(shell).toContain("New subscription");
    expect(shell).toContain("runCommand('new-collection')");
    expect(shell).toContain("add-split-menu");
    expect(shell).not.toContain("PcSearchField");
  });
});
