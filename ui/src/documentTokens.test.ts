import { describe, expect, it } from "vitest";

import { MAX_RENDERED_LINES, tokenizeDocument, tokenizeLine } from "./documentTokens";
import type { Token } from "./documentTokens";

/** Colouring must never lose or reorder a character of the document. */
function joined(tokens: Token[]): string {
  return tokens.map((token) => token.text).join("");
}

function kinds(tokens: Token[], kind: string): string[] {
  return tokens.filter((token) => token.kind === kind).map((token) => token.text);
}

describe("document tokens", () => {
  const uri =
    "vless://0cd403ef-ce9b@beijing.aliyun.roobli.org:34099?security=reality&sni=www.bilibili.com#hs-sh-direct";

  it("reassembles every line exactly, whatever the language", () => {
    const lines = [
      uri,
      "# a comment",
      "proxies:",
      "  - name: 🇭🇰 Hong Kong 01",
      '{"name": "hk", "port": 443, "udp": true}',
      "const x = 1; // note",
      "[Proxy]",
      "HK = vless, a.example, 443",
      "",
      "   ",
    ];
    for (const language of ["plain", "yaml", "json", "javascript", "ini"] as const) {
      for (const line of lines) {
        expect(joined(tokenizeLine(line, language)), `${language}: ${line}`).toBe(line);
      }
    }
  });

  it("colours the node name and endpoint in a proxy URI, not the credential", () => {
    const tokens = tokenizeLine(uri, "plain");
    expect(kinds(tokens, "name")).toEqual(["hs-sh-direct"]);
    expect(kinds(tokens, "host")).toEqual(["beijing.aliyun.roobli.org:34099"]);
    expect(kinds(tokens, "scheme")).toEqual(["vless"]);
    // The userinfo is the secret. It stays in the baseline colour so the eye is
    // not led to it in a document an operator may be screen-sharing.
    for (const token of tokens) {
      if (token.text.includes("0cd403ef-ce9b")) expect(token.kind).toBe("plain");
    }
  });

  it("finds URIs inside documents that are not plain text", () => {
    // A produced YAML or conf document still carries share links, and they are
    // the lines an operator scans for.
    for (const language of ["yaml", "ini"] as const) {
      expect(kinds(tokenizeLine(uri, language), "name")).toEqual(["hs-sh-direct"]);
    }
  });

  it("separates keys from values without pretending to parse", () => {
    expect(kinds(tokenizeLine("  - name: hk-01", "yaml"), "key")).toEqual(["name"]);
    expect(kinds(tokenizeLine("  port: 443", "yaml"), "number")).toEqual(["443"]);
    expect(kinds(tokenizeLine("# note", "yaml"), "comment")).toEqual(["# note"]);

    const json = tokenizeLine('  "port": 443,', "json");
    expect(kinds(json, "key")).toEqual(['"port"']);
    expect(kinds(json, "number")).toEqual(["443"]);

    const conf = tokenizeLine("HK = vless, a.example", "ini");
    expect(kinds(conf, "key")).toEqual(["HK"]);
    expect(kinds(tokenizeLine("[Proxy]", "ini"), "key")).toEqual(["[Proxy]"]);
    expect(kinds(tokenizeLine("; note", "ini"), "comment")).toEqual(["; note"]);

    const js = tokenizeLine('const name = "hk"; // note', "javascript");
    expect(kinds(js, "string")).toEqual(['"hk"']);
    expect(kinds(js, "comment")).toEqual(["// note"]);
  });

  it("counts lines the way a reader does", () => {
    const parsed = tokenizeDocument("a\nb\nc\n", "plain");
    expect(parsed.total).toBe(3);
    expect(parsed.lines).toHaveLength(3);
    expect(parsed.hidden).toBe(0);
    // An empty document is one empty line, not zero and not two.
    expect(tokenizeDocument("", "plain").total).toBe(1);
  });

  it("says how much it is holding back instead of showing a silent prefix", () => {
    const huge = Array.from({ length: MAX_RENDERED_LINES + 25 }, (_, i) => `line ${i}`).join("\n");
    const parsed = tokenizeDocument(huge, "plain");
    expect(parsed.lines).toHaveLength(MAX_RENDERED_LINES);
    expect(parsed.total).toBe(MAX_RENDERED_LINES + 25);
    expect(parsed.hidden).toBe(25);
  });

  it("emits no colour role the stylesheet does not define", () => {
    const known = new Set([
      "plain", "comment", "key", "string", "number", "literal", "punct", "scheme", "host", "param", "name",
    ]);
    const samples = [uri, "proxies:", '{"a":1}', "const a = 1", "[S]", "# c", "x = y"];
    for (const language of ["plain", "yaml", "json", "javascript", "ini"] as const) {
      for (const line of samples) {
        for (const token of tokenizeLine(line, language)) {
          expect(known.has(token.kind), `${token.kind} has no rule`).toBe(true);
        }
      }
    }
  });
});
