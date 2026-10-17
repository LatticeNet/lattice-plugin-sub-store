/**
 * The records and shares the harness serves with `?fixture=plans` (design 28
 * S2, lane 6b): the fleet fixture plus every state the Fleet view, the plan
 * flow and the staged state must draw.
 *
 *   asia-fleet         fleet, staged revision waiting, one identity share and
 *                      one share naming no identity (the decoy); a
 *                      combination names it explicitly, so its delete is blocked
 *   fleet-probe-fast   fleet, an identity share: its delete goes through a plan
 *   all-fleet-lines    fleet, no share: a staged edit publishes in one click
 *   self-export        legacy export, two shares naming no identity
 *   relay-graph        legacy graph record for vu_alice, gathered with
 *   home-graph         another legacy graph record into legacy-pair, so they
 *                      migrate together
 *   files-legacy       legacy export a file reads: migration blocked by files
 *   late-legacy        legacy export whose migration the plugin refuses with a
 *                      file the list has not seen yet (refused.files)
 *   promoted-member    fleet, promoted by a multi-record plan that stopped at
 *   legacy-left        a legacy graph record still staged, both in
 *   mixed-combo        a combination core refuses until legacy-left migrates
 *   openjobs-host      a provider record whose share names vu_bob: the
 *                      identity buys nothing; Asia combination with a
 *                      provider names it, so since S2-SR-1 its edits stage
 *                      and its delete goes through a plan
 *   paid-extra         a provider record fleet-plus-paid gathers by its tag
 *   fleet-plus-paid    a fleet-bound combination over every fleet line and
 *                      the paid-extra tag, with an identity share: a
 *                      paid-extra edit or delete is a plan
 *   tokyo-scripted     fleet, an identity share, and a staged revision whose
 *                      only change is a pass-through Script Operator: a plan
 *                      that moves no line, only a step
 *   osaka-lookup       fleet, an identity share, a Script Operator that asks
 *                      for the network, and a staged revision that renames
 *                      it: every fleet render of it is refused
 *                      (fleet_script_network_unavailable)
 *
 * Every name, id and figure is invented; no share link or credential is in
 * any of it beyond the harness's own placeholder tokens.
 *
 * Never imported by `src/`; the shipped bundle is built from index.html alone.
 */
import type { Fixture, ShareRow, StoredRecord } from "./fixtures";
import { catalogueRows } from "./fleetFixtures";

export type PlanShare = ShareRow & { identity_id?: string; display_name?: string };

const HOUR = 3600 * 1000;
const ago = (ms: number) => new Date(Date.now() - ms).toISOString();

/** Committed roots for the graph records: three Hong Kong lines and two Tokyo lines of the harness catalogue. */
function roots(country: string, count: number): string[] {
  return catalogueRows()
    .filter((row) => row.geo?.country === country && row.protocol !== "shadowsocks")
    .slice(0, count)
    .map((row) => row.line_uuid);
}

function share(subscription_id: string, share_id: string, slug: string, extra: Partial<PlanShare> = {}): PlanShare {
  const token = `${share_id.replace(/[^a-z0-9]/g, "")}harnesstokenharnesstoken`.slice(0, 32);
  return {
    subscription_id,
    share_id,
    slug,
    enabled: true,
    default_format: "plain",
    path: `/sub/${slug}/${token}`,
    url: `https://lattice.example/sub/${slug}/${token}`,
    ...extra,
  };
}

const TOKYO_FILTER = { type: "Structured Filter", args: { mode: "keep", match: "all", predicates: [{ field: "region", op: "in", values: ["Tokyo"] }] } };
const OSAKA_FILTER = { type: "Structured Filter", args: { mode: "keep", match: "all", predicates: [{ field: "region", op: "in", values: ["Osaka"] }] } };
/** A script that reads every node and asks the network about them: what S2-SR-2 withholds on a fleet render. */
const LOOKUP_SCRIPT = {
  type: "Script Operator",
  args: { mode: "script", content: "async function operator(proxies) {\n  await $substore.http.get({ url: 'https://geo.example/lookup' });\n  return proxies;\n}" },
};

