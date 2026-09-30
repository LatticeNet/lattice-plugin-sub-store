import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const SRC = new URL(".", import.meta.url);
const read = (path: string) => readFileSync(new URL(path, SRC), "utf8");

/**
 * Sources and Combinations are L1 collections (design 22, section 2): one
 * table each, columns that mean something for that kind, one affordance per
 * row. The row used to carry Open, a menu and a chevron at once, and its
 * chain unfolded in place; the chain now lives on the record page.
 */
describe("the sources and combinations layers are tables with one affordance per row", () => {
  const screen = read("screens/SubscriptionsScreen.vue");
  const shell = read("Shell.vue");

  it("is one screen for two layers, split by kind", () => {
    expect(shell).toMatch(/\{ id: "sources", label: "Sources", icon: Library, screen: SubscriptionsScreen, props: \{ kind: KIND_SUB \} \}/);
    expect(shell).toMatch(/\{ id: "combinations", label: "Combinations", icon: Layers, screen: SubscriptionsScreen, props: \{ kind: KIND_COLLECTION \} \}/);
    expect(screen).toContain('defineProps<{ kind: "sub" | "collection" }>()');
    // Kind is the layer now, so there is no second tab row inside it.
    expect(screen).not.toContain("RecKindTabs");
    expect(screen).not.toContain('role="tablist"');
  });

  it("keeps its columns at every width, first column sticky", () => {
    expect(screen).toMatch(/<PcTable v-else :stacked="false"/);
    expect(screen).toContain('<td class="pc-name" data-stack="name">');
    for (const column of ["Name", "Kind", "Members", "Nodes", "Steps", "Provider", "Last fetch", "Used by"]) {
      expect(screen, column).toMatch(new RegExp(`<PcTh[^>]*>${column}</PcTh>`));
    }
  });

  it("opens the side panel from the row and keeps every other verb in one menu", () => {
    expect(screen).toContain('@click="openRow(row, $event)"');
    expect(screen).toContain('class="row-open"');
    expect(screen.match(/<RecordMenu/g)).toHaveLength(1);
    expect(screen).toContain("return rowMenuFor(row, actionCaps.value);");
    for (const retired of ['class="rec-open"', 'class="rec-expand"', "<PcRowToggle", "<RecordChainDetail", 'class="rec-well"']) {
      expect(screen, retired).not.toContain(retired);
    }
  });

  it("never prints n/a: a cell with nothing to say is empty and says why on hover", () => {
    expect(screen).not.toContain('"n/a"');
    expect(screen).toContain(':title="figuresOf(row) ? undefined : NOT_A_PROVIDER"');
    expect(screen).toContain(':title="isProviderLink(row) ? undefined : NOT_FETCHED"');
  });

  it("filters the migration marker as a facet, not a chip on every row", () => {
    expect(screen).toContain('v-model="originFilter.origin"');
    expect(screen).not.toContain("tagChips(");
    expect(screen).not.toMatch(/label="migrated"/);
  });

  it("binds select-all mixed state as a vnode prop, not a watcher race", () => {
    expect(screen).toContain(':indeterminate="selectedCount > 0 && !allVisibleSelected"');
    expect(screen).not.toContain("selectAllEl");
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

  it("offers one primary action per layer, the overview's with the other kinds in its menu", () => {
    expect(shell).toContain('class="add-split"');
    expect(shell).toContain("New subscription");
    expect(shell).toContain("runCommand('new-collection')");
    expect(shell).toContain("runCommand('new-file')");
    expect(shell).toContain("add-split-menu");
    expect(shell).not.toContain("PcSearchField");
  });
});
