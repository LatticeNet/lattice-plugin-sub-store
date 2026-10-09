import { describe, expect, it } from "vitest";

import { KIND_COLLECTION, KIND_FILE, KIND_SUB, MAX_SUBSCRIPTION_RECORDS } from "./client";
import {
  createBlocks,
  headerCreate,
  storeBlock,
  type CatalogueView,
  type HeaderCreateInput,
} from "./createGate";
import { t } from "./i18n";

// The reasons are the message table's, read in English, the default locale.
const UNREAD_REASON = t.create.unread;
const READING_REASON = t.create.reading;
const LIMIT_REASON = t.create.limit(MAX_SUBSCRIPTION_RECORDS);
const NO_SOURCE_REASON = t.create.noSource;
const LEGACY_REASON = t.create.legacy;
import type { LoadState } from "./useSubscriptions";
import type { ViewId } from "./pipeline";

const STORE = [{ kind: KIND_SUB }, { kind: KIND_SUB }, { kind: KIND_COLLECTION }, { kind: KIND_FILE }];

function catalogue(state: LoadState, records: readonly { kind?: string }[] = STORE, failed = false): CatalogueView {
  return { state, failed, records };
}

/**
 * A layer as the header reads it: the Overview, or Records with its kind
 * filter (`records:file`). Records showing every kind is plain `records`.
 */
type Layer = ViewId | `records:${"source" | "combination" | "file"}`;

function header(layer: Layer, overrides: Partial<HeaderCreateInput> = {}) {
  const [tab, kind = ""] = layer.split(":") as [ViewId, string?];
  return headerCreate({
    tab,
    kind,
    catalogue: catalogue("ready"),
    caps: { ready: true, mutate: true },
    covered: false,
    ...overrides,
  });
}

const CREATING: readonly Layer[] = ["overview", "records", "records:source", "records:combination", "records:file"];

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

  it("cannot on a legacy store, which refuses every save until it is migrated", () => {
    const legacy = { ...catalogue("ready"), legacy: true };
    expect(storeBlock(legacy)).toBe(LEGACY_REASON);
    expect(createBlocks(legacy)).toEqual({ "new-subscription": LEGACY_REASON, "new-collection": LEGACY_REASON, "new-file": LEGACY_REASON });
    // Unread still comes first: whether the store is legacy is not known yet.
    expect(storeBlock({ ...catalogue("error"), legacy: true })).toBe(UNREAD_REASON);
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
  it("names the verb of the kind on screen, a split button where every kind shows", () => {
    expect(header("overview")).toMatchObject({ command: "new-subscription", label: "New source", menu: true, disabled: false });
    expect(header("records")).toMatchObject({ command: "new-subscription", label: "New source", menu: true, disabled: false });
    expect(header("records:source")).toMatchObject({ command: "new-subscription", label: "New source", menu: false });
    expect(header("records:combination")).toMatchObject({ command: "new-collection", label: "New combination", menu: false });
    expect(header("records:file")).toMatchObject({ command: "new-file", label: "New file", menu: false });
    expect(header("records:file")?.title).toBe("A document served as it is, with its proxy list kept in step");
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

  it("leaves create to the empty state of a kind the store holds none of", () => {
    expect(header("overview", { catalogue: catalogue("ready", []) })).toBeNull();
    expect(header("records", { catalogue: catalogue("ready", []) })).toBeNull();
    const onlyFiles = [{ kind: KIND_FILE }];
    expect(header("records:source", { catalogue: catalogue("ready", onlyFiles) })).toBeNull();
    expect(header("records:combination", { catalogue: catalogue("ready", onlyFiles) })).toBeNull();
    expect(header("records:file", { catalogue: catalogue("ready", onlyFiles) })).not.toBeNull();
    expect(header("records", { catalogue: catalogue("ready", onlyFiles) })).not.toBeNull();
    expect(header("overview", { catalogue: catalogue("ready", onlyFiles) })).not.toBeNull();
    expect(header("records:file", { catalogue: catalogue("ready", [{ kind: KIND_SUB }]) })).toBeNull();
  });

  it("reads an unknown kind filter as every kind", () => {
    expect(header("records", { kind: "module" })).toMatchObject({ command: "new-subscription", menu: true });
  });

  it("is disabled at the record limit, and the split button's chevron still opens to say why", () => {
    const full = [...STORE, ...Array.from({ length: MAX_SUBSCRIPTION_RECORDS - STORE.length }, () => ({ kind: KIND_FILE }))];
    for (const tab of CREATING) {
      expect(header(tab, { catalogue: catalogue("ready", full) }), tab).toMatchObject({ disabled: true, title: LIMIT_REASON, menuDisabled: false });
    }
  });

  it("disables New combination when there is no subscription to combine", () => {
    const combosOnly = [{ kind: KIND_COLLECTION }, { kind: KIND_FILE }];
    expect(header("records:combination", { catalogue: catalogue("ready", combosOnly) })).toMatchObject({ disabled: true, title: NO_SOURCE_REASON });
  });
});
