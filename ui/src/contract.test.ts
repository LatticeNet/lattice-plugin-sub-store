import { describe, expect, it } from "vitest";

import manifest from "../../manifest.json";
import { BINDINGS, CONVERT_TARGETS, SERVICES, type MethodBinding } from "./client";

/**
 * DoD gate: every UI data path must resolve to a manifest-declared method.
 * The UI speaks only `lattice.plugin.call`, and the host rejects anything the
 * signed manifest does not declare — so the binding table is checked against
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
    // It pushed nodes to an external Sub-Store — the direction this plugin
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
        `${key(target)} is now manifest-declared — flip it to "active" in client.ts`,
      ).toBe(false);
    }
  });

  it("keeps binding names unique across services", () => {
    const keys = all.map(key);
    expect(new Set(keys).size).toBe(keys.length);
  });
});

/**
 * The client sheet's capability split has to match the backend, not a wish.
 *
 * `render` picks the client from the core's bounded UA classification
 * (uaClassTargets in system-go/subscription_render.go). A target the UI marks
 * renderable but the core cannot select would offer a copy action that always
 * fails; a target the core CAN select but the UI leaves unmarked hides a
 * working path. Both directions are pinned here, so adding a client to one
 * side without the other fails the suite instead of production.
 */
describe("client target catalog", () => {
  // Mirrors uaClassTargets; keep in step with the Go map.
  const CORE_UA_CLASSES: Record<string, string> = {
    surge: "Surge",
    loon: "Loon",
    quantumultx: "QX",
    stash: "Stash",
    shadowrocket: "Shadowrocket",
    clash: "Clash",
    singbox: "sing-box",
    egern: "Egern",
  };

  it("declares unique target ids", () => {
    const ids = CONVERT_TARGETS.map((t) => t.id);
    expect(new Set(ids).size).toBe(ids.length);
  });

  it("only marks a target renderable when the core can select it", () => {
    for (const target of CONVERT_TARGETS) {
      if (!target.uaClass) continue;
      expect(CORE_UA_CLASSES[target.uaClass]).toBe(target.id);
    }
  });

  it("exposes every client the core can select", () => {
    const covered = new Set(
      CONVERT_TARGETS.filter((t) => t.uaClass).map((t) => t.uaClass as string),
    );
    for (const uaClass of Object.keys(CORE_UA_CLASSES)) {
      expect(covered.has(uaClass)).toBe(true);
    }
  });
});
