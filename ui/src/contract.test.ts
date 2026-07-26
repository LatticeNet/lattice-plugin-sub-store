import { describe, expect, it } from "vitest";

import manifest from "../../manifest.json";
import { BINDINGS, SERVICES, type MethodBinding } from "./client";

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
  it("declares the shipped adapter service in the manifest", () => {
    expect([...declared].some((entry) => entry.startsWith(`${SERVICES.import}/`))).toBe(true);
  });

  it("every active binding is declared in the signed manifest", () => {
    expect(active.length).toBeGreaterThan(0);
    for (const target of active) {
      expect(declared.has(key(target)), `${key(target)} must be manifest-declared`).toBe(true);
    }
  });

  it("pending bindings are not yet declared (tripwire for the TASK-0002 contract)", () => {
    expect(pending.length).toBeGreaterThan(0);
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
