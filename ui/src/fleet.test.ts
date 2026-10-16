import { describe, expect, it, vi } from "vitest";

import { ERROR_CODES, KNOWN_ERROR_CODES, errorCodeOf, isRefused } from "./client";
import {
  LINE_LOOKUP_BATCH,
  LineRowCache,
  MAX_SNAPSHOT_BYTES,
  STRUCTURED_FILTER_TYPE,
  effectiveGeo,
  fleetOptionsProblem,
  groupExclusions,
  hasBindShape,
  leadingRun,
  linesThatFit,
  linkModeScriptSteps,
  normalizeFleetOptions,
  planState,
  predicateProblem,
  readFilterArgs,
  responseTransformerSteps,
  sampleValues,
  shortVersion,
  withField,
  withOp,
  writeFilterArgs,
  type CatalogueRow,
  type FleetPreviewReply,
} from "./fleet";
import { keepSelectionFirst } from "./useRecordEditor";

function row(id: string, extra: Partial<CatalogueRow> = {}): CatalogueRow {
  return { line_uuid: id, line_hash_id: `h-${id}`, node_id: "n1", protocol: "vless", chain: { role: "single" }, probe: null, ...extra };
}

const filter = (predicates: unknown[], extra: Record<string, unknown> = {}) => ({
  type: STRUCTURED_FILTER_TYPE,
  args: { mode: "keep", match: "all", predicates, ...extra },
});

describe("a mutating method's refusal", () => {
  const refused = { saved: false, refused: { code: "fleet_file_unavailable", files: ["phone"], message: "subscription \"hk\" feeds file \"phone\"" } };

  it("is recognised from the successful reply, never from an error", () => {
    expect(isRefused(refused)).toBe(true);
    expect(isRefused({ saved: true, subscription: {} })).toBe(false);
    expect(isRefused({ saved: false, conflict: { id: "x", reason: "stale" } })).toBe(false);
    expect(isRefused({ saved: false, refused: { code: "", message: "" } })).toBe(false);
    expect(isRefused(null)).toBe(false);
  });

  it("is read by errorCodeOf before any message token", () => {
    expect(errorCodeOf(refused)).toBe(ERROR_CODES.fleetFileUnavailable);
    // An unknown structured code drives no state written for another.
    expect(errorCodeOf({ saved: false, refused: { code: "something_new", message: "x" } })).toBe("");
  });

  it("still reads a read method's code from the message's leading token", () => {
    expect(errorCodeOf(new Error("owner_credentials_withheld: subscription \"home\" serves line owner credentials"))).toBe(
      ERROR_CODES.ownerCredentialsWithheld,
    );
    expect(errorCodeOf(new Error("snapshot_too_large: the selection holds 5000 lines"))).toBe(ERROR_CODES.snapshotTooLarge);
    expect(errorCodeOf(new Error("not_a_code_we_know: x"))).toBe("");
  });

  it("knows the two codes carried from wave 1", () => {
    expect(KNOWN_ERROR_CODES.has("structured_args_invalid")).toBe(true);
    expect(KNOWN_ERROR_CODES.has("fleet_probe_unavailable")).toBe(true);
  });
});

describe("planState", () => {
  it("derives failed from an approved plan whose apply failed", () => {
    expect(planState({ status: "approved", reason: "apply_revision failed: the record may have been promoted" })).toBe("failed");
  });

  it("derives stale from core's boolean, not from the reason text", () => {
    expect(planState({ status: "rejected", reason: "the record's live revision moved", stale: true })).toBe("stale");
    expect(planState({ status: "rejected", reason: "the record's live revision moved" })).toBe("rejected");
  });

  it("passes core's statuses through", () => {
    for (const status of ["pending", "approved", "rejected", "applied"] as const) expect(planState({ status })).toBe(status);
  });
});