/**
 * The staged revisions the plans fixture starts with beside asia-fleet's
 * and legacy-left's (dev/planHost.ts seeds them): tokyo-scripted gains a
 * pass-through script, osaka-lookup renames its lookup script.
 */
export function planStagedRecords(): { id: string; process: unknown[]; minutesAgo: number }[] {
  return [
    {
      id: "tokyo-scripted",
      minutesAgo: 25,
      process: [TOKYO_FILTER, { type: "Script Operator", args: { mode: "script", content: "function operator(proxies) {\n  return proxies;\n}" } }],
    },
    { id: "osaka-lookup", minutesAgo: 15, process: [OSAKA_FILTER, { ...LOOKUP_SCRIPT, customName: "Geo lookup" }] },
  ];
}

export function planRecords(): StoredRecord[] {
  return [
    {
      id: "relay-graph",
      name: "relay-graph",
      display_name: "香港中继 Relay path for alice",
      tags: ["relay"],
      source: "vpn-core-graph",
      vpn_identity: "vu_alice",
      entry_roots: roots("HK", 3),
      graph_options_version: `ov1:${"b".repeat(64)}`,
      process: [{ type: "Regex Filter", args: { regex: ["香港|HK"], keep: true } }],
      last_fetch_at: ago(2 * HOUR),
      last_fetch_ok: true,
    },
    {
      id: "home-graph",
      name: "home-graph",
      display_name: "Home relay for bob",
      tags: ["relay"],
      source: "vpn-core-graph",
      vpn_identity: "vu_bob",
      entry_roots: roots("HK", 1),
      graph_options_version: `ov1:${"b".repeat(64)}`,
      process: [],
      last_fetch_at: ago(2 * HOUR),
      last_fetch_ok: true,
    },
    {
      id: "legacy-pair",
      kind: "collection",
      name: "legacy-pair",
      display_name: "Relay pair for the family",
      members: ["relay-graph", "home-graph"],
      failure_mode: "strict",
      process: [],
    },
    {
      id: "files-legacy",
      name: "files-legacy",
      display_name: "Router export (legacy)",
      source: "vpn-core",
      process: [],
      last_fetch_at: ago(5 * HOUR),
      last_fetch_ok: true,
    },
    {
      id: "router-config",
      kind: "file",
      name: "router-config",
      display_name: "OpenWrt router config",
      source: "local",
      file_type: "config",
      node_source: "files-legacy",
      content: "proxies: []\n",
      process: [],
    },
    {
      id: "late-legacy",
      name: "late-legacy",
      display_name: "Office export (legacy)",
      source: "vpn-core",
      process: [{ type: "Regex Filter", args: { regex: ["Office|办公"], keep: true } }],
      last_fetch_at: ago(5 * HOUR),
      last_fetch_ok: true,
    },
    {
      id: "promoted-member",
      name: "promoted-member",
      display_name: "Tokyo lines (migrated)",
      tags: ["fleet"],
      source: "fleet",
      process: [{ type: "Structured Filter", args: { mode: "keep", match: "all", predicates: [{ field: "region", op: "in", values: ["Tokyo"] }] } }],
      last_fetch_at: ago(HOUR),
      last_fetch_ok: true,
    },
    {
      id: "legacy-left",
      name: "legacy-left",
      display_name: "大阪中继 Osaka relay (legacy)",
      source: "vpn-core-graph",
      vpn_identity: "vu_erin",
      entry_roots: roots("JP", 2),
      graph_options_version: `ov1:${"b".repeat(64)}`,
      process: [],
      last_fetch_at: ago(3 * HOUR),
      last_fetch_ok: true,
    },
    {
      // A selection over core's snapshot bound: every refresh fails with the computed figure.
      id: "huge-fleet",
      name: "huge-fleet",
      display_name: "Every line of every region, unfiltered (too large)",
      tags: ["fleet"],
      source: "fleet",
      process: [],
      last_fetch_at: ago(30 * 60 * 1000),
      last_fetch_ok: false,
      last_error:
        "snapshot_too_large: the selection holds 6200 lines; this core keeps at most 4 MiB of snapshot, about 2953 lines of this fleet; narrow the selection or split the record",
    },
    {
      id: "paid-extra",
      name: "paid-extra",
      display_name: "Paid provider extra",
      tags: ["fleet-extra"],
      source: "remote",
      url: "https://sub.example-provider.com/api/v1/client/subscribe?token=00aa11bb22cc33dd44ee55ff66778899",
      process: [{ type: "Regex Filter", args: { regex: ["HK|JP"], keep: true } }],
      last_fetch_at: ago(HOUR),
      last_fetch_ok: true,
    },
    {
      id: "fleet-plus-paid",
      kind: "collection",
      name: "fleet-plus-paid",
      display_name: "Fleet lines with a paid extra",
      members: ["all-fleet-lines"],
      member_tags: ["fleet-extra"],
      failure_mode: "skip",
      process: [],
    },
    {
      id: "tokyo-scripted",
      name: "tokyo-scripted",
      display_name: "Tokyo lines, pass-through script",
      tags: ["fleet"],
      source: "fleet",
      process: [TOKYO_FILTER],
      last_fetch_at: ago(HOUR),
      last_fetch_ok: true,
    },
    {
      id: "osaka-lookup",
      name: "osaka-lookup",
      display_name: "Osaka lines with a lookup script",
      tags: ["fleet"],
      source: "fleet",
      process: [OSAKA_FILTER, LOOKUP_SCRIPT],
      last_fetch_at: ago(HOUR),
      last_fetch_ok: true,
    },
    {
      id: "mixed-combo",
      kind: "collection",
      name: "mixed-combo",
      display_name: "Japan combination",
      members: ["promoted-member", "legacy-left"],
      failure_mode: "strict",
      process: [],
    },
  ];
}

