import { describe, expect, it } from "vitest";
import { ref } from "vue";
import type { BridgeClient } from "@latticenet/plugin-bridge";

import { BINDINGS, KIND_COLLECTION, SOURCE_FLEET, SOURCE_REMOTE, STORE_VERSION_LEGACY, type SubscriptionRecord } from "./client";
import { refusalMessage, type FleetOptions } from "./fleet";
import type { HostContext } from "./host";
import { t } from "./i18n";
import { useRecordEditor } from "./useRecordEditor";
import { emptyDraft, useSubscriptions } from "./useSubscriptions";

/**
 * What the record editor sends when it saves a fleet record or a combination
 * (design 28 S2, lane 6a), and how it reads a write the store refused. The
 * plugin writes the record it is sent, so a save that leaves out the binding
 * rules removes them, and since S2 a refusal arrives as a successful
 * `{saved: false, refused}` reply (s2-plan section 2.6), never as an error.
 */

const key = (binding: { service: string; method: string }) => `${binding.service}/${binding.method}`;
const SAVE = key(BINDINGS.subSave);
const GET = key(BINDINGS.subGet);
const LIST = key(BINDINGS.subList);

type Answer = unknown | ((payload: unknown) => unknown);

function fleetHost(answers: Record<string, Answer>) {
  const calls: { method: string; payload: unknown }[] = [];
  const bridge = {
    call(service: string, method: string, payload: unknown) {
      calls.push({ method, payload });
      // The host structured-clones what leaves the frame; a Vue proxy cannot be.
      structuredClone(payload);
      const answer = answers[`${service}/${method}`];
      const value = typeof answer === "function" ? (answer as (p: unknown) => unknown)(payload) : answer;
      return { promise: Promise.resolve(value), cancel: () => {} };
    },
  } as unknown as BridgeClient;
  const host: HostContext = {
    bridge,
    init: ref(undefined),
    bootError: ref(""),
    available: () => true,
    resize: async () => {},
    pageState: ref({}),
    sendState: () => {},
  };
  const saved = () => calls.filter((call) => call.method === "save").map((call) => (call.payload as { subscription: SubscriptionRecord }).subscription);
  return { host, calls, saved };
}

const RULES: FleetOptions = {
  probe_exclusion: { enabled: true },
  usage_exclusion: { enabled: true, max_bytes_per_line: 50 * 1024 ** 3 },
};

const ASIA: SubscriptionRecord = {
  id: "asia-fleet",
  name: "asia-fleet",
  display_name: "亚洲线路 Asia lines",
  source: SOURCE_FLEET,
  process: [{ type: "Structured Filter", args: { mode: "keep", match: "all", predicates: [{ field: "country", op: "in", values: ["HK", "JP"] }] } }],
  fleet: RULES,
  revision: "rev-asia-fleet-1",
};

const COMBINATION: SubscriptionRecord = {
  id: "asia-combination",
  kind: KIND_COLLECTION,
  name: "asia-combination",
  members: ["asia-fleet", "openjobs-host"],
  failure_mode: "strict",
  fleet: { ddns_dial: true },
  revision: "rev-asia-combination-1",
};

function editorOver(stored: SubscriptionRecord, extra: Record<string, Answer> = {}) {
  const env = fleetHost({
    [GET]: { subscription: stored },
    [SAVE]: (payload: unknown) => ({ saved: true, subscription: { ...(payload as { subscription: SubscriptionRecord }).subscription, revision: "rev-2" } }),
    [LIST]: { subscriptions: [] },
    ...extra,
  });
  const subs = useSubscriptions(env.host);
  const editor = useRecordEditor({ host: env.host, subs, clearListState: () => {}, onSaved: () => {} });
  return { ...env, subs, editor };
}

