import { describe, expect, it } from "vitest";

import { applyRewrite, offerState, regexDiagnostics, regexIncompatible, rewriteNegativeLookahead, stepPatterns, type RegexDiagnostic } from "./regexRewrite";

describe("what the native engine cannot run", () => {
  it("is lookaround, an atomic group and a backreference", () => {
    for (const pattern of ["^(?!.*HK).*$", "a(?=b)", "(?<=x)y", "(?<!x)y", "(?>ab)c", "(\\w)\\1"]) {
      expect(regexIncompatible(pattern), pattern).toBe(true);
    }
  });

  it("is not a named group, a non-capturing group, an escape or a class that only looks like one", () => {
    for (const pattern of ["(?:HK|JP)", "(?P<name>x)", "(?i)hk", "\\(\\?!", "[(?!]", "[\\]1]", "香港|HK", "\\d+"]) {
      expect(regexIncompatible(pattern), pattern).toBe(false);
    }
  });
});

describe("the rewrite the engine offers", () => {
  it("turns keep-everything-except into the drop-mode pattern", () => {
    expect(rewriteNegativeLookahead("^(?!.*(过期|剩余|官网)).*$")).toBe("过期|剩余|官网");
    expect(rewriteNegativeLookahead("^(?!.*(?:HK|JP)).*$")).toBe("HK|JP");
    expect(rewriteNegativeLookahead("^(?!.*expire).*$")).toBe("expire");
    // Spaces around it are the operator's typing, not the pattern.
    expect(rewriteNegativeLookahead("  ^(?!.*HK).*$ ")).toBe("HK");
  });

  it("offers nothing for any other shape", () => {
    for (const pattern of ["(?!.*HK).*", "^(?!HK).*$", "^(?=.*HK).*$", "^(?!.*(HK).*$", "^(?!.*(?!JP)).*$", "^(?!.*\\1).*$"]) {
      expect(rewriteNegativeLookahead(pattern), pattern).toBe("");
    }
  });
});

describe("diagnostics over a chain", () => {
  const chain = [
    { type: "Quick Setting Operator", args: { udp: true } },
    { type: "Regex Filter", args: { regex: ["^(?!.*(过期|官网)).*$"], keep: true } },
    { type: "Regex Rename Operator", args: { value: [{ expr: "(\\w+)-\\1", now: "$1" }] } },
    { type: "Regex Filter", args: { regex: ["^(?!.*HK).*$"], keep: true }, disabled: true },
    { type: "Regex Filter", args: { regex: ["^(?!.*JP).*$", "SG"], keep: true } },
    { type: "Regex Filter", args: { value: ["^(?!.*TW).*$"], keep: false } },
  ];

  it("names each refused pattern by its place in the whole chain, skipping disabled steps", () => {
    expect(regexDiagnostics(chain)).toEqual([
      { step: 2, type: "Regex Filter", pattern: "^(?!.*(过期|官网)).*$", rewrite: "过期|官网" },
      { step: 3, type: "Regex Rename Operator", pattern: "(\\w+)-\\1", rewrite: "" },
      // Beside another pattern a drop filter would change what the other keeps.
      { step: 5, type: "Regex Filter", pattern: "^(?!.*JP).*$", rewrite: "" },
      // Already a drop filter: a negative lookahead there has no keep idiom to undo.
      { step: 6, type: "Regex Filter", pattern: "^(?!.*TW).*$", rewrite: "" },
    ]);
  });

  it("reads each operator's own argument for its patterns", () => {
    expect(stepPatterns({ type: "Regex Filter", args: { regex: ["a", "b"] } })).toEqual(["a", "b"]);
    expect(stepPatterns({ type: "Regex Filter", args: { value: ["a"] } })).toEqual(["a"]);
    expect(stepPatterns({ type: "Regex Delete Operator", args: { value: ["x"] } })).toEqual(["x"]);
    expect(stepPatterns({ type: "Regex Sort Operator", args: { value: ["x", "y"] } })).toEqual(["x", "y"]);
    expect(stepPatterns({ type: "Regex Rename Operator", args: { value: [{ expr: "e", now: "n" }] } })).toEqual(["e"]);
    expect(stepPatterns({ type: "Sort Operator", args: { value: "asc" } })).toEqual([]);
    expect(stepPatterns(null)).toEqual([]);
  });

  it("applies a rewrite to its step alone, keeping the step's other fields", () => {
    const stored = [{ type: "Regex Filter", customName: "Drop the noise", id: "s1", args: { value: ["^(?!.*HK).*$"], keep: true } }, { type: "Sort Operator" }];
    const [diagnostic] = regexDiagnostics(stored);
    const next = applyRewrite(stored, diagnostic!);
    expect(next[0]).toEqual({ type: "Regex Filter", customName: "Drop the noise", id: "s1", args: { regex: ["HK"], keep: false } });
    expect(next[1]).toBe(stored[1]);
    expect(regexDiagnostics(next)).toEqual([]);
    // A diagnostic that no longer matches its step changes nothing.
    expect(applyRewrite(next, diagnostic!)).toEqual(next);
  });
});

describe("what the editor's notice says", () => {
  const named: RegexDiagnostic = { step: 1, type: "Regex Filter", pattern: "^(?!.*HK).*$", rewrite: "HK" };

  it("lists the patterns while the chain has any, refused or not", () => {
    expect(offerState([named], null, false)).toBe("patterns");
    expect(offerState([named], { diagnostics: [named] }, true)).toBe("patterns");
  });

  it("says they are gone after a refusal that named them, or a rewrite applied from it", () => {
    expect(offerState([], { diagnostics: [named] }, false)).toBe("resolved-refusal");
    expect(offerState([], null, true)).toBe("resolved-rewrite");
  });

  it("says nothing for a chain that never had one, nor for a refusal that named no pattern", () => {
    expect(offerState([], null, false)).toBe("none");
    expect(offerState([], { diagnostics: [] }, false)).toBe("none");
  });
});
