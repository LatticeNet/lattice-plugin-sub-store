import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const SRC = new URL(".", import.meta.url);
const read = (path: string) => readFileSync(new URL(path, SRC), "utf8");

/**
 * Records is the one L1 collection for every kind (design 28, S1; design 22
 * section 2 for the layering): one table, the kind as a filter rather than a
 * second tab row, one affordance per row. The chain lives on the record page.
 */
describe("the records layer is one table with one affordance per row", () => {
  const screen = read("screens/SubscriptionsScreen.vue");
  const shell = read("Shell.vue");

  it("is one screen for every kind, the kind a filter rather than a tab row", () => {
    expect(shell).toMatch(/\{ id: "records", label: "Records", screen: SubscriptionsScreen \}/);
    expect(screen).not.toContain("defineProps");
    expect(screen).toContain('<fieldset class="rec-kinds">');
    expect(screen).toContain('v-model="kindFacet" type="radio"');
    expect(screen).not.toContain("RecKindTabs");
    expect(screen).not.toContain('role="tablist"');
    expect(screen).not.toContain("<PcLensTabs");
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
    expect(screen).toContain(':title="cell(row).expiry.title"');
    expect(screen).toContain(':title="cell(row).fetch ? undefined : TEXT.notFetched"');
  });

  it("filters the migration marker as a facet, not a chip on every row", () => {
    expect(screen).toContain('v-model="facets.origin"');
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
    expect(shell).toMatch(/class="[^"]*\badd-split"/);
    expect(shell).toContain("runCommand('new-collection')");
    expect(shell).toContain("runCommand('new-file')");
    expect(shell).toContain("add-split-menu");
    expect(shell).not.toContain("PcSearchField");
  });

  it("renders the create rules from createGate rather than deciding them", () => {
    // Which verb, when it shows and why it is disabled are tested in createGate.test.ts.
    expect(shell).toMatch(/headerCreate\(\{\s*tab: activeTab\.value,\s*kind: chrome\.facets\.kind,\s*catalogue: catalogueView\.value/);
    expect(shell).toContain('const blocks = computed(() => createBlocks(catalogueView.value));');
    expect(shell).toContain(':create-blocked="blocks"');
  });

  it("puts the primary action in the header after Refresh, never in the tab row", () => {
    const actions = shell.slice(shell.indexOf("<template #actions>"), shell.indexOf("<template #proof>"));
    for (const control of ["{{ head.label }}", "Open in Publishing"]) expect(actions, control).toContain(control);
    expect(actions.indexOf("header-refresh")).toBeLessThan(actions.indexOf("ss-head-primary"));
    expect(shell).not.toContain("#primary");
  });
});
