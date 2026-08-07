import { describe, expect, it } from "vitest";

import {
  defaultArgs,
  fromWireArgs,
  schemaFor,
  toWireArgs,
  OPERATOR_SCHEMAS,
} from "./operatorSchema";

/**
 * These pin each operator's arguments to what the bundled engine's constructor
 * actually destructures. The schemas were originally written from the operator
 * names, and most of them were wrong — a regex filter whose patterns were
 * stored under `value` left the engine with `regex = []`, and in keep mode that
 * drops every node rather than keeping them.
 *
 * The expectations below are transcribed from the engine, not from the docs:
 *
 *   Regex Filter          function XW({regex = [], keep = true})
 *   Conditional Filter    function DW({rule})
 *   Flag Operator         function BW({mode, tw})
 *   Regex Rename Operator function lI(e)  → for (const {expr, now} of e)
 *   Regex Delete Operator function xW(e)  → e.map(r => ({expr: r, now: ""}))
 *   Sort Operator         function $W(e = "asc") → switch (e)
 *   Regex Sort Operator   function PW(e)  → e.order / e.expressions, or bare
 *   Resolve Domain        function mI({provider, type, filter, cache, …})
 */
describe("operator arguments match the engine's contract", () => {
  const keysOf = (type: string) => (schemaFor(type)?.fields ?? []).map((f) => f.key);

  it("names a regex filter's patterns `regex`", () => {
    expect(keysOf("Regex Filter")).toContain("regex");
    expect(keysOf("Regex Filter")).not.toContain("value");
  });

  it("names a conditional filter's tree `rule`", () => {
    expect(keysOf("Conditional Filter")).toEqual(["rule"]);
  });

  it("names the flag operator's setting `mode`", () => {
    expect(keysOf("Flag Operator")).toContain("mode");
  });

  it("names the domain resolver's family `type`, not `mode`", () => {
    expect(keysOf("Resolve Domain Operator")).toContain("type");
    expect(keysOf("Resolve Domain Operator")).not.toContain("mode");
  });

  // Region and Type filters accept `e?.value || e`, so the wrapper is fine.
  // OW({sourceType, sourceName, position}) — `value` matched nothing, so the
  // operator resolved no source at all.
  it("names the appended source the way the engine reads it", () => {
    const keys = keysOf("Add Proxies From Subscription Operator");
    expect(keys).toEqual(["sourceType", "sourceName", "position"]);
  });

  // IW({action, template, link, position, field}) — `field` defaults to
  // ["name"], so omitting it from the form is a choice rather than a gap.
  it("covers the duplicate handler's arguments", () => {
    const keys = keysOf("Handle Duplicate Operator");
    for (const key of ["action", "template", "link", "position"]) {
      expect(keys).toContain(key);
    }
  });

  it("keeps the wrapper where the engine accepts one", () => {
    expect(keysOf("Region Filter")).toEqual(["value", "keep"]);
    expect(keysOf("Type Filter")).toEqual(["value", "keep"]);
  });
});

describe("bare operators are handed their value directly", () => {
  it("sorts with a string, not an object", () => {
    expect(toWireArgs("Sort Operator", { value: "desc" })).toBe("desc");
  });

  it("renames with an array of {expr, now}", () => {
    const wire = toWireArgs("Regex Rename Operator", {
      value: [{ expr: "^HK", now: "Hong Kong" }],
    });
    expect(wire).toEqual([{ expr: "^HK", now: "Hong Kong" }]);
  });

  it("deletes with an array of patterns", () => {
    expect(toWireArgs("Regex Delete Operator", { value: ["cn"] })).toEqual(["cn"]);
  });

  it("leaves object operators alone", () => {
    const args = { regex: ["a"], keep: true };
    expect(toWireArgs("Regex Filter", args)).toEqual(args);
  });
});

describe("stored steps load back into the editor", () => {
  it("reads a bare value into its field", () => {
    expect(fromWireArgs("Sort Operator", "asc")).toEqual({ value: "asc" });
    expect(fromWireArgs("Regex Delete Operator", ["cn"])).toEqual({ value: ["cn"] });
  });

  // Records written before the shapes were understood are still openable, and
  // saving one writes the corrected shape.
  it("recovers a value from the old wrapper", () => {
    expect(fromWireArgs("Sort Operator", { value: "desc" })).toEqual({ value: "desc" });
    expect(toWireArgs("Sort Operator", fromWireArgs("Sort Operator", { value: "desc" }))).toBe("desc");
  });

  it("round-trips an object operator unchanged", () => {
    const args = { regex: ["a"], keep: false };
    expect(fromWireArgs("Regex Filter", args)).toEqual(args);
  });

  it("survives an operator it has never heard of", () => {
    expect(fromWireArgs("Made Up Operator", { anything: 1 })).toEqual({ anything: 1 });
    expect(toWireArgs("Made Up Operator", { anything: 1 })).toEqual({ anything: 1 });
  });
});

// A default that does not match the wire shape produces a step that is broken
// the moment it is added, before the operator has touched it.
describe("defaults survive the wire conversion", () => {
  it("every schema's defaults convert without losing the field", () => {
    for (const schema of OPERATOR_SCHEMAS) {
      const wire = toWireArgs(schema.type, defaultArgs(schema.type));
      if (schema.wire === "bare") {
        expect(Array.isArray(wire) || typeof wire === "string" || wire === undefined).toBe(true);
      } else {
        expect(typeof wire === "object" || wire === undefined).toBe(true);
      }
    }
  });
});
