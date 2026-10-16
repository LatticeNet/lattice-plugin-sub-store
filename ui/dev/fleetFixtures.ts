/**
 * The fleet the harness serves with `?fixture=fleet` (design 28 S2): the
 * production record set plus fleet records, a fleet-bound combination, a
 * legacy export record, the line catalogue those records select from and the
 * identities a share may name.
 *
 * Shaped like production on 2026-10-08 (138 lines, eleven pushable selector
 * fields, no probe producer yet): 117 lines over 38 nodes, probe data
 * unavailable, CJK and long Latin node names, a few lines down, a chain
 * whose relay resolves to no fleet line, and shadowsocks lines core cannot
 * bind. Every
 * value is invented; none is a credential, and a catalogue row has no field
 * that could hold one.
 *
 * Never imported by `src/`; the shipped bundle is built from index.html alone.
 */
import type { CatalogueRow, FleetOptions, Identity } from "../src/fleet";
import type { Fixture, StoredRecord } from "./fixtures";

export type FleetRecord = StoredRecord & { fleet?: FleetOptions };

const DAY = 24 * 3600 * 1000;

/** The catalogue version the harness's catalogue answers with. */
export const CATALOGUE_VERSION = "lcv1-0a63b2c4d5e6f708192a3b4c5d6e7f80";
/** The version a stale snapshot was taken at (`?fleet=stale`). */
export const STALE_VERSION = "lcv1-9f1e2d3c4b5a69788796a5b4c3d2e1f0";

/** The eleven fields this server's catalogue can evaluate. */
export const SELECTOR_FIELDS = [
  "chain_roles",
  "countries",
  "group_ids",
  "line_uuids",
  "node_tags",
  "probe_passed_within_hours",
  "protocols",
  "regions",
  "renewal_within_days",
  "service_states",
  "transports",
];

interface NodeSpec {
  name: string;
  country: string;
  region: string;
  city: string;
  asn: number;
  as_org: string;
  provider: string;
  vendor: string;
  tags: string[];
  groups: string[];
  lines: string[];
  renewDays: number;
  ddns?: boolean;
  down?: number[];
}

