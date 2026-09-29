import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const SRC = new URL(".", import.meta.url);
const read = (path: string) => readFileSync(new URL(path, SRC), "utf8");

/**
 * Files and Shares are L1 collections on the same table chassis as Sources:
 * columns that mean something for the kind, the first one sticky, one
 * affordance per row (design 22, section 4).
 */
describe("the files layer is a table of what each file renders and whether it is served", () => {
  const styles = read("styles.css").replace(/\/\*[\s\S]*?\*\//g, "");
  const screen = read("screens/FilesScreen.vue");

  it("keeps its columns at every width", () => {
    expect(screen).toMatch(/<PcTable v-else :stacked="false"/);
    for (const column of ["Name", "Client", "Renders", "Published", "Type"]) {
      expect(screen, column).toMatch(new RegExp(`<PcTh[^>]*>${column}</PcTh>`));
    }
    expect(screen).not.toContain("RecKindTabs");
  });

  it("offers Publish where a file has no share, and filters on it from the address", () => {
    expect(screen).toContain('class="row-publish"');
    expect(screen).toContain('@click.stop="openShares(item.name)"');
    expect(screen).toContain('v-model="facets.published"');
    expect(screen).toContain('if (facets.published === "no" && isPublished(file)) return false;');
  });

  it("reads the client from the name and says why when it cannot", () => {
    expect(screen).toContain("const client = clientOfFile(item.name);");
    expect(screen).toContain("does not name a client app");
  });

  it("opens the side panel from the row, with the menu the only other control", () => {
    expect(screen).toContain('@click="openRow(item, $event)"');
    expect(screen.match(/<RecordMenu/g)).toHaveLength(1);
    expect(screen).toContain('const MENU_ACTIONS = ["output", "duplicate", "delete"] as const;');
    expect(screen).not.toContain('class="rec-open"');
    expect(screen).not.toContain('class="rec-file-facts"');
  });

  it("puts the whole name and the id in the title", () => {
    expect(screen).toContain(':title="nameTitle(item)"');
    expect(screen).toMatch(/function nameTitle[\s\S]*?item\.display_name \|\| item\.name/);
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

  it("opens one document surface from every entry", () => {
    expect(screen).toMatch(/if \(id === "output"\) return openFileSheet\(item, event\);/);
    expect(screen).not.toContain("row-popover-document");
    expect(screen).not.toMatch(/mode: "preview"/);
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
