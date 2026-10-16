import { readFileSync } from "node:fs";

import { describe, expect, it } from "vitest";

import { KNOWN_ERROR_CODES } from "./client";
import { BIND_SHAPE_PROTOCOLS, PREDICATE_FIELDS, STRUCTURED_SORT_KEYS, opsOf } from "./fleet";

/**
 * The editor's copy of the structured vocabulary, held to the plugin's Go
 * source. The editor offers fields, operators and sort keys before any round
 * trip, so a field the plugin adds (or drops) must fail here rather than
 * reach an operator as a condition the save refuses, or never be offered at
 * all. The Go files are read as text: the table is a map literal whose lines
 * have one shape, and a reading that finds nothing fails on its own count.
 */
const GO = new URL("../../system-go/", import.meta.url);
const read = (path: string) => readFileSync(new URL(path, GO), "utf8");

interface GoField {
  kind: string;
  selector: string;
  probe: boolean;
  geo: boolean;
  ops: string[] | null;
}

function goPredicateFields(): Map<string, GoField> {
  const source = read("operators/structured.go");
  const start = source.indexOf("var predicateFields = map[string]fieldSpec{");
  const end = source.indexOf("\n}", start);
  expect(start, "predicateFields table").toBeGreaterThan(0);
  const out = new Map<string, GoField>();
  for (const line of source.slice(start, end).split("\n")) {
    const m = /^\s*"([a-z_0-9]+)":\s*\{(.*)\},?\s*$/.exec(line);
    if (!m) continue;
    const body = m[2]!;
    const kind = /kind:\s*kind(\w+)/.exec(body)?.[1]?.toLowerCase() ?? "";
    const selector = /selector:\s*"([a-z_]+)"/.exec(body)?.[1] ?? "";
    const ops = /ops:\s*\[\]string\{([^}]*)\}/.exec(body)?.[1];
    out.set(m[1]!, {
      kind,
      selector,
      probe: /probe:\s*true/.test(body),
      geo: /geo:\s*true/.test(body),
      ops: ops
        ? ops.split(",").map((op) => op.trim()).filter(Boolean).map((op) => ({ OpIn: "in", OpNotIn: "not_in", OpLTE: "lte", OpGTE: "gte", OpTrue: "true", OpFalse: "false", OpPresent: "present", OpAbsent: "absent" })[op] ?? op)
        : null,
    });
  }
  return out;
}

describe("the structured vocabulary", () => {
  const go = goPredicateFields();

  it("reads the plugin's table", () => {
    expect(go.size).toBeGreaterThan(20);
  });

  it("names exactly the plugin's predicate fields", () => {
    expect(Object.keys(PREDICATE_FIELDS).sort()).toEqual([...go.keys()].sort());
  });

  it("gives each field the plugin's kind, selector, probe and geo marks, and operators", () => {
    const kindOps: Record<string, string[]> = {
      text: ["in", "not_in", "present", "absent"],
      list: ["in", "not_in", "present", "absent"],
      number: ["lte", "gte", "present", "absent"],
      bool: ["true", "false"],
    };
    for (const [name, field] of go) {
      const ui = PREDICATE_FIELDS[name]!;
      expect(ui.kind, name).toBe(field.kind);
      expect(ui.selector ?? "", name).toBe(field.selector);
      expect(Boolean(ui.probe), name).toBe(field.probe);
      expect(Boolean(ui.geo), name).toBe(field.geo);
      expect([...opsOf(name)], name).toEqual(field.ops ?? kindOps[field.kind]);
    }
  });

  it("sorts by exactly the plugin's keys", () => {
    const source = read("operators/structured_sort.go");
    const block = source.slice(source.indexOf("var sortKeys = map[string]bool{"));
    const keys = [...block.slice(0, block.indexOf("\n}")).matchAll(/"([a-z_0-9]+)":\s*true/g)].map((m) => m[1]);
    expect(keys.length).toBeGreaterThan(3);
    expect([...STRUCTURED_SORT_KEYS].sort()).toEqual([...keys].sort());
  });

  it("knows exactly the protocols the fleet render can bind", () => {
    const source = read("subscription_fleet_render.go");
    const block = source.slice(source.indexOf("var fleetCredentialFields = map[string][]string{"));
    const protocols = [...block.slice(0, block.indexOf("\n}")).matchAll(/^\s*"([a-z0-9]+)":/gm)].map((m) => m[1]);
    expect(protocols.length).toBeGreaterThan(3);
    expect([...BIND_SHAPE_PROTOCOLS].sort()).toEqual([...protocols].sort());
  });
});

describe("the refusal codes", () => {
  it("cover every structured refusal a mutating method answers", () => {
    const source = read("store_staged.go");
    const block = source.slice(source.indexOf("refusedFleetToLegacy"), source.indexOf("\n)", source.indexOf("refusedFleetToLegacy")));
    const literal = [...block.matchAll(/=\s*"([a-z_]+)"/g)].map((m) => m[1]!);
    expect(literal.length).toBeGreaterThan(8);
    for (const code of literal) expect(KNOWN_ERROR_CODES.has(code), code).toBe(true);
  });

  it("cover every error code the fleet render and its readers lead with", () => {
    const source = read("subscription_fleet.go");
    const block = source.slice(source.indexOf("codeFleetEnvelopeMismatch"), source.indexOf("\n)", source.indexOf("codeFleetEnvelopeMismatch")));
    const codes = [...block.matchAll(/=\s*"([a-z_0-9]+)"/g)].map((m) => m[1]!);
    expect(codes.length).toBeGreaterThan(8);
    for (const code of codes) expect(KNOWN_ERROR_CODES.has(code), code).toBe(true);
  });
});
