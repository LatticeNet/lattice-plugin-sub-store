/**
 * The record sets the harness can serve, picked with `?fixture=` on its URL.
 *
 * `production` is the default and is production as read on 2026-09-29: the
 * same 23 records under the same names and ids, the same node counts in and
 * out, one share, and every record migrated. What production did not say is
 * invented and marked here: which file renders which record, the step counts,
 * the file contents, and the provider figures on 建材市场 (82 percent used,
 * six days to expiry, so the attention rules have something true-shaped to
 * trip). `failing` is the same store after a bad day, `large` fills the
 * 256-record budget, `states` is the bad day plus every state the Records
 * table must draw (a chain flagged for a regex rewrite, names long enough to
 * truncate in two scripts, an expired provider), and `canned` is the small
 * hand-made set the editor and layout drives were written against.
 *
 * Never imported by `src/`; the shipped bundle is built from index.html alone.
 */

export interface StoredRecord {
  id: string;
  kind?: string;
  name: string;
  display_name?: string;
  remark?: string;
  tags?: string[];
  source?: string;
  vpn_identity?: string;
  entry_roots?: string[];
  graph_options_version?: string;
  url?: string;
  content?: string;
  ua?: string;
  members?: string[];
  member_tags?: string[];
  failure_mode?: string;
  target?: string;
  file_type?: string;
  node_source?: string;
  query_params?: string[];
  arguments?: Record<string, string>;
  process?: unknown[];
  origin?: unknown;
  last_fetch_at?: string;
  last_fetch_ok?: boolean;
  last_error?: string;
  userinfo?: string;
}

export interface ShareRow {
  subscription_id: string;
  share_id: string;
  slug: string;
  enabled: boolean;
  default_format?: string;
  expires_at?: string;
  path: string;
  url?: string;
}

export interface Fixture {
  records: StoredRecord[];
  shares: ShareRow[];
  /** Nodes in and out of a full preview, by record id. Absent: the generic eight. */
  counts: Record<string, [number, number]>;
}

export type FixtureName = "production" | "failing" | "large" | "states" | "canned";
export const FIXTURE_NAMES: readonly FixtureName[] = ["production", "failing", "large", "states", "canned"];

const HOUR = 3600 * 1000;
const DAY = 24 * HOUR;
const GB = 1024 ** 3;

function ago(ms: number): string {
  return new Date(Date.now() - ms).toISOString();
}

function migrated(kind: string): { source: string; kind: string } {
  return { source: "sub-store", kind };
}

function steps(count: number): unknown[] {
  const chain = [
    { type: "Regex Filter", args: { value: ["香港|HK|日本|JP|新加坡|SG"], keep: true } },
    { type: "Regex Rename Operator", args: { value: [{ expr: "^\\[.*?\\]\\s*", now: "" }] } },
    { type: "Sort Operator", args: { value: "asc" } },
  ];
  return chain.slice(0, count);
}

const CONFIG_TEMPLATE = [
  "mixed-port: 7890",
  "mode: rule",
  "proxies: []",
  "proxy-groups:",
  "  - name: PROXY",
  "    type: select",
  "    include-all: true",
  "rules:",
  "  - MATCH,PROXY",
  "",
].join("\n");

const RULES_ONLY = "DOMAIN-SUFFIX,openjobs.internal,DIRECT\nDOMAIN-SUFFIX,metix.internal,DIRECT\nFINAL,DIRECT\n";

function file(name: string, nodeSource: string | undefined, extra: Partial<StoredRecord> = {}): StoredRecord {
  const plain = !nodeSource;
  return {
    id: `imported-file-${name}`,
    kind: "file",
    name,
    source: "local",
    file_type: plain ? "plain" : "config",
    node_source: nodeSource,
    content: plain ? RULES_ONLY : CONFIG_TEMPLATE,
    process: [],
    origin: migrated("file"),
    ...extra,
  };
}