export function planShares(): PlanShare[] {
  return [
    share("asia-fleet", "sh-asia-alice", "asia-alice", { identity_id: "vu_alice", display_name: "Alice's laptop, Asia" }),
    share("asia-fleet", "sh-asia-open", "asia-open", { display_name: "Asia shared link (no identity)" }),
    share("fleet-probe-fast", "sh-probe-erin", "probe-erin", { identity_id: "vu_erin" }),
    share("self-export", "sh-self-home", "self", { display_name: "家里的路由器 home router" }),
    share("self-export", "sh-self-phone", "self-phone"),
    share("relay-graph", "sh-relay-alice", "relay-alice", { display_name: "Alice relay link" }),
    share("legacy-pair", "sh-pair-family", "pair-family", { display_name: "Family pair link" }),
    share("home-graph", "sh-home-bob", "home-bob"),
    share("openjobs-host", "sh-oj-bob", "openjobs-bob", { identity_id: "vu_bob", display_name: "Bob's provider link" }),
    share("promoted-member", "sh-tokyo-alice", "tokyo-alice", { identity_id: "vu_alice" }),
    share("legacy-left", "sh-osaka", "osaka"),
    share("mixed-combo", "sh-japan", "japan-family", { display_name: "Japan family link" }),
    share("files-legacy", "sh-router", "router"),
    share("fleet-plus-paid", "sh-plus-family", "plus-family", { identity_id: "vu_alice", display_name: "Family link with the paid extra" }),
    share("tokyo-scripted", "sh-tokyo-script", "tokyo-script", { identity_id: "vu_alice", display_name: "Alice's Tokyo link" }),
    share("osaka-lookup", "sh-osaka-lookup", "osaka-lookup", { identity_id: "vu_erin", display_name: "Erin's Osaka link" }),
  ];
}

/** The fleet fixture with the plan states' records and shares added. */
export function planFixture(base: Fixture): Fixture {
  const extra = planRecords();
  const ids = new Set(extra.map((record) => record.id));
  return {
    records: [...base.records.filter((record) => !ids.has(record.id)), ...extra],
    shares: [...base.shares, ...planShares()],
    counts: base.counts,
  };
}
