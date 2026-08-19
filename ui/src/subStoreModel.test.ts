import { describe, expect, it } from "vitest";

import { safeErrorMessage, statusLabel, validateCollection, validateEndpoint } from "./subStoreModel";

describe("Sub-Store endpoint validation", () => {
  it("accepts HTTPS and loopback HTTP secret paths", () => {
    expect(validateEndpoint(" https://sub.example.com/secret/ ").value).toBe("https://sub.example.com/secret");
    expect(validateEndpoint("http://127.0.0.1:3000/secret").value).toBe("http://127.0.0.1:3000/secret");
    expect(validateEndpoint("http://[::1]:3000/secret").value).toBe("http://[::1]:3000/secret");
    expect(validateEndpoint("secret://latticenet.sub-store/endpoint").value).toBe("secret://latticenet.sub-store/endpoint");
  });

  it("rejects remote cleartext, missing secret paths, credentials and traversal", () => {
    expect(validateEndpoint("http://sub.example.com/secret").error).toMatch(/HTTPS/);
    expect(validateEndpoint("https://sub.example.com").error).toMatch(/secret path/);
    expect(validateEndpoint("https://user:pass@sub.example.com/secret").error).toMatch(/credentials/);
    expect(validateEndpoint("https://sub.example.com/%2e%2e/secret").error).toMatch(/unsafe segment/);
    expect(validateEndpoint("https://sub.example.com/secret%0aheader").error).toMatch(/unsafe segment/);
    expect(validateEndpoint("https://sub.example.com/secret?token=x").error).toMatch(/query/);
    expect(validateEndpoint("secret://other.plugin/endpoint").error).toMatch(/secret reference/);
  });
});

it("validates collection names before bridge calls", () => {
  expect(validateCollection("managed.v2_1")).toBeUndefined();
  expect(validateCollection("../other")).toMatch(/must start/);
  expect(validateCollection("space name")).toMatch(/must start/);
});

it("redacts endpoint secrets from errors", () => {
  expect(safeErrorMessage(new Error("dial https://sub.example.com/very-secret: refused"), "failed"))
    .toBe("dial [endpoint] refused");
});

/**
 * V8 quotes the offending input back in a JSON.parse message, and what an
 * operator pastes into an operator argument is routinely a node URI whose
 * userinfo is the credential. The raw-argument editor shows that message, so
 * it has to survive the redactor with nothing readable left.
 */
it("redacts a node URI quoted back by a JSON parse failure", () => {
  // A short input is quoted whole: JSON.parse("ss://abc@h:1") reports
  // `Unexpected token 's', "ss://abc@h:1" is not valid JSON`, credential and
  // all. A long one is cut after ten characters, which still carries the
  // scheme and the start of the secret.
  const message = (input: string): string => {
    try {
      JSON.parse(input);
    } catch (cause) {
      return safeErrorMessage(cause, "This is not valid JSON.");
    }
    throw new Error("that input parsed");
  };
  expect(message("ss://abc@h:1")).toBe('Unexpected token \'s\', "[endpoint]" is not valid JSON');
  expect(message("trojan://hunter2@example.com:443")).not.toContain("hunter2");
});

it("summarizes status without inventing success", () => {
  expect(statusLabel()).toBe("Not checked");
  expect(statusLabel({ reachable: true })).toBe("Reachable");
  expect(statusLabel({ reachable: false })).toBe("Unavailable");
});