describe("a predicate", () => {
  it("is refused where the plugin refuses it", () => {
    expect(predicateProblem({ field: "country", op: "in", values: ["HK"] })).toBe("");
    expect(predicateProblem({ field: "nope", op: "in", values: ["x"] })).toBe("unknownField");
    expect(predicateProblem({ field: "country", op: "lte", value: 1 })).toBe("badOp");
    expect(predicateProblem({ field: "country", op: "in", values: [] })).toBe("needsValues");
    expect(predicateProblem({ field: "country", op: "in", values: [""] })).toBe("emptyValue");
    expect(predicateProblem({ field: "line_uuid", op: "in", values: ["ABC"] })).toBe("lineUUID");
    expect(predicateProblem({ field: "line_uuid", op: "in", values: ["0a63b2c4-1d2e-4f3a-8b4c-5d6e7f8a9b0c"] })).toBe("");
    expect(predicateProblem({ field: "asn", op: "in", values: ["09"] })).toBe("asn");
    expect(predicateProblem({ field: "chain_role", op: "in", values: ["hub"] })).toBe("closedValue");
    expect(predicateProblem({ field: "renewal_days", op: "lte" })).toBe("needsValue");
    expect(predicateProblem({ field: "renewal_days", op: "lte", value: 99999 })).toBe("outOfRange");
    expect(predicateProblem({ field: "probe_passed_hours", op: "present" })).toBe("badOp");
    expect(predicateProblem({ field: "managed", op: "true" })).toBe("");
  });

  it("keeps what still applies when its field or operator changes", () => {
    expect(withOp({ field: "country", op: "in", values: ["HK"] }, "not_in")).toEqual({ field: "country", op: "not_in", values: ["HK"] });
    expect(withOp({ field: "country", op: "in", values: ["HK"] }, "present")).toEqual({ field: "country", op: "present" });
    expect(withField({ field: "country", op: "in", values: ["HK"] }, "region")).toEqual({ field: "region", op: "in", values: [] });
    expect(withField({ field: "country", op: "in", values: ["HK"] }, "managed")).toEqual({ field: "managed", op: "true" });
  });

  it("round-trips through the wire form with the plugin's defaults left out", () => {
    const wire = writeFilterArgs(readFilterArgs({ mode: "drop", match: "any", geo: "exit", predicates: [{ field: "renewal_days", op: "lte", value: 3, values: ["x"] }] }));
    expect(wire).toEqual({ mode: "drop", match: "any", predicates: [{ field: "renewal_days", op: "lte", value: 3 }] });
    expect(writeFilterArgs(readFilterArgs({ predicates: [], geo: "entry" }))).toEqual({ mode: "keep", match: "all", predicates: [], geo: "entry" });
  });
});

describe("the leading structured run", () => {
  it("counts disabled steps between filters and stops at the first other step", () => {
    const process = [filter([]), { type: "Sort Operator", disabled: true }, filter([]), { type: "Regex Filter" }, filter([])];
    expect(leadingRun(process)).toEqual({ length: 3, filters: [0, 2] });
  });

  it("is empty when the chain starts elsewhere", () => {
    expect(leadingRun([{ type: "Useless Filter" }, filter([])])).toEqual({ length: 0, filters: [] });
  });

  it("stays at the head when the common settings add their steps", () => {
    const process = [{ type: "Quick Setting Operator", args: { udp: true } }, { type: "Useless Filter" }, filter([]), { type: "Regex Rename Operator" }];
    const kept = keepSelectionFirst(process);
    expect(kept.map((step) => step.type)).toEqual([STRUCTURED_FILTER_TYPE, "Quick Setting Operator", "Useless Filter", "Regex Rename Operator"]);
    expect(leadingRun(kept).filters).toEqual([0]);
    // A chain without a selection is left as it is.
    const plain = [{ type: "Useless Filter" }, { type: "Regex Rename Operator" }];
    expect(keepSelectionFirst(plain)).toEqual(plain);
  });

  it("finds what fleet records refuse in the rest of the chain", () => {
    const process = [filter([]), { type: "Script Operator", args: { mode: "link", content: "https://x.example/a.js" } }, { type: "Script Filter", args: { mode: "script" } }, { type: "Response Transformer" }];
    expect(linkModeScriptSteps(process)).toEqual([1]);
    expect(responseTransformerSteps(process)).toEqual([3]);
  });
});

describe("binding rules", () => {
  it("store nothing when nothing is opted into", () => {
    expect(normalizeFleetOptions({ ddns_dial: false, probe_exclusion: { enabled: false } })).toBeUndefined();
  });

  it("drop the probe threshold the plugin reads by default", () => {
    expect(normalizeFleetOptions({ probe_exclusion: { enabled: true, consecutive_failures: 3 } })).toEqual({ probe_exclusion: { enabled: true } });
    expect(normalizeFleetOptions({ probe_exclusion: { enabled: true, consecutive_failures: 5 } })).toEqual({ probe_exclusion: { enabled: true, consecutive_failures: 5 } });
  });

  it("are refused out of the plugin's ranges", () => {
    expect(fleetOptionsProblem({ probe_exclusion: { enabled: true, consecutive_failures: 11 } })).toBe("probeRange");
    expect(fleetOptionsProblem({ probe_exclusion: { enabled: true, consecutive_failures: 0 } })).toBe("probeRange");
    expect(fleetOptionsProblem({ usage_exclusion: { enabled: true, max_bytes_per_line: 0 } })).toBe("usagePositive");
    expect(fleetOptionsProblem({ usage_exclusion: { enabled: true, max_bytes_per_line: 53687091200 } })).toBe("");
  });
});