const CD_SELF = "imported-cdcd-self-host";
const CD_BAK = "imported-cdcd-self-hostbak-20260820";
const OJ_HOST = "imported-openjobs-host";
const OJ_TROJAN = "imported-openjobs-host-trojan";
const JIANCAI = "imported-unnamed";
const MERGE_CD = "imported-col-merge-cd-openjobs";
const MERGE_OJ = "imported-col-merge-openjobs";

function productionRecords(): StoredRecord[] {
  // 建材市场: 410 GB of 500 GB used (82 percent) and six days left.
  const jiancaiExpire = Math.floor((Date.now() + 6 * DAY + 5 * HOUR) / 1000);
  return [
    {
      id: CD_BAK,
      name: "cdcd-self-host.bak-20260820",
      source: "local",
      content: "vless://00000000-0000-0000-0000-000000000000@bak.example:443#bak",
      process: [],
      origin: migrated("subscription"),
    },
    {
      id: CD_SELF,
      name: "cdcd-self-host",
      source: "local",
      content: "vless://11111111-1111-1111-1111-111111111111@self.example:443#self",
      process: [{ type: "Sort Operator", args: { value: "asc" } }],
      origin: migrated("subscription"),
    },
    {
      id: OJ_HOST,
      name: "openjobs-host",
      source: "local",
      content: "trojan://password@oj.example:443#oj",
      process: steps(1),
      origin: migrated("subscription"),
    },
    {
      id: OJ_TROJAN,
      name: "openjobs-host-trojan",
      source: "local",
      content: "trojan://password@oj-trojan.example:443#oj-trojan",
      process: [],
      origin: migrated("subscription"),
    },
    {
      id: JIANCAI,
      name: "建材市场",
      source: "remote",
      url: "https://vip.ding202507.xyz/api/v1/client/subscribe?token=jiancai-harness-token&flag=clash",
      process: steps(3),
      origin: migrated("subscription"),
      last_fetch_at: ago(2 * HOUR),
      last_fetch_ok: true,
      userinfo: `upload=${Math.round(12 * GB)}; download=${Math.round(398 * GB)}; total=${500 * GB}; expire=${jiancaiExpire}`,
    },
    {
      id: MERGE_CD,
      kind: "collection",
      name: "merge-cd-openjobs",
      members: [CD_SELF, OJ_HOST],
      failure_mode: "strict",
      process: [],
      origin: migrated("collection"),
    },
    {
      id: MERGE_OJ,
      kind: "collection",
      name: "merge-openjobs",
      members: [OJ_HOST],
      failure_mode: "strict",
      process: [],
      origin: migrated("collection"),
    },
    // Invented: which record each file renders. The names are production's.
    file("for-cdcd-egern", MERGE_CD),
    file("for-cdcd-loon", MERGE_CD),
    file("for-cdcd-self-use", CD_SELF),
    file("for-cdcd-shadowrocket", MERGE_CD),
    file("for-cdcd-stash", MERGE_CD),
    file("for-clash-novpn", undefined),
    file("for-loon-novpn", undefined),
    file("for-openjobs-egern", MERGE_OJ),
    file("for-openjobs-fangfang", JIANCAI),
    file("for-openjobs-inner", MERGE_OJ),
    file("for-openjobs-loon", MERGE_OJ),
    file("for-openjobs-metix-direct-only", undefined),
    file("for-openjobs-shadowrocket", MERGE_OJ),
    file("for-openjobs-shenzhen", OJ_TROJAN),
    file("for-openjobs-shenzhen-loon", OJ_TROJAN),
    file("for-openjobs-stash", MERGE_OJ),
  ];
}

const PRODUCTION_COUNTS: Record<string, [number, number]> = {
  [CD_BAK]: [28, 28],
  [CD_SELF]: [23, 23],
  [MERGE_CD]: [101, 101],
  [MERGE_OJ]: [78, 78],
  [OJ_HOST]: [86, 78],
  [OJ_TROJAN]: [26, 26],
  [JIANCAI]: [166, 25],
};