describe("a fleet record's binding rules on save", () => {
  it("keeps the stored rules through an edit to something else", async () => {
    const { editor, saved } = editorOver(ASIA);
    await editor.startEdit(ASIA.id);
    editor.draft.value.displayName = "Asia lines, renamed";
    await editor.submit();
    expect(saved()).toHaveLength(1);
    expect(saved()[0]!.display_name).toBe("Asia lines, renamed");
    expect(saved()[0]!.fleet).toEqual(RULES);
  });

  it("sends the rules the operator changed", async () => {
    const { editor, saved } = editorOver(ASIA);
    await editor.startEdit(ASIA.id);
    editor.fleet.setRules({ ddns_dial: true, probe_exclusion: { enabled: true, consecutive_failures: 5 } });
    await editor.submit();
    expect(saved()[0]!.fleet).toEqual({ ddns_dial: true, probe_exclusion: { enabled: true, consecutive_failures: 5 } });
  });

  it("sends a combination's own rules", async () => {
    const { editor, saved } = editorOver(COMBINATION);
    await editor.startEdit(COMBINATION.id);
    editor.draft.value.remark = "Asia, with the provider as a fallback";
    await editor.submit();
    expect(saved()[0]!.fleet).toEqual({ ddns_dial: true });
  });

  it("sends a new fleet record's rules, and none once every rule is off", async () => {
    const { host, saved } = fleetHost({ [SAVE]: { saved: true, subscription: {} }, [LIST]: { subscriptions: [] } });
    const subs = useSubscriptions(host);
    const draft = { ...emptyDraft(), name: "Japan lines", source: SOURCE_FLEET, fleet: { ddns_dial: true } as FleetOptions };
    expect(await subs.save(draft)).toBe(true);
    expect(saved()[0]!.fleet).toEqual({ ddns_dial: true });
    expect(await subs.save({ ...draft, fleet: { ddns_dial: false } })).toBe(true);
    expect(saved()[1]!.fleet).toBeUndefined();
  });

  it("sends no rules for a provider record", async () => {
    const { host, saved } = fleetHost({ [SAVE]: { saved: true, subscription: {} }, [LIST]: { subscriptions: [] } });
    const subs = useSubscriptions(host);
    const draft = { ...emptyDraft(), name: "Provider", source: SOURCE_REMOTE, url: "https://sub.example-provider.com/s", fleet: RULES };
    expect(await subs.save(draft)).toBe(true);
    expect(saved()[0]!.fleet).toBeUndefined();
  });
});

