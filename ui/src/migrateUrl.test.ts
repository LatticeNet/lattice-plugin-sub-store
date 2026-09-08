import { describe, expect, it } from "vitest";

import { describeSubStoreBase, resolveSubStoreBase } from "./migrateUrl";

describe("resolveSubStoreBase", () => {
  it("unwraps the official frontend ?api= value into the backend URL", () => {
    expect(
      resolveSubStoreBase("http://127.0.0.1:19876/?api=http%3A%2F%2F127.0.0.1%3A19876%2Fsecret-path"),
    ).toBe("http://127.0.0.1:19876/secret-path");
  });

  it("leaves a backend URL with a secret path alone", () => {
    expect(resolveSubStoreBase("http://127.0.0.1:19876/secret-path")).toBe("http://127.0.0.1:19876/secret-path");
  });

  it("does not invent a path for a bare origin", () => {
    expect(resolveSubStoreBase("http://127.0.0.1:19876/")).toBe("http://127.0.0.1:19876/");
  });
});

describe("describeSubStoreBase", () => {
  it("keeps the origin and masks the secret path", () => {
    const got = describeSubStoreBase("http://127.0.0.1:19876/secret-path");
    expect(got).toEqual({ ok: true, origin: "http://127.0.0.1:19876", masked: "http://127.0.0.1:19876/…" });
  });

  it("accepts the frontend address bar by unwrapping ?api=", () => {
    const got = describeSubStoreBase("http://127.0.0.1:19876/?api=http://127.0.0.1:19876/secret-path");
    expect(got).toEqual({ ok: true, origin: "http://127.0.0.1:19876", masked: "http://127.0.0.1:19876/…" });
  });

  it("refuses an origin with no secret path", () => {
    const got = describeSubStoreBase("http://127.0.0.1:19876/");
    expect(got.ok).toBe(false);
    if (got.ok) return;
    expect(got.reason).toMatch(/origin is not enough/i);
  });

  it("does not echo the secret path in the confirmable origin", () => {
    const got = describeSubStoreBase("https://sub.example.com/this-is-the-secret");
    expect(got.ok).toBe(true);
    if (!got.ok) return;
    expect(got.origin).toBe("https://sub.example.com");
    expect(got.origin).not.toContain("secret");
    expect(got.masked).not.toContain("this-is-the-secret");
  });
});
