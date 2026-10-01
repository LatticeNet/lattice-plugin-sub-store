import { describe, expect, it } from "vitest";

import { KIND_COLLECTION, KIND_FILE, KIND_SUB, MAX_SUBSCRIPTION_RECORDS } from "./client";
import {
  LIMIT_REASON,
  NO_SOURCE_REASON,
  READING_REASON,
  UNREAD_REASON,
  createBlocks,
  headerCreate,
  storeBlock,
  type CatalogueView,
  type HeaderCreateInput,
} from "./createGate";
import type { LoadState } from "./useSubscriptions";
import type { ViewId } from "./pipeline";

const STORE = [{ kind: KIND_SUB }, { kind: KIND_SUB }, { kind: KIND_COLLECTION }, { kind: KIND_FILE }];

function catalogue(state: LoadState, records: readonly { kind?: string }[] = STORE, failed = false): CatalogueView {
  return { state, failed, records };
}

function header(tab: ViewId, overrides: Partial<HeaderCreateInput> = {}) {
  return headerCreate({
    tab,
    catalogue: catalogue("ready"),
    caps: { ready: true, mutate: true },
    covered: false,
    ...overrides,
  });
}

const CREATING: readonly ViewId[] = ["overview", "sources", "combinations", "files"];

describe("whether the store can take a new record", () => {
  it("cannot while the catalogue is unread, failed or still being read", () => {
    expect(storeBlock(catalogue("error"))).toBe(UNREAD_REASON);
    expect(storeBlock(catalogue("loading"))).toBe(READING_REASON);
    expect(storeBlock(catalogue("idle"))).toBe(READING_REASON);
    // A retry after a failure is a read in flight, not a failure.
    expect(storeBlock(catalogue("loading", STORE, true))).toBe(READING_REASON);
  });

  it("can once read, until the record budget is spent", () => {
    expect(storeBlock(catalogue("ready"))).toBe("");
    const full = Array.from({ length: MAX_SUBSCRIPTION_RECORDS }, () => ({ kind: KIND_SUB }));
    expect(storeBlock(catalogue("ready", full))).toBe(LIMIT_REASON);
    expect(storeBlock(catalogue("ready", full.slice(1)))).toBe("");
  });

  it("gives a combination its own reason when there is nothing to combine, after unread and before the limit", () => {
    const noSources = [{ kind: KIND_FILE }, { kind: KIND_COLLECTION }];
    expect(createBlocks(catalogue("ready", noSources))).toEqual({
      "new-subscription": "",
      "new-collection": NO_SOURCE_REASON,
      "new-file": "",
    });
    expect(createBlocks(catalogue("error", noSources))["new-collection"]).toBe(UNREAD_REASON);
    expect(createBlocks(catalogue("loading", noSources))["new-collection"]).toBe(READING_REASON);
    const fullOfFiles = Array.from({ length: MAX_SUBSCRIPTION_RECORDS }, () => ({ kind: KIND_FILE }));
    expect(createBlocks(catalogue("ready", fullOfFiles))["new-collection"]).toBe(NO_SOURCE_REASON);
    // A record with no kind is a subscription, as the catalogue stores old ones.
    expect(createBlocks(catalogue("ready", [{}]))["new-collection"]).toBe("");
  });
});

describe("the header's create action", () => {
  it("names each layer's own verb, the Overview's as a split button", () => {
    expect(header("overview")).toMatchObject({ command: "new-subscription", label: "New source", menu: true, disabled: false });
    expect(header("sources")).toMatchObject({ command: "new-subscription", label: "New source", menu: false });
    expect(header("combinations")).toMatchObject({ command: "new-collection", label: "New combination", menu: false });
    expect(header("files")).toMatchObject({ command: "new-file", label: "New file", menu: false });
    expect(header("files")?.title).toBe("A document served as it is, with its proxy list kept in step");
    expect(header("shares")).toBeNull();
    expect(header("settings")).toBeNull();
  });

  it("is absent for a session that may not create, before the handshake, and over a record page or an editor", () => {
    for (const tab of CREATING) {
      expect(header(tab, { caps: { ready: true, mutate: false } }), tab).toBeNull();
      expect(header(tab, { caps: { ready: false, mutate: true } }), tab).toBeNull();
      expect(header(tab, { covered: true }), tab).toBeNull();
    }
  });

  it("is absent while the first read is in flight, since the layer may turn out empty", () => {
    for (const tab of CREATING) {
      expect(header(tab, { catalogue: catalogue("idle", []) }), tab).toBeNull();
      expect(header(tab, { catalogue: catalogue("loading", []) }), tab).toBeNull();
    }
  });

  it("stays in place, disabled with the reason, when the read failed and while it is retried", () => {
    for (const tab of CREATING) {
      expect(header(tab, { catalogue: catalogue("error", []) }), tab).toMatchObject({ disabled: true, title: UNREAD_REASON, menuDisabled: true });
      expect(header(tab, { catalogue: catalogue("loading", [], true) }), tab).toMatchObject({ disabled: true, title: READING_REASON, menuDisabled: true });
    }
  });

  it("leaves create to a read and empty layer's empty state", () => {
    expect(header("overview", { catalogue: catalogue("ready", []) })).toBeNull();
    const onlyFiles = [{ kind: KIND_FILE }];
    expect(header("sources", { catalogue: catalogue("ready", onlyFiles) })).toBeNull();
    expect(header("combinations", { catalogue: catalogue("ready", onlyFiles) })).toBeNull();
    expect(header("files", { catalogue: catalogue("ready", onlyFiles) })).not.toBeNull();
    expect(header("overview", { catalogue: catalogue("ready", onlyFiles) })).not.toBeNull();
    expect(header("files", { catalogue: catalogue("ready", [{ kind: KIND_SUB }]) })).toBeNull();
  });

  it("is disabled at the record limit, and the Overview's chevron still opens to say why", () => {
    const full = [...STORE, ...Array.from({ length: MAX_SUBSCRIPTION_RECORDS - STORE.length }, () => ({ kind: KIND_FILE }))];
    for (const tab of CREATING) {
      expect(header(tab, { catalogue: catalogue("ready", full) }), tab).toMatchObject({ disabled: true, title: LIMIT_REASON, menuDisabled: false });
    }
  });

  it("disables New combination when there is no subscription to combine", () => {
    const combosOnly = [{ kind: KIND_COLLECTION }, { kind: KIND_FILE }];
    expect(header("combinations", { catalogue: catalogue("ready", combosOnly) })).toMatchObject({ disabled: true, title: NO_SOURCE_REASON });
  });
});
