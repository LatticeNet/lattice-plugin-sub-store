/**
 * The harness's half of design 28 S2: the fleet proxies (fleet_preview,
 * identities), core's bind.preview, and the fleet records' list fields and
 * previews, answered in the plugin's and core's wire shapes over the fleet
 * in fleetFixtures.ts.
 *
 * `?manifest=s2` plays the 0.18 manifest: the handshake declares the S2
 * methods and the bind and plans services, a fleet record previews over
 * placeholder credentials, and a legacy record's preview is withheld. Without
 * it nothing here changes what the harness answers, so the drives written
 * against the 0.17 manifest see what they always saw.
 *
 * `?fleet=` picks a state the editor must draw:
 *   slow     every fleet read holds, so the loading state can be looked at
 *   error    the catalogue cannot be read (catalogue_unavailable)
 *   denied   a token without vpncore:read: the handshake omits fleet_preview,
 *            identities and bind.preview
 *   empty    a selection matches no line
 *   large    a selection's snapshot is over core's 4 MiB bound
 *   stale    the records' snapshots predate the catalogue
 *   probe    the catalogue has probe data (the default has none, as
 *            production has none today)
 *
 * Never imported by `src/`; the shipped bundle is built from index.html alone.
 */
import type { MethodBinding } from "../src/client";
import type { CatalogueRow, Predicate, StructuredFilterArgs } from "../src/fleet";
import {
  CATALOGUE_VERSION,
  SELECTOR_FIELDS,
  STALE_VERSION,
  catalogueRows,
  harnessIdentities,
  type FleetRecord,
} from "./fleetFixtures";

export type FleetHarnessState = "ok" | "slow" | "error" | "denied" | "empty" | "large" | "stale" | "probe";

export function fleetState(): FleetHarnessState {
  const asked = new URLSearchParams(window.location.search).get("fleet");
  const known: FleetHarnessState[] = ["ok", "slow", "error", "denied", "empty", "large", "stale", "probe"];
  return (known as string[]).includes(asked ?? "") ? (asked as FleetHarnessState) : "ok";
}

/** Whether the handshake plays the 0.18 manifest (`?manifest=s2`). */
export function s2Manifest(): boolean {
  return new URLSearchParams(window.location.search).get("manifest") === "s2";
}

/** The subscription service's S2 methods, as manifest.json declares them. */
export const S2_SUBSCRIPTION_METHODS = ["fleet_preview", "identities", "migrate_record", "discard_staged"];

/** The two core-backed services the 0.18 manifest declares. */
export const S2_INTERFACES = [
  { service: "latticenet.sub-store/bind", methods: ["preview", "bind_selection"] },
  { service: "latticenet.sub-store/plans", methods: ["propose", "status", "publish"] },
];

/** What a token without vpncore:read does not see (`?fleet=denied`). */
export function fleetWithheld(target: MethodBinding): boolean {
  if (fleetState() !== "denied") return false;
  if (target.service === "latticenet.sub-store/bind" && target.method === "preview") return true;
  return target.service === "latticenet.sub-store/subscription" && (target.method === "fleet_preview" || target.method === "identities");
}

// ── evaluating a selection, the way the plugin's evaluator reads a row ────────

const FOLD = new Set(["country", "region", "city", "as_org", "geo_provider", "machine_vendor", "protocol", "transport", "security", "service_state", "status", "overlay_status"]);
const SELECTOR: Record<string, string> = {
  line_uuid: "line_uuids",
  country: "countries",
  region: "regions",
  node_tag: "node_tags",
  group: "group_ids",
  protocol: "protocols",
  transport: "transports",
  service_state: "service_states",
  chain_role: "chain_roles",
  renewal_days: "renewal_within_days",
  probe_passed_hours: "probe_passed_within_hours",
};
const GEO = new Set(["country", "region", "city", "asn", "as_org", "geo_provider"]);
const NUMBER = new Set(["renewal_days", "probe_passed_hours", "probe_cold_p50_ms"]);

