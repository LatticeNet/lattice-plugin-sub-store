import { describe, expect, it } from "vitest";

import { validateDisplayName, validateSubscriptionName, validateSubscriptionUrl } from "./subscriptionsModel";

describe("validateSubscriptionName", () => {
  it("accepts Lattice-safe names", () => {
    expect(validateSubscriptionName("hk-providers").value).toBe("hk-providers");
    expect(validateSubscriptionName("a1._-x").value).toBe("a1._-x");
  });

  it("rejects empty and ill-formed names", () => {
    expect(validateSubscriptionName("").error).toBeTruthy();
    expect(validateSubscriptionName("   ").error).toBeTruthy();
    expect(validateSubscriptionName("-lead").error).toBeTruthy();
    expect(validateSubscriptionName("has space").error).toBeTruthy();
    expect(validateSubscriptionName("x".repeat(129)).error).toBeTruthy();
  });
});

describe("validateDisplayName", () => {
  it("is optional but bounded", () => {
    expect(validateDisplayName("").value).toBeUndefined();
    expect(validateDisplayName("  ").value).toBeUndefined();
    expect(validateDisplayName("HK premium").value).toBe("HK premium");
    expect(validateDisplayName("x".repeat(65)).error).toBeTruthy();
    expect(validateDisplayName("bad\tname").error).toBeTruthy();
  });
});

describe("validateSubscriptionUrl", () => {
  it("accepts absolute https URLs, query included", () => {
    const result = validateSubscriptionUrl("https://provider.example/sub?token=abc123");
    expect(result.error).toBeUndefined();
    expect(result.value).toContain("token=abc123");
  });

  it("accepts http only on loopback", () => {
    expect(validateSubscriptionUrl("http://127.0.0.1:8080/sub").error).toBeUndefined();
    expect(validateSubscriptionUrl("http://localhost/sub").error).toBeUndefined();
    expect(validateSubscriptionUrl("http://provider.example/sub").error).toBeTruthy();
  });

  it("rejects credentials, non-http schemes, and unparseable URLs", () => {
    expect(validateSubscriptionUrl("https://user:pass@provider.example/sub").error).toBeTruthy();
    expect(validateSubscriptionUrl("ftp://provider.example/sub").error).toBeTruthy();
    // WHATWG parsing normalizes "https:///x" to a valid URL; truly host-less input throws.
    expect(validateSubscriptionUrl("https://").error).toBeTruthy();
  });

  it("rejects malformed, over-long, and control-character input", () => {
    expect(validateSubscriptionUrl("not a url").error).toBeTruthy();
    expect(validateSubscriptionUrl(`https://provider.example/${"x".repeat(2048)}`).error).toBeTruthy();
    expect(validateSubscriptionUrl("https://provider.example/a\nb").error).toBeTruthy();
  });
});
