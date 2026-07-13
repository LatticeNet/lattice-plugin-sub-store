import { describe, expect, it } from "vitest";

import { safeErrorMessage, statusLabel, validateCollection, validateEndpoint } from "./subStoreModel";

describe("Sub-Store endpoint validation", () => {
  it("accepts HTTPS and loopback HTTP secret paths", () => {
    expect(validateEndpoint(" https://sub.example.com/secret/ ").value).toBe("https://sub.example.com/secret");
    expect(validateEndpoint("http://127.0.0.1:3000/secret").value).toBe("http://127.0.0.1:3000/secret");
    expect(validateEndpoint("http://[::1]:3000/secret").value).toBe("http://[::1]:3000/secret");
  });

  it("rejects remote cleartext, missing secret paths, credentials and traversal", () => {
    expect(validateEndpoint("http://sub.example.com/secret").error).toMatch(/HTTPS/);
    expect(validateEndpoint("https://sub.example.com").error).toMatch(/secret path/);
    expect(validateEndpoint("https://user:pass@sub.example.com/secret").error).toMatch(/credentials/);
    expect(validateEndpoint("https://sub.example.com/%2e%2e/secret").error).toMatch(/unsafe segment/);
    expect(validateEndpoint("https://sub.example.com/secret%0aheader").error).toMatch(/unsafe segment/);
    expect(validateEndpoint("https://sub.example.com/secret?token=x").error).toMatch(/query/);
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

it("summarizes status without inventing success", () => {
  expect(statusLabel()).toBe("Not checked");
  expect(statusLabel({ reachable: true })).toBe("Reachable");
  expect(statusLabel({ reachable: false })).toBe("Unavailable");
});