function productionShares(): ShareRow[] {
  return [
    {
      subscription_id: "imported-file-for-cdcd-loon",
      share_id: "sh-cdcd",
      slug: "cdcd",
      enabled: true,
      default_format: "plain",
      path: "/sub/cdcd/prodtokenprodtokenprodtokenprodtoken",
      url: "https://lattice.example/sub/cdcd/prodtokenprodtokenprodtokenprodtoken",
    },
  ];
}

/** Production after a bad day: every failure the attention list names. */
function failingFixture(): Fixture {
  const records = productionRecords();
  const trojan = records.find((record) => record.id === OJ_TROJAN)!;
  // A provider that answers 503.
  trojan.source = "remote";
  trojan.content = undefined;
  trojan.url = "https://sub.example-provider.com/api/v1/client/subscribe?token=9f8e7d6c5b4a3210&flag=trojan";
  trojan.last_fetch_at = ago(3 * HOUR);
  trojan.last_fetch_ok = false;
  trojan.last_error = `subscription "${OJ_TROJAN}" provider returned status 503 from https://sub.example-provider.com/api/v1/client/subscribe?token=9f8e7d6c5b4a3210`;
  trojan.userinfo = `upload=${Math.round(4 * GB)}; download=${Math.round(106 * GB)}; total=${100 * GB}; expire=${Math.floor((Date.now() - 2 * DAY) / 1000)}`;
  // A combination still naming the backup that was deleted.
  records.find((record) => record.id === MERGE_OJ)!.members = [OJ_HOST, "imported-openjobs-host-old"];
  // A file pointing at a record that is gone.
  records.find((record) => record.id === "imported-file-for-loon-novpn")!.node_source = "imported-col-merge-retired";
  records.find((record) => record.id === "imported-file-for-loon-novpn")!.file_type = "config";
  // A combination nothing renders.
  records.push({
    id: "imported-col-merge-spare",
    kind: "collection",
    name: "merge-spare",
    members: [CD_BAK],
    failure_mode: "skip-failed",
    process: [],
    origin: migrated("collection"),
  });
  const shares = productionShares();
  shares.push(
    {
      subscription_id: "imported-file-for-openjobs-loon",
      share_id: "sh-oj-loon",
      slug: "oj-loon",
      enabled: true,
      default_format: "plain",
      expires_at: new Date(Date.now() - 3 * DAY).toISOString(),
      path: "/sub/oj-loon/expiredtokenexpiredtokenexpiredtok",
      url: "https://lattice.example/sub/oj-loon/expiredtokenexpiredtokenexpiredtok",
    },
    {
      subscription_id: "imported-file-for-openjobs-stash",
      share_id: "sh-oj-stash",
      slug: "oj-stash",
      enabled: true,
      default_format: "plain",
      expires_at: new Date(Date.now() + 4 * DAY).toISOString(),
      path: "/sub/oj-stash/soontokensoontokensoontokensoontok",
      url: "https://lattice.example/sub/oj-stash/soontokensoontokensoontokensoontok",
    },
  );
  return { records, shares, counts: { ...PRODUCTION_COUNTS, "imported-col-merge-spare": [28, 28] } };
}