function geoOf(row: CatalogueRow, choice: string) {
  if (choice !== "entry" && row.chain) {
    if (row.chain.unresolved) return null;
    if (row.chain.exit_geo) return row.chain.exit_geo;
  }
  return row.geo ?? null;
}

function textOf(row: CatalogueRow, field: string, choice: string): string | null {
  if (GEO.has(field)) {
    const geo = geoOf(row, choice);
    if (!geo) return null;
    const value = { country: geo.country, region: geo.region, city: geo.city, asn: geo.asn ? String(geo.asn) : "", as_org: geo.as_org, geo_provider: geo.provider }[field];
    return value || null;
  }
  const value = {
    machine_vendor: row.machine?.vendor,
    protocol: row.protocol,
    transport: row.transport,
    security: row.security,
    service_state: row.service_state,
    status: row.status,
    overlay_status: row.overlay_status,
    chain_role: row.chain?.role,
    path_state: row.chain?.path_state,
    line_uuid: row.line_uuid,
    probe_vantage: row.probe?.vantage,
  }[field];
  return value || null;
}

function numberOf(row: CatalogueRow, field: string, now: number): number | null {
  if (field === "renewal_days") {
    const at = row.machine?.next_renewal ? Date.parse(row.machine.next_renewal) : Number.NaN;
    return Number.isNaN(at) ? null : (at - now) / 86_400_000;
  }
  if (field === "probe_passed_hours") {
    if (!row.probe || row.probe.verdict !== "pass") return null;
    return (now - Date.parse(row.probe.at)) / 3_600_000;
  }
  if (field === "probe_cold_p50_ms") return row.probe?.cold_p50_ms || null;
  return null;
}

function boolOf(row: CatalogueRow, field: string): boolean | null {
  switch (field) {
    case "managed": return Boolean(row.managed);
    case "overlay": return Boolean(row.overlay);
    case "ddns_present": return Boolean(row.ddns_names?.some((name) => name.verified));
    case "chain_root": return Boolean(row.chain?.root);
    case "chain_unresolved": return Boolean(row.chain?.unresolved);
    case "probe_udp_ok": return row.probe?.udp_ok ?? null;
    default: return null;
  }
}

/** match, or null for unknown. */
function evaluate(p: Predicate, row: CatalogueRow, choice: string, now: number): boolean | null {
  if (p.field === "node_tag" || p.field === "group") {
    const have = (p.field === "node_tag" ? row.node_tags : row.group_ids) ?? [];
    if (p.op === "present") return have.length > 0;
    if (p.op === "absent") return have.length === 0;
    const any = (p.values ?? []).some((v) => have.includes(v));
    return p.op === "in" ? any : !any;
  }
  if (p.op === "true" || p.op === "false") {
    const b = boolOf(row, p.field);
    return b === null ? null : b === (p.op === "true");
  }
  if (NUMBER.has(p.field)) {
    const n = numberOf(row, p.field, now);
    if (p.op === "present") return n !== null;
    if (p.op === "absent") return n === null;
    if (n === null) return null;
    return p.op === "lte" ? n <= (p.value ?? 0) : n >= (p.value ?? 0);
  }
  const v = textOf(row, p.field, choice);
  if (p.op === "present") return v !== null;
  if (p.op === "absent") return v === null;
  if (v === null) return null;
  const fold = FOLD.has(p.field);
  const hit = (p.values ?? []).some((value) => (fold ? value.toLowerCase() === v.toLowerCase() : value === v));
  return p.op === "in" ? hit : !hit;
}

function runStep(args: StructuredFilterArgs, rows: CatalogueRow[], now: number): CatalogueRow[] {
  const choice = args.geo ?? "exit";
  return rows.filter((row) => {
    const results = args.predicates.map((p) => evaluate(p, row, choice, now));
    const matched = args.match === "any" ? results.some((r) => r === true) : results.every((r) => r === true);
    const unknown = args.match === "any" ? !matched && results.some((r) => r === null) : results.some((r) => r === null);
    if (args.mode === "drop") return unknown || !matched;
    return matched;
  });
}

