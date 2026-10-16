import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";

import { describe, expect, it } from "vitest";

import manifest from "../../manifest.json";
import { BINDINGS, CONVERT_TARGETS, SERVICES, buildShareLink, type MethodBinding } from "./client";

/**
 * DoD gate: every UI data path must resolve to a manifest-declared method.
 * The UI speaks only `lattice.plugin.call`, and the host rejects anything the
 * signed manifest does not declare, so the binding table is checked against
 * the manifest directly.
 *
 * Tier rules:
 *  - active  ⊆ manifest (calling anything undeclared would 400 in production)
 *  - pending ∩ manifest = ∅ (tripwire: when TASK-0002's contract lands, this
 *    test fails and forces the bindings to be reclassified to active)
 */

interface ManifestMethod {
  name: string;
}

interface ManifestInterface {
  service: string;
  methods: ManifestMethod[];
}

const declared = new Set(
  (manifest.interfaces as ManifestInterface[]).flatMap((entry) =>
    entry.methods.map((method) => `${entry.service}/${method.name}`),
  ),
);

function key(target: MethodBinding): string {
  return `${target.service}/${target.method}`;
}

const all = Object.values(BINDINGS);
const active = all.filter((target) => target.status === "active");
const pending = all.filter((target) => target.status === "pending");

describe("UI ↔ manifest method contract", () => {
  it("no longer declares the outbound import service", () => {
    // It pushed nodes to an external Sub-Store. The direction this plugin
    // exists to remove. Re-declaring it should be a deliberate act, not a
    // silent reappearance.
    expect([...declared].some((entry) => entry.startsWith("latticenet.sub-store/import/"))).toBe(false);
  });

  it("declares the embedded engine service in the manifest", () => {
    const engineMethods = [...declared].filter((entry) => entry.startsWith(`${SERVICES.engine}/`));
    expect(engineMethods.length).toBeGreaterThanOrEqual(7);
  });

  it("every active binding is declared in the signed manifest", () => {
    expect(active.length).toBeGreaterThan(0);
    for (const target of active) {
      expect(declared.has(key(target)), `${key(target)} must be manifest-declared`).toBe(true);
    }
  });

  it("pending bindings are not yet declared (tripwire for future contract waves)", () => {
    // The tier may be empty between waves (it is now, post-PR6); when a future
    // proposal tier exists, every entry must stay undeclared until it lands.
    for (const target of pending) {
      expect(
        declared.has(key(target)),
        `${key(target)} is now manifest-declared, flip it to "active" in client.ts`,
      ).toBe(false);
    }
  });

  it("keeps binding names unique across services", () => {
    const keys = all.map(key);
    expect(new Set(keys).size).toBe(keys.length);
  });
});

/**
 * The client catalog's automatic detection metadata has to match the backend.
 *
 * Explicit target ids render directly. `uaClass` exists only for the fallback
 * path where no explicit target was supplied, so the two bounded maps still
 * need to agree in both directions.
 */
describe("client target catalog", () => {
  // Mirrors uaClassTargets; keep in step with the Go map.
  const CORE_UA_CLASSES: Record<string, string> = {
    surge: "Surge",
    loon: "Loon",
    quantumultx: "QX",
    stash: "Stash",
    shadowrocket: "Shadowrocket",
    clashmeta: "ClashMeta",
    clash: "Clash",
    singbox: "sing-box",
    egern: "Egern",
  };

  it("declares unique target ids", () => {
    const ids = CONVERT_TARGETS.map((t) => t.id);
    expect(new Set(ids).size).toBe(ids.length);
  });

  it("maps declared UA fallbacks to the same explicit targets as the core", () => {
    for (const target of CONVERT_TARGETS) {
      if (!target.uaClass) continue;
      expect(CORE_UA_CLASSES[target.uaClass]).toBe(target.id);
    }
  });

  it("covers every bounded UA fallback the core can detect", () => {
    const covered = new Set(
      CONVERT_TARGETS.filter((t) => t.uaClass).map((t) => t.uaClass as string),
    );
    for (const uaClass of Object.keys(CORE_UA_CLASSES)) {
      expect(covered.has(uaClass)).toBe(true);
    }
  });
});

describe("share link construction", () => {
  // The serve path's URL contract (P-1): ?target= names the client, flags ride
  // along under upstream's exact names. These strings hit production URLs, so
  // they are pinned here rather than trusted to survive refactors.
  it("appends the explicit target", () => {
    expect(buildShareLink("https://host/sub/team/tok", "Stash", false)).toBe(
      "https://host/sub/team/tok?target=Stash",
    );
  });

  it("encodes targets that need it and adds flags only when set", () => {
    expect(buildShareLink("/sub/team/tok", "sing-box", true)).toBe(
      "/sub/team/tok?target=sing-box&includeUnsupportedProxy=1",
    );
  });

  it("chains onto a base that already carries a query", () => {
    expect(buildShareLink("/sub/team/tok?noFlow=1", "Surge", false)).toBe(
      "/sub/team/tok?noFlow=1&target=Surge",
    );
  });
});

/**
 * Core's reply types (bind.preview, its entries and exclusions, the plan
 * status row, the plan, the shares row) are generated from the server's JSON
 * tags by lattice-server's tools/tsgen and committed here as core-types.ts,
 * with the server commit in the header (s2-plan section 2.8). A hand copy is
 * how an earlier draft lost `label`, `node_id` and `clone`.
 *
 * The header always names a full commit and the banner always says the file
 * is generated. With LATTICE_SERVER_DIR naming a lattice-server checkout at
 * that commit, the generator runs and the committed bytes must equal its
 * output, so a hand edit or a stale regeneration fails here:
 *
 *   LATTICE_SERVER_DIR=../lattice-server npm test -- contract
 */
describe("core reply types", () => {
  const committed = readFileSync(new URL("./core-types.ts", import.meta.url), "utf8");
  const commit = /^\/\/ Source: lattice-server ([0-9a-f]{40})$/m.exec(committed)?.[1];

  it("is the generator's file and names the server commit it came from", () => {
    expect(committed.startsWith("// Code generated by lattice-server tools/tsgen")).toBe(true);
    expect(commit, "core-types.ts must be generated with -ref <full server commit>").toMatch(/^[0-9a-f]{40}$/);
  });

  it("carries the fields the bind preview list reads", () => {
    for (const field of ["label?: string;", "node_id?: string;", "clone?: number;", "identity_status: string;", "refused?: string;", "fix?: string;"]) {
      expect(committed).toContain(field);
    }
  });

  const server = process.env.LATTICE_SERVER_DIR;
  it.skipIf(!server)("equals the generator's output at that commit", () => {
    const dir = server!;
    const head = execFileSync("git", ["-C", dir, "rev-parse", "HEAD"], { encoding: "utf8" }).trim();
    expect(head, `${dir} is not at the commit core-types.ts names`).toBe(commit);
    const generated = execFileSync("go", ["run", "./tools/tsgen", "-ref", head], { cwd: dir, encoding: "utf8", env: process.env });
    expect(committed).toBe(generated);
  });
});