const NODES: NodeSpec[] = [
  { name: "hk-hkt-01", country: "HK", region: "Hong Kong", city: "Kwai Chung", asn: 4760, as_org: "HKT Limited", provider: "ipinfo", vendor: "HKT", tags: ["asia", "premium"], groups: ["grp_asia", "grp_premium"], lines: ["vless-reality", "hy2", "trojan-ws", "ss-2022"], renewDays: 12, ddns: true },
  { name: "hk-hkt-02", country: "HK", region: "Hong Kong", city: "Kwai Chung", asn: 4760, as_org: "HKT Limited", provider: "ipinfo", vendor: "HKT", tags: ["asia", "premium"], groups: ["grp_asia", "grp_premium"], lines: ["vless-reality", "hy2", "tuic"], renewDays: 40 },
  { name: "hk-hgc-01", country: "HK", region: "Hong Kong", city: "Tseung Kwan O", asn: 9304, as_org: "HGC Global Communications", provider: "ipinfo", vendor: "HGC", tags: ["asia"], groups: ["grp_asia"], lines: ["vless-reality", "hy2", "anytls"], renewDays: 3, down: [1] },
  { name: "hk-hgc-02", country: "HK", region: "Hong Kong", city: "Tseung Kwan O", asn: 9304, as_org: "HGC Global Communications", provider: "ipinfo", vendor: "HGC", tags: ["asia"], groups: ["grp_asia"], lines: ["vless-reality", "trojan-ws", "ss-2022"], renewDays: 90 },
  { name: "香港沙田数据中心-电信CN2GIA-专线入口-非常长的节点名称", country: "HK", region: "Hong Kong", city: "Sha Tin", asn: 4809, as_org: "China Telecom Next Generation Carrier Network", provider: "ipinfo", vendor: "CTG", tags: ["asia", "cn2"], groups: ["grp_asia"], lines: ["hysteria2-salamander-obfuscated-udp-port-hopping", "vless-reality-vision"], renewDays: 20 },
  { name: "jp-tyo-iij-01", country: "JP", region: "Tokyo", city: "Tokyo", asn: 2497, as_org: "Internet Initiative Japan Inc.", provider: "ipinfo", vendor: "IIJ", tags: ["asia", "premium"], groups: ["grp_asia", "grp_premium"], lines: ["vless-reality", "hy2", "tuic", "ss-2022"], renewDays: 25, ddns: true },
  { name: "jp-tyo-iij-02", country: "JP", region: "Tokyo", city: "Tokyo", asn: 2497, as_org: "Internet Initiative Japan Inc.", provider: "ipinfo", vendor: "IIJ", tags: ["asia"], groups: ["grp_asia"], lines: ["vless-reality", "hy2", "trojan-ws"], renewDays: 60 },
  { name: "jp-tyo-iij-03", country: "JP", region: "Tokyo", city: "Tokyo", asn: 2497, as_org: "Internet Initiative Japan Inc.", provider: "ipinfo", vendor: "IIJ", tags: ["asia"], groups: ["grp_asia"], lines: ["vless-reality", "anytls"], renewDays: 8, down: [0] },
  { name: "jp-osa-sakura-01", country: "JP", region: "Osaka", city: "Osaka", asn: 9370, as_org: "SAKURA Internet Inc.", provider: "ipinfo", vendor: "Sakura", tags: ["asia"], groups: ["grp_asia"], lines: ["vless-reality", "hy2", "tuic"], renewDays: 180 },
  { name: "tokyo-iij-premium-residential-exit-node-with-a-very-long-name-04", country: "JP", region: "Tokyo", city: "Shinjuku", asn: 2497, as_org: "Internet Initiative Japan Inc.", provider: "ipinfo", vendor: "IIJ", tags: ["asia", "residential"], groups: ["grp_premium"], lines: ["vless-reality-vision-xtls-rprx", "hy2"], renewDays: 33 },
  { name: "sg-equinix-01", country: "SG", region: "Singapore", city: "Singapore", asn: 16509, as_org: "Amazon.com, Inc.", provider: "ipinfo", vendor: "AWS", tags: ["asia"], groups: ["grp_asia"], lines: ["vless-reality", "hy2", "trojan-ws", "tuic"], renewDays: 14 },
  { name: "sg-equinix-02", country: "SG", region: "Singapore", city: "Singapore", asn: 16509, as_org: "Amazon.com, Inc.", provider: "ipinfo", vendor: "AWS", tags: ["asia"], groups: ["grp_asia"], lines: ["vless-reality", "anytls", "ss-2022"], renewDays: 50 },
  { name: "tw-hinet-01", country: "TW", region: "Taipei", city: "Taipei", asn: 3462, as_org: "Chunghwa Telecom Co., Ltd.", provider: "ipinfo", vendor: "HiNet", tags: ["asia", "residential"], groups: ["grp_asia"], lines: ["vless-reality", "hy2", "trojan-ws"], renewDays: 75 },
  { name: "kr-sel-kt-01", country: "KR", region: "Seoul", city: "Seoul", asn: 4766, as_org: "Korea Telecom", provider: "ipinfo", vendor: "KT", tags: ["asia"], groups: ["grp_asia"], lines: ["vless-reality", "hy2", "tuic"], renewDays: 120 },
  { name: "us-lax-cn2-01", country: "US", region: "California", city: "Los Angeles", asn: 4134, as_org: "China Telecom (Americas)", provider: "ipinfo", vendor: "CN2", tags: ["americas", "cn2"], groups: ["grp_americas"], lines: ["vless-reality", "hy2", "trojan-ws", "ss-2022"], renewDays: 9 },
  { name: "us-lax-cn2-02", country: "US", region: "California", city: "Los Angeles", asn: 4134, as_org: "China Telecom (Americas)", provider: "ipinfo", vendor: "CN2", tags: ["americas", "cn2"], groups: ["grp_americas"], lines: ["vless-reality", "hy2", "tuic"], renewDays: 270, down: [2] },
  { name: "us-sjc-equinix-01", country: "US", region: "California", city: "San Jose", asn: 13335, as_org: "Cloudflare, Inc.", provider: "ipinfo", vendor: "Equinix", tags: ["americas"], groups: ["grp_americas"], lines: ["vless-reality", "anytls", "socks"], renewDays: 31 },
  { name: "us-nyc-01", country: "US", region: "New York", city: "New York", asn: 20473, as_org: "The Constant Company, LLC", provider: "ipinfo", vendor: "Vultr", tags: ["americas"], groups: ["grp_americas"], lines: ["vless-reality", "hy2", "trojan-ws"], renewDays: 44 },
  { name: "de-fra-hetzner-01", country: "DE", region: "Hesse", city: "Frankfurt", asn: 24940, as_org: "Hetzner Online GmbH", provider: "ipinfo", vendor: "Hetzner", tags: ["europe"], groups: ["grp_europe"], lines: ["vless-reality", "hy2", "tuic", "ss-2022"], renewDays: 18 },
  { name: "de-fra-hetzner-02", country: "DE", region: "Hesse", city: "Frankfurt", asn: 24940, as_org: "Hetzner Online GmbH", provider: "ipinfo", vendor: "Hetzner", tags: ["europe"], groups: ["grp_europe"], lines: ["vless-reality", "hy2", "trojan-ws"], renewDays: 100 },
  { name: "gb-lon-01", country: "GB", region: "England", city: "London", asn: 60068, as_org: "Datacamp Limited", provider: "ipinfo", vendor: "CDN77", tags: ["europe"], groups: ["grp_europe"], lines: ["vless-reality", "hy2", "anytls"], renewDays: 22 },
  { name: "nl-ams-01", country: "NL", region: "North Holland", city: "Amsterdam", asn: 14061, as_org: "DigitalOcean, LLC", provider: "ipinfo", vendor: "DigitalOcean", tags: ["europe"], groups: ["grp_europe"], lines: ["vless-reality", "hy2", "tuic", "trojan-ws"], renewDays: 66 },
  { name: "fr-par-01", country: "FR", region: "Ile-de-France", city: "Paris", asn: 16276, as_org: "OVH SAS", provider: "ipinfo", vendor: "OVH", tags: ["europe"], groups: ["grp_europe"], lines: ["vless-reality", "hy2", "ss-2022"], renewDays: 150 },
  { name: "ru-mow-01", country: "RU", region: "Moscow", city: "Moscow", asn: 9123, as_org: "TimeWeb Ltd.", provider: "ipinfo", vendor: "Timeweb", tags: ["europe"], groups: ["grp_europe"], lines: ["vless-reality", "hy2", "trojan-ws"], renewDays: 27 },
  { name: "au-syd-01", country: "AU", region: "New South Wales", city: "Sydney", asn: 16509, as_org: "Amazon.com, Inc.", provider: "ipinfo", vendor: "AWS", tags: ["oceania"], groups: [], lines: ["vless-reality", "hy2", "tuic"], renewDays: 200 },
  { name: "in-bom-01", country: "IN", region: "Maharashtra", city: "Mumbai", asn: 16509, as_org: "Amazon.com, Inc.", provider: "ipinfo", vendor: "AWS", tags: ["asia"], groups: ["grp_asia"], lines: ["vless-reality", "hy2", "trojan-ws"], renewDays: 35 },
  { name: "tr-ist-01", country: "TR", region: "Istanbul", city: "Istanbul", asn: 62240, as_org: "Clouvider Limited", provider: "ipinfo", vendor: "Clouvider", tags: ["europe"], groups: [], lines: ["vless-reality", "hy2", "anytls"], renewDays: 11 },
  { name: "br-gru-01", country: "BR", region: "Sao Paulo", city: "Sao Paulo", asn: 16509, as_org: "Amazon.com, Inc.", provider: "ipinfo", vendor: "AWS", tags: ["americas"], groups: ["grp_americas"], lines: ["vless-reality", "hy2", "tuic"], renewDays: 85 },
  { name: "ca-yyz-01", country: "CA", region: "Ontario", city: "Toronto", asn: 20473, as_org: "The Constant Company, LLC", provider: "ipinfo", vendor: "Vultr", tags: ["americas"], groups: ["grp_americas"], lines: ["vless-reality", "hy2", "trojan-ws"], renewDays: 140 },
  { name: "ae-dxb-01", country: "AE", region: "Dubai", city: "Dubai", asn: 16509, as_org: "Amazon.com, Inc.", provider: "ipinfo", vendor: "AWS", tags: ["middle-east"], groups: [], lines: ["vless-reality", "hy2", "tuic"], renewDays: 7 },
  { name: "za-jnb-01", country: "ZA", region: "Gauteng", city: "Johannesburg", asn: 37153, as_org: "Xneelo (Pty) Ltd", provider: "ipinfo", vendor: "Xneelo", tags: ["africa"], groups: [], lines: ["vless-reality", "hy2", "trojan-ws"], renewDays: 210 },
  { name: "mx-mex-01", country: "MX", region: "Mexico City", city: "Mexico City", asn: 8151, as_org: "Uninet S.A. de C.V.", provider: "ipinfo", vendor: "Telmex", tags: ["americas"], groups: ["grp_americas"], lines: ["vless-reality", "hy2", "ss-2022"], renewDays: 95 },
  { name: "vn-sgn-01", country: "VN", region: "Ho Chi Minh", city: "Ho Chi Minh City", asn: 7552, as_org: "Viettel Group", provider: "ipinfo", vendor: "Viettel", tags: ["asia"], groups: ["grp_asia"], lines: ["vless-reality", "hy2", "trojan-ws"], renewDays: 52 },
  { name: "th-bkk-01", country: "TH", region: "Bangkok", city: "Bangkok", asn: 45629, as_org: "JasTel Network", provider: "ipinfo", vendor: "JasTel", tags: ["asia"], groups: ["grp_asia"], lines: ["vless-reality", "hy2", "tuic"], renewDays: 63 },
  { name: "id-cgk-01", country: "ID", region: "Jakarta", city: "Jakarta", asn: 17451, as_org: "BIZNET NETWORKS", provider: "ipinfo", vendor: "Biznet", tags: ["asia"], groups: ["grp_asia"], lines: ["vless-reality", "hy2", "anytls"], renewDays: 77 },
  { name: "my-kul-01", country: "MY", region: "Kuala Lumpur", city: "Kuala Lumpur", asn: 4788, as_org: "TM Net, Internet Service Provider", provider: "ipinfo", vendor: "TM", tags: ["asia"], groups: ["grp_asia"], lines: ["vless-reality", "hy2", "trojan-ws"], renewDays: 130 },
  { name: "ph-mnl-01", country: "PH", region: "Metro Manila", city: "Manila", asn: 9299, as_org: "Philippine Long Distance Telephone", provider: "ipinfo", vendor: "PLDT", tags: ["asia"], groups: ["grp_asia"], lines: ["vless-reality", "hy2", "tuic"], renewDays: 46 },
  { name: "jp-hk-relay-entry", country: "JP", region: "Tokyo", city: "Tokyo", asn: 2497, as_org: "Internet Initiative Japan Inc.", provider: "ipinfo", vendor: "IIJ", tags: ["asia", "chain"], groups: ["grp_asia"], lines: ["vless-reality-chain", "hy2-chain", "trojan-ws-chain"], renewDays: 58 },
];