/** The 256-record budget nearly spent: 40 sources, 12 combinations, 180 files, 20 shares. */
function largeFixture(): Fixture {
  const records: StoredRecord[] = [];
  const counts: Record<string, [number, number]> = {};
  const regions = ["hk", "jp", "sg", "us", "de", "uk", "kr", "tw"];
  for (let i = 0; i < 40; i += 1) {
    const id = `src-${String(i + 1).padStart(2, "0")}`;
    const provider = i % 3 === 0;
    const used = 0.3 + ((i * 37) % 70) / 100;
    records.push({
      id,
      name: `${provider ? "provider" : "pasted"}-${regions[i % regions.length]}-${String(i + 1).padStart(2, "0")}`,
      source: provider ? "remote" : "local",
      url: provider ? `https://sub${i}.provider.example/api/v1/client/subscribe?token=large${i}` : undefined,
      content: provider ? undefined : `vless://${i}@n${i}.example:443#n${i}`,
      tags: [regions[i % regions.length]!],
      process: steps(i % 4),
      ...(provider
        ? {
            last_fetch_at: ago((i + 1) * HOUR),
            last_fetch_ok: i % 13 !== 0,
            last_error: i % 13 === 0 ? `subscription "${id}" provider returned status 502` : undefined,
            userinfo: `upload=${Math.round(used * 20 * GB)}; download=${Math.round(used * 180 * GB)}; total=${200 * GB}; expire=${Math.floor((Date.now() + ((i * 11) % 90) * DAY) / 1000)}`,
          }
        : {}),
    });
    // A source without a chain hands on every node; the rest run the Regex
    // filter first, which is the only step here that removes any.
    counts[id] = i % 4 === 0 ? [40 + i * 3, 40 + i * 3] : [40 + i * 3, 30 + i * 2];
  }
  for (let c = 0; c < 12; c += 1) {
    const id = `combo-${String(c + 1).padStart(2, "0")}`;
    const members = [0, 1, 2].map((k) => `src-${String(((c * 3 + k) % 40) + 1).padStart(2, "0")}`);
    records.push({ id, kind: "collection", name: `merge-team-${String(c + 1).padStart(2, "0")}`, members, member_tags: c % 4 === 0 ? [regions[c % regions.length]!] : [], process: [] });
    // A combination with no chain of its own is its members, every node.
    const total = members.reduce((sum, member) => sum + (counts[member]?.[1] ?? 0), 0);
    counts[id] = [total, total];
  }
  const people = ["alice", "bob", "carol", "dave", "erin", "frank", "grace", "heidi", "ivan", "judy", "mallory", "niaj"];
  const clients = ["loon", "stash", "egern", "shadowrocket", "surge", "clash", "mihomo", "sing-box", "qx", "v2ray", "surfboard", "loon-lite", "stash-ipad", "egern-mac", "surge-mac"];
  let n = 0;
  for (const person of people) {
    for (const client of clients) {
      const combo = `combo-${String((n % 12) + 1).padStart(2, "0")}`;
      records.push({
        id: `file-${person}-${client}`,
        kind: "file",
        name: `for-${person}-${client}`,
        source: "local",
        file_type: "config",
        node_source: n % 17 === 0 ? `src-${String((n % 40) + 1).padStart(2, "0")}` : combo,
        content: CONFIG_TEMPLATE,
        process: [],
      });
      n += 1;
    }
  }
  const shares: ShareRow[] = [];
  for (let s = 0; s < 20; s += 1) {
    const person = people[s % people.length]!;
    const client = clients[s % clients.length]!;
    shares.push({
      subscription_id: `file-${person}-${client}`,
      share_id: `sh-large-${s}`,
      slug: `${person}-${client}`,
      enabled: s % 7 !== 3,
      default_format: "plain",
      expires_at: s % 5 === 0 ? new Date(Date.now() + (s - 2) * DAY).toISOString() : undefined,
      path: `/sub/${person}-${client}/largetoken${s}`,
      url: `https://lattice.example/sub/${person}-${client}/largetoken${s}`,
    });
  }
  return { records, shares, counts };
}

/**
 * Every state the Records table draws, on top of the failing store: the
 * expired provider is already there (openjobs-host-trojan, two days past).
 * Added: a provider whose chain keeps everything except a few words with a
 * negative lookahead, the idiom the native engine cannot run and offers to
 * rewrite; a second flagged record whose pattern has no rewrite (a
 * backreference); and a source, a combination and a file whose names are long
 * in Latin and in CJK.
 */