describe("a write the store refused", () => {
  const refusedWith = (refused: Record<string, unknown>) => ({ saved: false, refused });

  it("says why beside Save, from the structured reply, with the records it names", async () => {
    const { editor, subs } = editorOver(COMBINATION, {
      [SAVE]: refusedWith({
        code: "fleet_mixed_owner_credentials",
        ids: ["asia-fleet", "self-export"],
        collections: ["asia-combination"],
        message: 'collection "asia-combination" would gather the fleet records asia-fleet and the legacy records self-export',
      }),
    });
    await editor.startEdit(COMBINATION.id);
    editor.draft.value.members = [...editor.draft.value.members, "self-export"];
    await editor.submit();
    const words = t.fleetEditor.refusal;
    expect(subs.actionError.value).toBe(words.notSaved(`${words.codes.fleet_mixed_owner_credentials} ${words.records("asia-fleet, self-export")}`));
    expect(subs.actionError.value).not.toBe(t.subs.saveUnconfirmed);
    // The editor stays open on the refused draft.
    expect(editor.editing.value).toBe(true);
  });

  it("offers the regex rewrite when the refusal is regex_incompatible", async () => {
    const { host } = fleetHost({
      [SAVE]: refusedWith({ code: "regex_incompatible", ids: ["provider"], message: 'step 1 pattern "^(?!.*expired).*$" needs lookaround, which RE2 cannot compile' }),
    });
    const subs = useSubscriptions(host);
    const draft = {
      ...emptyDraft(),
      name: "provider",
      source: SOURCE_REMOTE,
      url: "https://sub.example-provider.com/s",
      process: [{ type: "Regex Filter", args: { regex: ["^(?!.*expired).*$"], keep: true } }],
    };
    expect(await subs.save(draft)).toBe(false);
    expect(subs.saveRefusal.value?.code).toBe("regex_incompatible");
    expect(subs.saveRefusal.value?.diagnostics.length).toBeGreaterThan(0);
    expect(subs.actionError.value).toBe(t.subs.regexRefused([1]));
  });

  it("asks for the store migration when the refusal is store_migration_required", async () => {
    const { host } = fleetHost({
      [SAVE]: refusedWith({ code: "store_migration_required", message: "the subscription store still holds the legacy single document; run migrate_store until it reports done, then retry" }),
    });
    const subs = useSubscriptions(host);
    expect(await subs.save({ ...emptyDraft(), name: "Japan lines", source: SOURCE_FLEET })).toBe(false);
    expect(subs.actionError.value).toBe(t.subs.legacyRefusal);
    expect(subs.storeVersion.value).toBe(STORE_VERSION_LEGACY);
  });

  it("shows the plugin's fixed message for a code it has no sentence for", async () => {
    const { host } = fleetHost({ [SAVE]: refusedWith({ code: "fleet_new_rule", ids: ["japan-lines"], message: "record \"japan-lines\" was refused by a rule" }) });
    const subs = useSubscriptions(host);
    expect(await subs.save({ ...emptyDraft(), name: "Japan lines", source: SOURCE_FLEET })).toBe(false);
    expect(subs.actionError.value).toBe(t.fleetEditor.refusal.notSaved('record "japan-lines" was refused by a rule'));
  });

  it("says why a copy was refused", async () => {
    const legacy: SubscriptionRecord = { id: "self-export", name: "self-export", source: "vpn-core", process: [] };
    const { host } = fleetHost({
      [GET]: { subscription: legacy },
      [SAVE]: refusedWith({ code: "legacy_source_retired", ids: ["self-export-copy"], message: 'record "self-export-copy" cannot take the vpn-core source' }),
    });
    const subs = useSubscriptions(host);
    expect(await subs.duplicate("self-export")).toBeNull();
    expect(subs.actionError.value).toBe(t.fleetEditor.refusal.notSaved(t.fleetEditor.refusal.codes.legacy_source_retired));
    expect(subs.actionError.value).not.toBe(t.subs.copyUnconfirmed);
  });
});

describe("refusalMessage", () => {
  it("leaves out the record being written and names files and revisions", () => {
    const words = t.fleetEditor.refusal;
    expect(refusalMessage({ code: "fleet_file_unavailable", ids: ["asia-fleet"], files: ["clash-config", "asia-fleet"], message: "m" }, "asia-fleet"))
      .toBe(words.notSaved(`${words.codes.fleet_file_unavailable} ${words.files("clash-config")}`));
    expect(refusalMessage({ code: "promotion_pending", ids: ["asia-fleet"], staged_revision: "rev-3f1a", message: "m" }, "asia-fleet"))
      .toBe(words.notSaved(`${words.codes.promotion_pending} ${words.revision("rev-3f1a")}`));
  });

  it("names records the way the Records table does, and an unlisted one by its id", () => {
    const words = t.fleetEditor.refusal;
    const records = [
      { id: "asia-fleet", name: "asia-fleet", display_name: "亚洲线路 Asia lines" },
      { id: "self-export", name: "self-export" },
    ];
    expect(refusalMessage({ code: "fleet_mixed_owner_credentials", ids: ["asia-fleet", "self-export", "gone"], collections: ["asia-combination"], message: "m" }, "asia-combination", records))
      .toBe(words.notSaved(`${words.codes.fleet_mixed_owner_credentials} ${words.records("亚洲线路 Asia lines, self-export, gone")}`));
  });

  it("redacts a link in a message it shows as written", () => {
    expect(refusalMessage({ code: "unknown_code", message: "fetch https://sub.example.com/s?token=1 refused" })).toBe(t.fleetEditor.refusal.notSaved("fetch [endpoint] refused"));
  });
});