function protocolOf(line: string): { protocol: string; transport?: string; security?: string; port: number } {
  if (line.startsWith("vless")) return { protocol: "vless", transport: "tcp", security: "reality", port: 443 };
  if (line.startsWith("hy2") || line.startsWith("hysteria2")) return { protocol: "hysteria2", transport: "udp", security: "tls", port: 8443 };
  if (line.startsWith("trojan")) return { protocol: "trojan", transport: "ws", security: "tls", port: 443 };
  if (line.startsWith("tuic")) return { protocol: "tuic", transport: "udp", security: "tls", port: 9443 };
  if (line.startsWith("anytls")) return { protocol: "anytls", transport: "tcp", security: "tls", port: 8444 };
  if (line.startsWith("ss")) return { protocol: "shadowsocks", transport: "tcp", port: 8388 };
  return { protocol: "socks", transport: "tcp", port: 1080 };
}

function uuidFor(n: number): string {
  const hex = (n * 2654435761 >>> 0).toString(16).padStart(8, "0");
  const tail = (n * 40503 + 7).toString(16).padStart(12, "0").slice(-12);
  return `${hex}-${hex.slice(0, 4)}-4${hex.slice(1, 4)}-a${hex.slice(5, 8)}-${tail}`;
}

/** Every line of the harness fleet, in a stable order. */
export function catalogueRows(now: number = Date.now()): CatalogueRow[] {
  const rows: CatalogueRow[] = [];
  let n = 0;
  NODES.forEach((node, nodeIndex) => {
    // A node named in CJK still dials a DNS name, as production's do.
    const host = `${/^[\x21-\x7e]+$/.test(node.name) ? node.name.toLowerCase() : `hk-sha-tin-cn2-${nodeIndex}`}.fleet.example`;
    const edge = `203.0.113.${(nodeIndex % 250) + 1}`;
    node.lines.forEach((line, lineIndex) => {
      n += 1;
      const shape = protocolOf(line);
      const chained = node.tags.includes("chain");
      const down = node.down?.includes(lineIndex);
      const row: CatalogueRow = {
        line_uuid: uuidFor(n),
        line_hash_id: `lh_${n.toString(16).padStart(6, "0")}`,
        node_id: `node_${nodeIndex.toString().padStart(3, "0")}`,
        node_name: node.name,
        name: line,
        label: `${node.name} ${line}`.slice(0, 120),
        node_tags: node.tags,
        group_ids: node.groups,
        geo: { country: node.country, region: node.region, city: node.city, asn: node.asn, as_org: node.as_org, provider: node.provider },
        machine: { vendor: node.vendor, region: node.region, next_renewal: new Date(now + node.renewDays * DAY).toISOString() },
        protocol: shape.protocol,
        ...(shape.transport ? { transport: shape.transport } : {}),
        ...(shape.security ? { security: shape.security } : {}),
        public_host: edge,
        addresses: [edge, `2001:db8:${nodeIndex.toString(16)}::${lineIndex + 1}`],
        managed: false,
        overlay: false,
        status: "applied",
        service_state: down ? "down" : nodeIndex === 21 && lineIndex === 1 ? "restarting" : "running",
        chain: chained
          ? lineIndex === 2
            ? { role: "relay", unresolved: true }
            : { role: "entry", root: true, path_state: lineIndex === 1 ? "drifted" : "converged", downstream_line_uuid: uuidFor(1), exit_geo: { country: "HK", region: "Hong Kong", city: "Kwai Chung", asn: 4760, as_org: "HKT Limited" }, path_service_state: "running" }
          : { role: "single" },
        probe: null,
        template: { protocol: shape.protocol, host, port: shape.port, ...(shape.security === "reality" ? { params: { security: "reality", sni: "www.example.com", fp: "chrome" } } : {}) },
      };
      if (node.ddns) row.ddns_names = [{ name: `${node.name}.dyn.fleet.example`, verified: true }, { name: `${node.name}-v6.dyn.fleet.example`, verified: false }];
      rows.push(row);
    });
  });
  return rows;
}

