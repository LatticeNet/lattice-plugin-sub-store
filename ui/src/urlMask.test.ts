import { describe, expect, it } from "vitest";

import { maskUrl, maskUrlsIn } from "./urlMask";

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