interface Step {
  type?: string;
  disabled?: boolean;
  args?: StructuredFilterArgs;
}

function leadingFilters(process: unknown[]): number[] {
  const out: number[] = [];
  for (let i = 0; i < process.length; i++) {
    const step = process[i] as Step;
    if (step?.disabled) continue;
    if (step?.type === "Structured Filter") {
      out.push(i);
      continue;
    }
    break;
  }
  return out;
}

/** The leading steps core can evaluate, by operators/pushdown.go's rule. */
function pushedCount(process: unknown[], filters: number[]): number {
  const used = new Set<string>();
  let covered = 0;
  for (const index of filters) {
    const args = (process[index] as Step).args!;
    if ((args.mode ?? "keep") !== "keep" || (args.match ?? "all") !== "all") break;
    const fields: string[] = [];
    const ok = args.predicates.every((p) => {
      const selector = SELECTOR[p.field];
      if (!selector || !SELECTOR_FIELDS.includes(selector) || used.has(selector) || fields.includes(selector)) return false;
      if (GEO.has(p.field) && args.geo === "entry") return false;
      if (p.op === "in") return fields.push(selector) > 0;
      if (p.op === "lte" && (p.field === "renewal_days" || p.field === "probe_passed_hours") && Number.isInteger(p.value)) return fields.push(selector) > 0;
      return false;
    });
    if (!ok) break;
    fields.forEach((field) => used.add(field));
    covered = index + 1;
  }
  return covered;
}

function rowsNow(): CatalogueRow[] {
  const rows = catalogueRows();
  if (fleetState() !== "probe") return rows;
  const now = Date.now();
  return rows.map((row, i) => ({
    ...row,
    probe: { verdict: i % 9 === 0 ? "fail" : "pass", at: new Date(now - (i % 30) * 3_600_000).toISOString(), vantage: "control-plane", consecutive_failures: i % 9 === 0 ? 2 : 0, cold_p50_ms: 80 + (i % 17) * 23, udp_ok: i % 4 !== 0 },
  }));
}

/** fleet_preview's process form over the harness catalogue. */
function previewProcess(process: unknown[], sample: number) {
  const now = Date.now();
  const all = rowsNow();
  const filters = leadingFilters(process);
  const covered = pushedCount(process, filters);
  let rows = all;
  const steps: { index: number; in?: number; out: number; pushed: boolean }[] = [];
  const pushed = filters.filter((index) => index < covered);
  for (const index of pushed) rows = runStep((process[index] as Step).args!, rows, now);
  if (pushed.length) steps.push({ index: pushed[pushed.length - 1]!, out: rows.length, pushed: true });
  for (const index of filters.filter((i) => i >= covered)) {
    const before = rows.length;
    rows = runStep((process[index] as Step).args!, rows, now);
    steps.push({ index, in: before, out: rows.length, pushed: false });
  }
  const state = fleetState();
  if (state === "empty" && process.length) rows = [];
  const count = state === "large" && process.length ? 6200 : rows.length;
  const bytes = state === "large" && process.length ? 8_912_896 : Math.round(rows.length * 1420) + 512;
  return {
    catalogue_version: CATALOGUE_VERSION,
    selector_fields: SELECTOR_FIELDS,
    ...(state === "probe" ? {} : { unavailable: ["probe"] }),
    ...(covered ? { pushed: { countries: ["HK", "JP", "SG"] } } : {}),
    covered,
    steps,
    count,
    sample: rows.slice(0, sample),
    estimated_envelope_bytes: bytes,
  };
}

/** The rows a fleet record selects, as its fetch would keep them. */
function selectedRows(process: unknown[]): CatalogueRow[] {
  const now = Date.now();
  let rows = rowsNow();
  for (const index of leadingFilters(process)) rows = runStep((process[index] as Step).args!, rows, now);
  return fleetState() === "empty" && process.length ? [] : rows;
}

