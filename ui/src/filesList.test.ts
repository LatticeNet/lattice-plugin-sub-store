import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const SRC = new URL(".", import.meta.url);
const read = (path: string) => readFileSync(new URL(path, SRC), "utf8");

/**
 * Records is one L1 collection on the table chassis for every kind (design 28,
 * S1): the columns every kind shares, one affordance per row. What each cell
 * says is tested in recordTable.test.ts and the states in
 * e2e/record-table.spec.ts; this is the wiring those rules ride on.
 */
describe("the records layer is one table of every kind", () => {
  const styles = read("styles.css").replace(/\/\*[\s\S]*?\*\//g, "");
  const screen = read("screens/SubscriptionsScreen.vue");

  it("has the design's columns, and stacks into rows on a phone instead of scrolling sideways", () => {
    for (const column of ["Name", "Kind", "Published", "Nodes in", "Nodes out", "Steps", "Expiry and traffic", "Last fetch"]) {
      expect(screen, column).toMatch(new RegExp(`<PcTh[^>]*>${column}</PcTh>`));
    }
    // No `:stacked="false"`: the chassis stacks the rows under 480px.
    expect(screen).not.toContain(':stacked="false"');
    expect(screen).toContain(':density="compact ? \'compact\' : \'comfortable\'"');
  });

  it("offers Publish where a record has no share, and filters on it from the address", () => {
    expect(screen).toContain('class="row-publish"');
    expect(screen).toContain('@click.stop="openShares(row)"');
    expect(screen).toContain("sharesRoute(record.id)");
    expect(screen).toContain('v-model="facets.published"');
    expect(screen).toContain('if (facets.published === "no" && isPublished(item)) return false;');
  });

  it("opens the side panel from the row, with the menu the only other control", () => {
    expect(screen).toContain('@click="openRow(row, $event)"');
    expect(screen.match(/<RecordMenu/g)).toHaveLength(1);
    expect(screen).toContain("return rowMenuFor(row, actionCaps.value);");
    expect(screen).not.toContain('class="rec-open"');
  });

  it("keeps no sideways scroll rule outside a scroller", () => {
    for (const m of styles.matchAll(/([^{}]+)\{[^}]*width:\s*max-content/g)) {
      const selector = m[1]!.trim();
      expect(selector, selector + " widens rows outside a scroller").toMatch(/^\.pc-batch-bar|^\.lt-batchbar/);
    }
  });

  it("opens the sheet without displacing it sideways", () => {
    const frames = styles.match(/@keyframes sheet-in \{[^}]*\}/s);
    expect(frames, "sheet-in keyframes are gone").not.toBeNull();
    expect(frames![0]).not.toMatch(/translateX/);
  });

  it("pages by fifty, and selects and deletes only the rows on screen", () => {
    // The paging and select-all rules are tested in paging.test.ts.
    expect(screen).toContain("const PAGE_SIZE = 50;");
    expect(screen).toMatch(/usePages\(\s*\(\) => sorted\.value,\s*PAGE_SIZE,/);
    expect(screen).toContain('v-for="(row, index) in table.rows"');
    expect(screen).toContain("table.value.rows.filter((row) => selectedIds.value.has(row.id))");
    expect(screen).toContain("toggleShown(selectedIds.value, table.value.rows.map((row) => row.id))");
  });

  it("opens one document surface from every entry", () => {
    expect(screen).toMatch(/if \(id === "output"\) return openTargetSheet\(row, event\);/);
    expect(screen).not.toContain("row-popover-document");
  });
});

describe("the shares layer is the list from the client's side", () => {
  const shares = read("screens/SharesScreen.vue");

  it("has the design's columns and keeps them at every width", () => {
    expect(shares).toMatch(/<PcTable v-else :stacked="false"/);
    for (const column of ["Path", "Record", "Format", "Expiry", "State"]) {
      expect(shares, column).toMatch(new RegExp(`<PcTh[^>]*>${column}</PcTh>`));
    }
    expect(shares).toContain("as the client asks");
    expect(shares).toContain("<PcStatePill");
  });

  it("keeps Copy link as the row's one verb and opens the record behind it", () => {
    expect(shares).toContain('{{ copiedId === line.share.share_id ? "Copied" : "Copy link" }}');
    expect(shares).toContain('@click="openRow(line, $event)"');
    expect(shares).toContain("if (line.record) chrome.openRecord(line.record.id);");
  });
});