const LONG_SOURCE = "imported-sub-long-name";
function statesFixture(): Fixture {
  const base = failingFixture();
  const records = base.records;
  records.splice(5, 0, {
    id: "imported-sub-flagged",
    name: "lookahead-provider",
    remark: "Keeps every node that does not mention expiry or the website",
    source: "remote",
    url: "https://sub.flagged-provider.example/api/v1/client/subscribe?token=flaggedtokenflaggedtoken&flag=clash",
    process: [
      { type: "Regex Filter", args: { regex: ["^(?!.*(过期|剩余|官网)).*$"], keep: true } },
      { type: "Sort Operator", args: { value: "asc" } },
    ],
    origin: migrated("subscription"),
    last_fetch_at: ago(5 * HOUR),
    last_fetch_ok: true,
    userinfo: `upload=${Math.round(2 * GB)}; download=${Math.round(40 * GB)}; total=${200 * GB}; expire=${Math.floor((Date.now() + 40 * DAY) / 1000)}`,
  });
  records.splice(6, 0, {
    id: "imported-sub-backref",
    name: "backreference-rename",
    source: "local",
    content: "trojan://password@backref.example:443#backref",
    process: [{ type: "Regex Rename Operator", args: { value: [{ expr: "(\\w+)-\\1", now: "$1" }] } }],
    origin: migrated("subscription"),
  });
  records.push(
    {
      id: LONG_SOURCE,
      name: "一个非常非常长的机场订阅名称用来检查截断与换行-and-a-very-long-latin-provider-subscription-name-as-well",
      remark: "备注也很长：这条订阅的名字在一行里放不下，表格必须省略而不是把其他列挤出去，手机上必须换行而不是横向滚动。",
      tags: ["长名称", "long-name", "provider"],
      source: "remote",
      url: "https://a-provider-with-a-long-host-name.example-subscriptions.invalid/api/v1/client/subscribe?token=longtokenlongtoken",
      process: steps(2),
      origin: migrated("subscription"),
      last_fetch_at: ago(9 * HOUR),
      last_fetch_ok: true,
      userinfo: `upload=${Math.round(30 * GB)}; download=${Math.round(160 * GB)}; total=${200 * GB}; expire=${Math.floor((Date.now() + 10 * DAY) / 1000)}`,
    },
    {
      id: "imported-col-long-name",
      kind: "collection",
      name: "combination-with-a-name-long-enough-to-need-truncation-on-every-screen-组合名称同样很长",
      members: [LONG_SOURCE, OJ_HOST],
      failure_mode: "strict",
      process: [],
      origin: migrated("collection"),
    },
    file("for-a-person-with-a-very-long-name-on-a-tablet-in-landscape-stash-给平板用的配置", LONG_SOURCE),
  );
  return {
    records,
    shares: base.shares,
    counts: {
      ...base.counts,
      "imported-sub-flagged": [120, 96],
      "imported-sub-backref": [12, 12],
      [LONG_SOURCE]: [64, 40],
      "imported-col-long-name": [126, 118],
    },
  };
}

export function fixture(name: FixtureName, canned: () => Fixture): Fixture {
  if (name === "failing") return failingFixture();
  if (name === "large") return largeFixture();
  if (name === "states") return statesFixture();
  if (name === "canned") return canned();
  return { records: productionRecords(), shares: productionShares(), counts: { ...PRODUCTION_COUNTS } };
}

export function fixtureName(search: string): FixtureName {
  const asked = new URLSearchParams(search).get("fixture");
  return (FIXTURE_NAMES as readonly string[]).includes(asked ?? "") ? (asked as FixtureName) : "production";
}

/** Production's list rows, for tests: what the plugin's `list` answers. */
export function productionFixture(): Fixture {
  return fixture("production", () => ({ records: [], shares: [], counts: {} }));
}
export { failingFixture, largeFixture, statesFixture };