const BINDABLE = new Set(["anytls", "hysteria2", "socks", "trojan", "tuic", "vless", "vmess"]);

function placeholder(row: CatalogueRow, field: string): string {
  return `LATTICE-SYN-${row.line_uuid}-${field}-${row.line_hash_id.slice(-6)}`;
}

/** The target's document over placeholder credentials, the shape the plugin's producers write. */
function placeholderDocument(rows: CatalogueRow[], target: string): string {
  const bindable = rows.filter((row) => BINDABLE.has(row.template?.protocol ?? row.protocol));
  if (target === "sing-box" || target === "JSON") {
    const outbounds = bindable.map((row) => {
      const field = row.protocol === "vless" ? "uuid" : "password";
      return { type: row.protocol, tag: row.label, server: row.template?.host, server_port: row.template?.port, [field]: placeholder(row, field) };
    });
    return `${JSON.stringify({ outbounds }, null, 2)}\n`;
  }
  if (target === "ClashMeta" || target === "Clash" || target === "Stash" || target === "Egern") {
    const lines = ["proxies:"];
    for (const row of bindable) {
      const field = row.protocol === "vless" ? "uuid" : "password";
      lines.push(`  - {name: "${row.label}", type: ${row.protocol}, server: ${row.template?.host}, port: ${row.template?.port}, ${field}: ${placeholder(row, field)}}`);
    }
    return `${lines.join("\n")}\n`;
  }
  return `${bindable
    .map((row) => {
      const field = row.protocol === "vless" ? "uuid" : "password";
      const scheme = row.protocol === "hysteria2" ? "hysteria2" : row.protocol;
      return `${scheme}://${placeholder(row, field)}@${row.template?.host}:${row.template?.port}#${encodeURIComponent(row.label ?? "")}`;
    })
    .join("\n")}\n`;
}

function fleetPreviewResult(rows: CatalogueRow[], target: string, provider: { name: string; type: string; server: string; port: string }[] = []) {
  const bindable = rows.filter((row) => BINDABLE.has(row.template?.protocol ?? row.protocol));
  const nodes = [
    ...bindable.map((row) => ({ name: row.label ?? row.line_uuid, type: row.protocol, server: row.template?.host, port: String(row.template?.port ?? ""), network: row.transport, security: row.security })),
    ...provider,
  ];
  return {
    source_node_count: nodes.length,
    node_count: nodes.length,
    nodes: nodes.slice(0, 200),
    truncated: nodes.length > 200,
    dropped: [],
    dropped_count: 0,
    source_version: CATALOGUE_VERSION,
    stale: false,
    document: placeholderDocument(rows, target || "URI"),
    placeholder_credentials: true,
  };
}

/** Plugs the fleet answers into the harness's handler table. */
export function installFleet(handlers: Record<string, (payload: any) => unknown>, records: FleetRecord[]): void {
  const hold = <T>(): Promise<T> => new Promise<T>(() => {});
  const refuseRead = () => {
    const state = fleetState();
    if (state === "error") throw new Error("catalogue_unavailable: the line catalogue could not be read");
    return state === "slow";
  };

  handlers["subscription/fleet_preview"] = (payload: { process?: unknown[]; sample?: number; line_uuids?: string[] }) => {
    if (refuseRead()) return hold();
    if (Array.isArray(payload.line_uuids)) {
      if (payload.line_uuids.length > 256) {
        throw new Error(`line_uuids_too_many: fleet_preview looks up at most 256 lines per request, not ${payload.line_uuids.length}; page the list`);
      }
      const wanted = new Set(payload.line_uuids);
      const rows = rowsNow().filter((row) => wanted.has(row.line_uuid));
      return { catalogue_version: CATALOGUE_VERSION, selector_fields: SELECTOR_FIELDS, unavailable: ["probe"], covered: 0, steps: [], count: rows.length, rows, estimated_envelope_bytes: 0 };
    }
    return previewProcess(payload.process ?? [], Math.min(Math.max(payload.sample ?? 50, 0), 200));
  };

  handlers["subscription/identities"] = () => {
    if (fleetState() === "slow") return hold();
    const identities = harnessIdentities().map(({ boundTo: _bound, pendingOn: _pending, ...identity }) => identity);
    return { identities, count: identities.length };
  };

  handlers["bind/preview"] = ({ subscription_id, identity_id, revision }: { subscription_id: string; identity_id: string; revision?: string }) => {
    if (fleetState() === "slow") return hold();
    const record = records.find((rec) => rec.id === subscription_id);
    const identity = harnessIdentities().find((entry) => entry.id === identity_id);
    if (!record) throw new Error(`sub-store/bind preview: subscription "${subscription_id}" is not in the store`);
    if (!identity) throw new Error(`sub-store/bind preview: identity "${identity_id}" does not exist`);
    let rows: CatalogueRow[] = [];
    let provider: { name: string; protocol: string; server: string; port: number }[] = [];
    if (record.kind === "collection") {
      for (const id of record.members ?? []) {
        const member = records.find((rec) => rec.id === id);
        if (member?.source === "fleet") rows.push(...selectedRows(member.process ?? []));
        else if (member) provider = [
          { name: "Provider HK 01", protocol: "trojan", server: "hk1.provider.example", port: 443 },
          { name: "Provider JP 02", protocol: "vmess", server: "jp2.provider.example", port: 8443 },
        ];
      }
    } else {
      rows = selectedRows(record.process ?? []);
    }
    const entries: Record<string, unknown>[] = [];
    const excluded: Record<string, unknown>[] = [];
    let index = 0;
    for (const row of rows) {
      const at = index++;
      if (!BINDABLE.has(row.template?.protocol ?? row.protocol)) continue;
      const name = row.label ?? row.line_uuid;
      if (!identity.boundTo(row)) {
        excluded.push({ index: at, line_uuid: row.line_uuid, name, reason: "not_bound", fix: `Bind ${identity.name ?? identity.id} to this line in vpn-core, then apply the binding plan.` });
      } else if (row.service_state === "down") {
        excluded.push({ index: at, line_uuid: row.line_uuid, name, reason: "service_down", fix: "Start the line's service on its node, or wait for the node to report it running.", detail: `${row.node_name} reports the service down` });
      } else if (row.chain?.path_state === "drifted") {
        excluded.push({ index: at, line_uuid: row.line_uuid, name, reason: "graph_drifted", fix: "Apply the chain plan again so the path converges." });
      } else if (identity.pendingOn?.(row)) {
        excluded.push({ index: at, line_uuid: row.line_uuid, name, reason: "credential_not_applied", fix: "Approve the pending binding plan; the node applies the credential on its next sync." });
      } else {
        entries.push({ index: at, line_uuid: row.line_uuid, name, label: name, node_id: row.node_id, protocol: row.protocol, server: row.template?.host, port: row.template?.port, digest: `d${row.line_hash_id}` });
      }
    }
    for (const entry of provider) entries.push({ index: index++, provider: true, ...entry, digest: `p${index}` });
    const fleetNodes = rows.filter((row) => BINDABLE.has(row.template?.protocol ?? row.protocol)).length;
    const refused = rows.length === 0 && provider.length === 0 ? "empty_plan" : entries.filter((entry) => !entry.provider).length === 0 && fleetNodes > 0 ? "no_includable_line" : undefined;
    return {
      subscription_id,
      ...(revision ? { revision } : {}),
      identity_id,
      identity_status: identity.status,
      kind: "nodes",
      source_version: `lfe1-${CATALOGUE_VERSION.slice(5, 21)}`,
      live_revision: `rev-${subscription_id}-1`,
      entries,
      excluded,
      ...(refused ? { refused } : {}),
      fleet_nodes: fleetNodes,
    };
  };

  const list = handlers["subscription/list"]!;
  handlers["subscription/list"] = (payload) => {
    const reply = list(payload) as { subscriptions?: Record<string, unknown>[] };
    if (!s2Manifest() || !reply.subscriptions) return reply;
    const fleetIds = new Set(records.filter((rec) => rec.source === "fleet").map((rec) => rec.id));
    reply.subscriptions = reply.subscriptions.map((row) => {
      const rec = records.find((entry) => entry.id === row.id);
      if (!rec) return row;
      const flags = { ...((row.flags as Record<string, unknown>) ?? {}) };
      if (rec.source === "fleet") {
        flags.fleet_bound = true;
        const selected = selectedRows(rec.process ?? []).length;
        return {
          ...row,
          flags,
          nodes_in: selected,
          selection_version: fleetState() === "stale" ? STALE_VERSION : CATALOGUE_VERSION,
          ...(rec.id === "asia-fleet" ? { staged_revision: "rev-asia-fleet-staged-3f1a9c", staged_at: new Date(Date.now() - 600_000).toISOString(), staged_source: "fleet" } : {}),
        };
      }
      if (rec.kind === "collection" && (rec.members ?? []).some((id) => fleetIds.has(id))) return { ...row, flags: { ...flags, fleet_bound: true } };
      if (rec.source === "vpn-core" || rec.source === "vpn-core-graph") return { ...row, flags: { ...flags, owner_credentials: true } };
      return row;
    });
    return reply;
  };

  const operators = handlers["subscription/operators"]!;
  handlers["subscription/operators"] = (payload) => {
    const reply = operators(payload) as { operators: Record<string, unknown>[] };
    if (!s2Manifest()) return reply;
    return { operators: [...reply.operators, { type: "Structured Filter", scripting: false, fleet: true }, { type: "Structured Sort Operator", scripting: false, fleet: true }] };
  };

  const preview = handlers["subscription/preview"]!;
  const fleetPreview = (payload: { subscription_id?: string; operators?: unknown[]; target?: string; source?: string }) => {
    const record = records.find((rec) => rec.id === payload.subscription_id);
    const process = Array.isArray(payload.operators) ? payload.operators : (record?.process ?? []);
    const isFleet = payload.source === "fleet" || (!payload.source && record?.source === "fleet");
    if (record && (record.source === "vpn-core" || record.source === "vpn-core-graph")) {
      throw new Error(`owner_credentials_withheld: subscription "${record.id}" serves line owner credentials; migrate it to fleet to preview it`);
    }
    if (isFleet) {
      if (refuseRead()) return hold();
      const transformer = process.findIndex((step) => (step as Step)?.type === "Response Transformer" && !(step as Step).disabled);
      if (transformer !== -1) {
        throw new Error(`fleet_response_chain_unavailable: subscription "${record?.id ?? "draft"}": process step ${transformer + 1} (Response Transformer) does not run on fleet records yet`);
      }
      return fleetPreviewResult(selectedRows(process), payload.target ?? record?.target ?? "");
    }
    if (record?.kind === "collection" && (record.members ?? []).some((id) => records.find((rec) => rec.id === id)?.source === "fleet")) {
      if (refuseRead()) return hold();
      const rows = (record.members ?? []).flatMap((id) => {
        const member = records.find((rec) => rec.id === id);
        return member?.source === "fleet" ? selectedRows(member.process ?? []) : [];
      });
      return fleetPreviewResult(rows, payload.target ?? "", [
        { name: "Provider HK 01", type: "trojan", server: "hk1.provider.example", port: "443" },
        { name: "Provider JP 02", type: "vmess", server: "jp2.provider.example", port: "8443" },
      ]);
    }
    return preview(payload);
  };
  handlers["subscription/preview"] = (payload) => (s2Manifest() ? fleetPreview(payload) : preview(payload));
  const previewDraft = handlers["subscription/preview_draft"]!;
  handlers["subscription/preview_draft"] = (payload) => (s2Manifest() ? fleetPreview(payload) : previewDraft(payload));
}
