import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

import { en } from "./messages/en";

const shell = readFileSync(new URL("./Shell.vue", import.meta.url), "utf8");
const page = readFileSync(new URL("./screens/RecordPage.vue", import.meta.url), "utf8");

/**
 * The layering rule (design 22, section 2): an area has one tab row for its
 * layers, mirrored in the address; there is never a second tab row inside
 * the first. A record page has tabs of its own, so it replaces the layer row
 * rather than stacking under it.
 */
describe("the layers", () => {
  it("are four, in the design's order, Overview first and the default", () => {
    // No icon per layer: the row is vpn-core's underline row (design 23 section 3.4).
    // Records holds every kind (design 28); the three per-kind layers it
    // replaced land on it through pageState's legacy views.
    const ids = [...shell.matchAll(/\{ id: "(\w+)", label: t\.layers\.(\w+), screen:/g)].map((m) => `${m[1]}:${en.layers[m[2] as keyof typeof en.layers]}`);
    expect(ids).toEqual(["overview:Overview", "records:Records", "shares:Shares", "settings:Settings"]);
    expect(shell).toContain('const activeTab = ref<TabId>("overview");');
  });

  it("keep the layer, the peek and the record page in the console's address, not the frame's", () => {
    // The console hands the state over at the handshake and the shell hands
    // every change back; pageState.test.ts covers the wire itself.
    expect(shell).toContain("applyState(decodeShellState(host.pageState.value));");
    expect(shell).toContain("if (stateApplied.value) stateSender.push(encodeShellState(state));");
    expect(shell).toContain("createStateSender((state) => host.sendState(state))");
    // The frame's own query does not survive a console reload, so the shell
    // no longer writes it.
    expect(shell).not.toContain("useDocumentQueryState");
  });

  it("give way to the record page's own tab row", () => {
    expect(shell).toContain('<PcLensTabs v-if="!recordId" v-model="activeTab" variant="layer" :label="t.shell.layersLabel">');
    expect(shell.match(/<PcLensTabs/g)).toHaveLength(1);
    expect(page.match(/<PcLensTabs/g)).toHaveLength(1);
  });

  it("name the record page's tabs the way the design does, Output for files only", () => {
    expect(page).toContain('? [{ id: "output", label: t.page.tabOutput }]');
    expect(page).toContain(': [{ id: "nodes", label: t.page.tabNodes }]');
    expect(page).toContain(
      '{ id: "steps", label: t.page.tabSteps }, { id: "source", label: t.page.tabSource }, { id: "publishing", label: t.page.tabPublishing }',
    );
    expect([en.page.tabOutput, en.page.tabNodes, en.page.tabSteps, en.page.tabSource, en.page.tabPublishing]).toEqual([
      "Output",
      "Nodes",
      "Steps",
      "Source",
      "Publishing",
    ]);
  });

  it("mask a provider link after the host and reveal it for sixty seconds", () => {
    expect(page).toContain("{{ reveal.on.value ? url : maskUrl(url) }}");
    expect(page).toContain("const reveal = useReveal();");
    expect(page).toContain("t.page.reveal");
    expect(en.page.reveal).toBe("Reveal for 60s");
  });
});
