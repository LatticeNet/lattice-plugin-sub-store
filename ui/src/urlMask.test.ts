import { describe, expect, it } from "vitest";

import { hostsIn, maskUrl, maskUrlsIn, refreshFailureText } from "./urlMask";

describe("masking a provider link", () => {
  it("keeps the host and hides the query string", () => {
    expect(maskUrl("https://sub.example-provider.com/api/v1/client/subscribe?token=9f8e7d6c&flag=clash"))
      .toBe("https://sub.example-provider.com/…?…");
  });

  it("hides userinfo, path and fragment each on their own", () => {
    expect(maskUrl("https://user:secret@host.example:8443/path/to/sub")).toBe("https://…@host.example:8443/…");
    expect(maskUrl("https://host.example/sub#frag")).toBe("https://host.example/…#…");
    expect(maskUrl("https://host.example/?token=abc")).toBe("https://host.example?…");
  });

  it("leaves a bare origin as it is", () => {
    expect(maskUrl("https://host.example")).toBe("https://host.example");
    expect(maskUrl("https://host.example/")).toBe("https://host.example");
  });

  it("masks a non-URL whole and an empty value to nothing", () => {
    expect(maskUrl("not a url, maybe a token")).toBe("…");
    expect(maskUrl("mailto:someone@example.com")).toBe("…");
    expect(maskUrl("   ")).toBe("");
  });

  it("masks every link inside a sentence", () => {
    expect(maskUrlsIn('fetch of https://p.example/sub?token=1 failed and vless://uuid@a.example:443#n was skipped'))
      .toBe("fetch of https://p.example/…?… failed and vless://…@a.example:443#… was skipped");
    expect(maskUrlsIn("nothing to mask")).toBe("nothing to mask");
  });
});

describe("a refresh failure inside a sentence", () => {
  it("drops the engine's record id and keeps only the provider's host", () => {
    const raw = 'subscription "imported-openjobs-host-trojan" provider returned status 503 from https://sub.example-provider.com/api/v1/client/subscribe?token=9f8e7d6c5b4a3210';
    const text = refreshFailureText(raw);
    expect(text).toBe("provider returned status 503 from sub.example-provider.com");
    expect(text).not.toContain("imported-");
    expect(text).not.toContain("token");
  });

  it("leaves a reason without an id or a link as it is, and says nothing for none", () => {
    expect(refreshFailureText("connection reset by peer")).toBe("connection reset by peer");
    expect(refreshFailureText(undefined)).toBe("");
    expect(hostsIn("from https://user:pw@host.example:8443/x and file:///etc/passwd")).toBe("from host.example:8443 and the provider");
  });
});

describe("what the reviewer fed it", () => {
  it("cuts a scheme-less link inside quotes to its host", () => {
    const text = refreshFailureText('Get "sub.example.com/api?token=SECRET"');
    expect(text).toBe('Get "sub.example.com"');
    expect(text).not.toContain("SECRET");
  });

  it("cuts a scheme-less link with a path to its host", () => {
    expect(refreshFailureText("fetch sub.example.com/x/SECRET failed")).toBe("fetch sub.example.com failed");
  });

  it("does not print a quoted response body", () => {
    expect(refreshFailureText('body: {"token":"SECRET"}')).toBe("the response body is not shown");
    const after = refreshFailureText('provider returned status 401: {"error":"bad token SECRET"}');
    expect(after).toBe("provider returned status 401; the response body is not shown");
    expect(after).not.toContain("SECRET");
  });

  it("drops the engine's record id wherever it sits", () => {
    const text = refreshFailureText('refresh: subscription "abc123": provider returned status 503');
    expect(text).toBe("refresh: provider returned status 503");
    expect(text).not.toContain("abc123");
  });

  it("masks a scheme-less link after its host in a read view, as it masks one with a scheme", () => {
    expect(maskUrlsIn("fetch sub.example.com/x/SECRET?token=SECRET failed")).toBe("fetch sub.example.com/…?… failed");
    expect(maskUrlsIn("from https://sub.example.com/api?token=SECRET")).toBe("from https://sub.example.com/…?…");
    // A bare host carries no token and names the provider; it stays.
    expect(maskUrlsIn("dial sub.example.com:443 timed out")).toBe("dial sub.example.com:443 timed out");
  });
});