/** The identities vpn-core holds, each with the lines it is bound to. */
export interface HarnessIdentity extends Identity {
  boundTo: (row: CatalogueRow) => boolean;
  /** Bound, but the node has not applied the credential yet. */
  pendingOn?: (row: CatalogueRow) => boolean;
}

export function harnessIdentities(now: number = Date.now()): HarnessIdentity[] {
  const asia = (row: CatalogueRow) => ["HK", "JP", "SG", "TW"].includes(row.geo?.country ?? "");
  return [
    { id: "vu_alice", name: "alice-laptop", group: "staff", status: "active", bound_lines: 52, expires_at: new Date(now + 120 * DAY).toISOString(), boundTo: asia, pendingOn: (row) => row.node_name === "hk-hgc-02" },
    { id: "vu_bob", name: "bob-phone", group: "staff", status: "active", bound_lines: 6, boundTo: (row) => row.node_name?.startsWith("hk-hkt") === true },
    { id: "vu_carol", name: "carol 张", group: "guests", status: "active", bound_lines: 0, boundTo: () => false },
    { id: "vu_dave", name: "dave-contractor", group: "contractors", status: "expired", reason: "expiry", bound_lines: 31, expires_at: new Date(now - 3 * DAY).toISOString(), boundTo: asia },
    { id: "vu_erin", name: "erin-tablet", group: "staff", status: "over_quota", reason: "quota", bound_lines: 12, boundTo: (row) => row.geo?.country === "JP" },
    { id: "vu_frank", name: "frank-old-router", status: "disabled", reason: "disabled", bound_lines: 4, boundTo: (row) => row.geo?.country === "US" },
    { id: "vu_oncall", name: "运维值班手机-shenzhen-office-oncall-rotation-device-07", group: "ops", status: "active", bound_lines: 138, boundTo: () => true },
  ];
}