describe("catalogue rows", () => {
  it("read an unresolved relay's geo as unknown, and the exit before the node", () => {
    const hub = row("a", { geo: { country: "HK" }, chain: { role: "relay", unresolved: true } });
    expect(effectiveGeo(hub)).toBeNull();
    expect(effectiveGeo(hub, "entry")).toEqual({ country: "HK" });
    const chained = row("b", { geo: { country: "HK" }, chain: { role: "entry", exit_geo: { country: "JP" } } });
    expect(effectiveGeo(chained)?.country).toBe("JP");
  });

  it("offer the values the selection holds, most frequent first", () => {
    const rows = [row("a", { geo: { country: "JP" } }), row("b", { geo: { country: "HK" } }), row("c", { geo: { country: "HK" } })];
    expect(sampleValues(rows, "country")).toEqual(["HK", "JP"]);
    expect(sampleValues([], "service_state")).toEqual(["down", "restarting", "running", "unknown"]);
  });

  it("know which protocols core can bind", () => {
    expect(hasBindShape(row("a", { protocol: "shadowsocks" }))).toBe(false);
    expect(hasBindShape(row("a", { protocol: "vless", template: { protocol: "vless", host: "h", port: 443 } }))).toBe(true);
  });

  it("say how many lines fit the snapshot bound at the selection's row size", () => {
    expect(linesThatFit({ count: 100, estimated_envelope_bytes: 150_000 })).toBe(Math.floor(MAX_SNAPSHOT_BYTES / 1500));
    expect(linesThatFit({ count: 0, estimated_envelope_bytes: 0 })).toBe(0);
  });

  it("print versions short", () => {
    expect(shortVersion("lcv1-0a63b2c4d5e6f7")).toBe("lcv1-0a63b2c4…");
    expect(shortVersion("abc")).toBe("abc");
  });
});

describe("the line row cache", () => {
  const reply = (version: string, ids: string[]): FleetPreviewReply => ({
    catalogue_version: version,
    selector_fields: [],
    covered: 0,
    steps: [],
    count: ids.length,
    rows: ids.map((id) => row(id)),
    estimated_envelope_bytes: 0,
  });

  it("looks rows up in batches of at most 256 and never twice", async () => {
    const lookup = vi.fn(async (ids: string[]) => reply("lcv1-a", ids));
    const cache = new LineRowCache(lookup);
    const ids = Array.from({ length: 600 }, (_, i) => `line-${i}`);
    await cache.ensure(ids);
    expect(lookup).toHaveBeenCalledTimes(3);
    for (const call of lookup.mock.calls) expect(call[0].length).toBeLessThanOrEqual(LINE_LOOKUP_BATCH);
    await cache.ensure(ids.slice(0, 10));
    expect(lookup).toHaveBeenCalledTimes(3);
    expect(cache.get("line-599")?.line_uuid).toBe("line-599");
  });

  it("forgets every row when the catalogue version moves", async () => {
    const cache = new LineRowCache(async (ids) => reply("lcv1-b", ids));
    cache.remember("lcv1-a", [row("old")]);
    expect(cache.get("old")).toBeDefined();
    await cache.ensure(["new"]);
    expect(cache.catalogueVersion).toBe("lcv1-b");
    expect(cache.get("old")).toBeUndefined();
    expect(cache.get("new")).toBeDefined();
  });
});

describe("excluded entries", () => {
  it("group by reason, largest group first", () => {
    const groups = groupExclusions([
      { index: 0, reason: "service_down" },
      { index: 1, reason: "not_bound", fix: "bind the identity to the line" },
      { index: 2, reason: "not_bound" },
    ]);
    expect(groups.map((group) => [group.reason, group.entries.length])).toEqual([
      ["not_bound", 2],
      ["service_down", 1],
    ]);
  });
});
