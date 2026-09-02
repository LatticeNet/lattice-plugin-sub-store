import { describe, expect, it } from "vitest";

import type { SubStoreShareRow, SubscriptionListItem } from "./client";
import { publishStateFor, refreshStateFor, shareStateOf } from "./shareState";

const NOW = Date.parse("2026-09-02T05:00:00Z");
const share = (over: Partial<SubStoreShareRow>): SubStoreShareRow => ({
  subscription_id: "cdcd-self-host", share_id: "sh-1", slug: "cd-self", enabled: true, path: "/sub/cd-self/tok", ...over,
});
const item = (over: Partial<SubscriptionListItem>): SubscriptionListItem => ({
  id: "r", kind: "subscription", name: "r", has_url: false, has_inline_content: true, step_count: 0, disabled_step_count: 0, imported: false, ...over,
} as SubscriptionListItem);

describe("publishStateFor", () => {
  it("says the list is unread, then not published, then names the live slug", () => {
    expect(publishStateFor(undefined, "cdcd-self-host").label).toBe("—");
    expect(publishStateFor([], "cdcd-self-host")).toMatchObject({ tone: "neutral", label: "not published" });
    const live = publishStateFor([share({}), share({ share_id: "sh-2", slug: "cd-self-2", subscription_id: "other" })], "cdcd-self-host", NOW);
    expect(live).toMatchObject({ tone: "ok", label: "/cd-self", slug: "cd-self" });
    expect(live.shares).toHaveLength(1);
  });

  it("warns when every share is disabled or expired, and prefers a live one when any is", () => {
    expect(publishStateFor([share({ enabled: false })], "cdcd-self-host", NOW)).toMatchObject({ tone: "warn", label: "/cd-self disabled" });
    expect(publishStateFor([share({ expires_at: "2026-09-01T00:00:00Z" })], "cdcd-self-host", NOW)).toMatchObject({ tone: "warn", label: "/cd-self expired" });
    const mixed = publishStateFor([share({ enabled: false }), share({ share_id: "sh-2", slug: "cd-self-live" })], "cdcd-self-host", NOW);
    expect(mixed).toMatchObject({ tone: "ok", label: "/cd-self-live" });
    expect(mixed.title).toBe("Served at /sub/cd-self/tok.");
  });
});

describe("refreshStateFor", () => {
  it("reports n/a for anything that is not a provider link", () => {
    expect(refreshStateFor(item({ has_url: false }), NOW)).toMatchObject({ tone: "neutral", label: "n/a" });
    expect(refreshStateFor(item({ has_url: false, last_fetch_at: "2026-09-02T04:00:00Z", last_fetch_ok: true }), NOW).label).toBe("n/a");
  });

  it("keeps the failure, unknown outcome, and success wording for provider links", () => {
    expect(refreshStateFor(item({ has_url: true }), NOW)).toMatchObject({ tone: "neutral", label: "Never refreshed" });
    expect(refreshStateFor(item({ has_url: true, last_fetch_at: "2026-09-02T04:00:00Z", last_fetch_ok: false, last_error: "403" }), NOW))
      .toMatchObject({ tone: "danger", title: "403" });
    expect(refreshStateFor(item({ has_url: true, last_fetch_at: "2026-09-02T04:00:00Z" }), NOW).tone).toBe("neutral");
    expect(refreshStateFor(item({ has_url: true, last_fetch_at: "2026-09-02T04:00:00Z", last_fetch_ok: true }), NOW).tone).toBe("ok");
  });
});

describe("one share's own verdict", () => {
  const now = Date.parse("2026-09-02T04:00:00Z");
  const base = { subscription_id: "s", share_id: "x", slug: "cd", path: "/sub/cd/tok" };

  it("is live only when enabled and not past its expiry", () => {
    expect(shareStateOf({ ...base, enabled: true }, now)).toMatchObject({ tone: "ok", label: "live" });
    expect(shareStateOf({ ...base, enabled: true, expires_at: "2026-12-01T00:00:00Z" }, now).label).toBe("live");
    expect(shareStateOf({ ...base, enabled: false }, now)).toMatchObject({ tone: "warn", label: "disabled" });
    expect(shareStateOf({ ...base, enabled: true, expires_at: "2026-09-01T00:00:00Z" }, now)).toMatchObject({ tone: "warn", label: "expired" });
    // Expired wins over disabled: the expiry is the reason a client gets nothing.
    expect(shareStateOf({ ...base, enabled: false, expires_at: "2026-09-01T00:00:00Z" }, now).label).toBe("expired");
  });
});