const filter = (predicates: unknown[], extra: Record<string, unknown> = {}) => ({
  type: "Structured Filter",
  args: { mode: "keep", match: "all", predicates, ...extra },
});

/** The fleet records, a fleet-bound combination and a legacy export record. */
export function fleetRecords(now: number = Date.now()): FleetRecord[] {
  const ago = (ms: number) => new Date(now - ms).toISOString();
  return [
    {
      id: "asia-fleet",
      name: "asia-fleet",
      display_name: "亚洲线路 Asia lines",
      tags: ["fleet", "asia"],
      source: "fleet",
      process: [
        filter([
          { field: "country", op: "in", values: ["HK", "JP", "SG"] },
          { field: "protocol", op: "in", values: ["vless", "hysteria2"] },
        ]),
        filter([{ field: "service_state", op: "in", values: ["down"] }], { mode: "drop" }),
        { type: "Regex Rename Operator", args: [{ expr: "-reality$", now: "" }] },
      ],
      fleet: { probe_exclusion: { enabled: true }, usage_exclusion: { enabled: true, max_bytes_per_line: 50 * 1024 ** 3 } },
      last_fetch_at: ago(40 * 60 * 1000),
      last_fetch_ok: true,
    },
    {
      id: "all-fleet-lines",
      name: "all-fleet-lines",
      display_name: "Every fleet line",
      tags: ["fleet"],
      source: "fleet",
      process: [],
      last_fetch_at: ago(20 * 60 * 1000),
      last_fetch_ok: true,
    },
    {
      id: "fleet-probe-fast",
      name: "fleet-probe-fast",
      display_name: "Lines that passed a probe today",
      tags: ["fleet"],
      source: "fleet",
      process: [filter([{ field: "probe_passed_hours", op: "lte", value: 24 }])],
    },
    {
      id: "fleet-with-rewrite",
      name: "fleet-with-rewrite",
      display_name: "Fleet lines with a document rewrite",
      tags: ["fleet"],
      source: "fleet",
      process: [
        filter([{ field: "region", op: "in", values: ["Tokyo"] }]),
        { type: "Response Transformer", args: { mode: "script", content: "function transformFunction(res) { return res; }" } },
      ],
    },
    {
      id: "asia-combination",
      kind: "collection",
      name: "asia-combination",
      display_name: "Asia combination with a provider",
      members: ["asia-fleet", "openjobs-host"],
      failure_mode: "strict",
      process: [],
      fleet: { ddns_dial: true },
    },
    {
      id: "self-export",
      name: "self-export",
      display_name: "This fleet (legacy export)",
      tags: ["home"],
      source: "vpn-core",
      process: [{ type: "Regex Filter", args: { regex: ["HK|JP"], keep: true } }],
      last_fetch_at: ago(3 * 3600 * 1000),
      last_fetch_ok: true,
    },
  ];
}

/**
 * The production set with the fleet records first. openjobs-host is a remote
 * provider record in the canned set; it is added here when production lacks
 * it, so the combination has a provider member.
 */
export function fleetFixture(base: Fixture): Fixture {
  const fleet = fleetRecords();
  const ids = new Set(fleet.map((rec) => rec.id));
  const records = [...fleet, ...base.records.filter((rec) => !ids.has(rec.id))];
  if (!records.some((rec) => rec.id === "openjobs-host")) {
    records.push({
      id: "openjobs-host",
      name: "openjobs-host",
      tags: ["paid"],
      source: "remote",
      url: "https://sub.example-provider.com/api/v1/client/subscribe?token=9f8e7d6c5b4a32100123456789abcdef",
      process: [],
    });
  }
  return { records, shares: base.shares, counts: base.counts };
}
