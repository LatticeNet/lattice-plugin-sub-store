/**
 * fleet.ts, the fleet source's wire shapes and the rules the editor applies
 * before a round trip (design 28 S2, s2-plan sections 2.1, 2.4, 2.7, 2.8).
 *
 * A fleet record is an identity-free structured selection over vpn-core's
 * line catalogue: its leading run of Structured Filter steps is the
 * selection, and each share names the identity whose credentials it
 * receives. The plugin's shapes below mirror system-go exactly
 * (subscription_fleet_proxy.go, snapshot_envelope.go, operators/structured.go,
 * subscription_store.go); core's replies come from core-types.ts, which the
 * server's tools/tsgen generates from its JSON tags. structuredFields.test.ts
 * reads the Go source and holds the predicate table, the sort keys and the
 * bind-shape protocols below equal to it, so a field added there fails here
 * rather than rendering as a field the editor cannot offer.
 *
 * Nothing in this module ever holds a credential: catalogue rows are
 * credential-free by the catalogue's contract, a fleet preview's document
 * carries placeholders, and bind.preview's entries carry no credential
 * field.
 */
import type { BindExclusion, BindPreviewReply, PlanStatusRow } from "./core-types";
import { t } from "./i18n";

export type { BindEntry, BindExclusion, BindPreviewReply } from "./core-types";

// ── record options (s2-plan section 2.1) ────────────────────────────────────

export interface ProbeExclusion {
  enabled: boolean;
  /** 1 to 10; the plugin reads 3 when it is left out. */
  consecutive_failures?: number;
}

export interface UsageExclusion {
  enabled: boolean;
  /** Bytes the share's identity may use on one line per period before core excludes it. */
  max_bytes_per_line: number;
}

/** The per-record binding rules. Every field is off by default and omitted when off. */
export interface FleetOptions {
  ddns_dial?: boolean;
  probe_exclusion?: ProbeExclusion;
  usage_exclusion?: UsageExclusion;
}

export const DEFAULT_PROBE_FAILURES = 3;
export const MIN_PROBE_FAILURES = 1;
export const MAX_PROBE_FAILURES = 10;

/**
 * The options as the plugin stores them: off rules left out, an empty set
 * left out whole, so a record that opts into nothing carries no `fleet` key
 * and its revision does not move for an untouched group.
 */
export function normalizeFleetOptions(options: FleetOptions | undefined): FleetOptions | undefined {
  if (!options) return undefined;
  const out: FleetOptions = {};
  if (options.ddns_dial) out.ddns_dial = true;
  const probe = options.probe_exclusion;
  if (probe?.enabled) {
    out.probe_exclusion = { enabled: true };
    if (probe.consecutive_failures !== undefined && probe.consecutive_failures !== DEFAULT_PROBE_FAILURES) {
      out.probe_exclusion.consecutive_failures = probe.consecutive_failures;
    }
  }
  const usage = options.usage_exclusion;
  if (usage?.enabled) out.usage_exclusion = { enabled: true, max_bytes_per_line: usage.max_bytes_per_line };
  return Object.keys(out).length ? out : undefined;
}

/** Why the options would be refused at save, or "" when they would not. */
export type FleetOptionsProblem = "" | "probeRange" | "usagePositive";

export function fleetOptionsProblem(options: FleetOptions | undefined): FleetOptionsProblem {
  const probe = options?.probe_exclusion;
  if (probe?.enabled && probe.consecutive_failures !== undefined) {
    const n = probe.consecutive_failures;
    if (!Number.isInteger(n) || n < MIN_PROBE_FAILURES || n > MAX_PROBE_FAILURES) return "probeRange";
  }
  const usage = options?.usage_exclusion;
  if (usage?.enabled && !(Number.isInteger(usage.max_bytes_per_line) && usage.max_bytes_per_line > 0)) return "usagePositive";
  return "";
}

// ── the two Lattice-only steps (s2-plan section 2.4) ────────────────────────

export const STRUCTURED_FILTER_TYPE = "Structured Filter";
export const STRUCTURED_SORT_TYPE = "Structured Sort Operator";
export const FLEET_STEP_TYPES: readonly string[] = [STRUCTURED_FILTER_TYPE, STRUCTURED_SORT_TYPE];

export type PredicateOp = "in" | "not_in" | "lte" | "gte" | "true" | "false" | "present" | "absent";

export interface Predicate {
  field: string;
  op: PredicateOp;
  /** in and not_in. */
  values?: string[];
  /** lte and gte. */
  value?: number;
}

export interface StructuredFilterArgs {
  mode: "keep" | "drop";
  match: "all" | "any";
  /** Which geo the geo fields read for a chained line; exit when left out. */
  geo?: "exit" | "entry";
  predicates: Predicate[];
}

export interface StructuredSortArgs {
  key: string;
  order: "asc" | "desc";
}

export type PredicateKind = "text" | "list" | "number" | "bool";

export interface PredicateFieldSpec {
  kind: PredicateKind;
  /** The SDK selector field an `in` (or, for the two clock fields, `lte`) predicate pushes to. */
  selector?: string;
  /** Reads the probe block, null until probes run on this server. */
  probe?: boolean;
  /** Reads the step's effective geo (exit by default, entry on the toggle). */
  geo?: boolean;
  /** Narrows the kind's operators for this field. */
  ops?: readonly PredicateOp[];
  min?: number;
  max?: number;
}

const KIND_OPS: Record<PredicateKind, readonly PredicateOp[]> = {
  text: ["in", "not_in", "present", "absent"],
  list: ["in", "not_in", "present", "absent"],
  number: ["lte", "gte", "present", "absent"],
  bool: ["true", "false"],
};

/** model.MaxLineCatalogueRenewalDays, model.MaxLineCatalogueProbeHours. */
const MAX_RENEWAL_DAYS = 3660;
const MAX_PROBE_HOURS = 8760;
const MAX_COLD_P50_MS = 600_000;
/** model.MaxSubscriptionRecordNodes: what a migrated record's line_uuid pin may name. */
export const MAX_PREDICATE_VALUES = 4096;
export const MAX_STRUCTURED_PREDICATES = 64;

/**
 * Every field a predicate may name, in the order the field select lists
 * them: what an operator reaches for first (where, what, how it runs), then
 * the chain, the machine and the probe. operators/structured.go's
 * predicateFields, field for field.
 */
export const PREDICATE_FIELDS: Readonly<Record<string, PredicateFieldSpec>> = {
  country: { kind: "text", geo: true, selector: "countries" },
  region: { kind: "text", geo: true, selector: "regions" },
  city: { kind: "text", geo: true },
  node_tag: { kind: "list", selector: "node_tags" },
  group: { kind: "list", selector: "group_ids" },
  protocol: { kind: "text", selector: "protocols" },
  transport: { kind: "text", selector: "transports" },
  security: { kind: "text" },
  service_state: { kind: "text", selector: "service_states" },
  status: { kind: "text" },
  line_uuid: { kind: "text", selector: "line_uuids" },
  chain_role: { kind: "text", selector: "chain_roles" },
  chain_root: { kind: "bool" },
  path_state: { kind: "text" },
  chain_unresolved: { kind: "bool" },
  asn: { kind: "text", geo: true },
  as_org: { kind: "text", geo: true },
  geo_provider: { kind: "text", geo: true },
  machine_vendor: { kind: "text" },
  renewal_days: { kind: "number", selector: "renewal_within_days", min: -MAX_RENEWAL_DAYS, max: MAX_RENEWAL_DAYS },
  managed: { kind: "bool" },
  overlay: { kind: "bool" },
  overlay_status: { kind: "text" },
  ddns_present: { kind: "bool" },
  probe_passed_hours: { kind: "number", probe: true, selector: "probe_passed_within_hours", min: 0, max: MAX_PROBE_HOURS, ops: ["lte", "gte"] },
  probe_vantage: { kind: "text", probe: true },
  probe_cold_p50_ms: { kind: "number", probe: true, min: 0, max: MAX_COLD_P50_MS },
  probe_udp_ok: { kind: "bool", probe: true },
};

export const PREDICATE_FIELD_NAMES: readonly string[] = Object.keys(PREDICATE_FIELDS);

/** The operators a field takes. */
export function opsOf(field: string): readonly PredicateOp[] {
  const spec = PREDICATE_FIELDS[field];
  if (!spec) return [];
  return spec.ops ?? KIND_OPS[spec.kind];
}

/** Whether an operator takes a list of values, one number, or nothing. */
export function opArity(op: PredicateOp): "values" | "value" | "none" {
  if (op === "in" || op === "not_in") return "values";
  if (op === "lte" || op === "gte") return "value";
  return "none";
}

/** The values a closed field can hold, offered as chips rather than typed. */
export const CLOSED_VALUES: Readonly<Record<string, readonly string[]>> = {
  chain_role: ["single", "entry", "relay", "exit"],
  path_state: ["converged", "drifted", "busy"],
  service_state: ["running", "down", "restarting", "unknown"],
};

/** The keys a Structured Sort may order by, operators/structured_sort.go's sortKeys. */
export const STRUCTURED_SORT_KEYS: readonly string[] = [
  "country",
  "region",
  "node_name",
  "service_state",
  "chain_role",
  "renewal_days",
  "probe_cold_p50_ms",
];

const LINE_UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const ASN = /^[1-9][0-9]{0,9}$/;

/** Why one predicate would be refused at save, as the plugin's check refuses it. */
export type PredicateProblem =
  | ""
  | "unknownField"
  | "badOp"
  | "needsValues"
  | "tooManyValues"
  | "emptyValue"
  | "lineUUID"
  | "asn"
  | "closedValue"
  | "needsValue"
  | "outOfRange";

export function predicateProblem(p: Predicate): PredicateProblem {
  const spec = PREDICATE_FIELDS[p.field];
  if (!spec) return "unknownField";
  if (!opsOf(p.field).includes(p.op)) return "badOp";
  const arity = opArity(p.op);
  if (arity === "values") {
    const values = p.values ?? [];
    if (values.length === 0) return "needsValues";
    if (values.length > MAX_PREDICATE_VALUES) return "tooManyValues";
    for (const v of values) {
      if (v === "") return "emptyValue";
      if (p.field === "line_uuid" && !LINE_UUID.test(v)) return "lineUUID";
      if (p.field === "asn" && !ASN.test(v)) return "asn";
      const closed = CLOSED_VALUES[p.field];
      if (closed && (p.field === "chain_role" || p.field === "path_state") && !closed.includes(v)) return "closedValue";
    }
  }
  if (arity === "value") {
    if (p.value === undefined || !Number.isFinite(p.value)) return "needsValue";
    if (p.value < (spec.min ?? -Infinity) || p.value > (spec.max ?? Infinity)) return "outOfRange";
  }
  return "";
}

/** A predicate problem in the active locale's words. */
export function problemText(found: PredicateProblem, field: string): string {
  if (!found) return "";
  if (found === "outOfRange") {
    const spec = PREDICATE_FIELDS[field];
    return t.structured.problems.outOfRange(spec?.min ?? 0, spec?.max ?? 0);
  }
  return t.structured.problems[found];
}

/** A fresh predicate over `field`, with the field's first operator and no value yet. */
export function newPredicate(field: string): Predicate {
  const op = opsOf(field)[0] ?? "in";
  const arity = opArity(op);
  if (arity === "values") return { field, op, values: [] };
  return { field, op };
}

/** A predicate moved to another operator, keeping what still applies. */
export function withOp(p: Predicate, op: PredicateOp): Predicate {
  const arity = opArity(op);
  if (arity === "values") return { field: p.field, op, values: p.values ?? [] };
  if (arity === "value") return p.value === undefined ? { field: p.field, op } : { field: p.field, op, value: p.value };
  return { field: p.field, op };
}

/** A predicate moved to another field: its operator survives when the field takes it. */
export function withField(p: Predicate, field: string): Predicate {
  const ops = opsOf(field);
  if (!ops.includes(p.op)) return newPredicate(field);
  const kept = withOp({ field, op: p.op }, p.op);
  if (opArity(p.op) === "values") kept.values = [];
  return kept;
}

/**
 * A stored Structured Filter's arguments, read leniently: the editor shows
 * what is there and the problems beside it, and the plugin decides at save.
 */
export function readFilterArgs(raw: unknown): StructuredFilterArgs {
  const value = raw && typeof raw === "object" && !Array.isArray(raw) ? (raw as Record<string, unknown>) : {};
  const predicates = Array.isArray(value.predicates)
    ? value.predicates
        .filter((entry): entry is Record<string, unknown> => Boolean(entry) && typeof entry === "object")
        .map((entry) => {
          const p: Predicate = { field: String(entry.field ?? ""), op: String(entry.op ?? "in") as PredicateOp };
          if (Array.isArray(entry.values)) p.values = entry.values.map((v) => String(v));
          if (typeof entry.value === "number") p.value = entry.value;
          return p;
        })
    : [];
  const args: StructuredFilterArgs = {
    mode: value.mode === "drop" ? "drop" : "keep",
    match: value.match === "any" ? "any" : "all",
    predicates,
  };
  if (value.geo === "entry") args.geo = "entry";
  return args;
}

/**
 * The wire form: defaults left out where the plugin fills them (geo exit),
 * values and value only where the operator takes them.
 */
export function writeFilterArgs(args: StructuredFilterArgs): Record<string, unknown> {
  const out: Record<string, unknown> = {
    mode: args.mode,
    match: args.match,
    predicates: args.predicates.map((p) => {
      const entry: Record<string, unknown> = { field: p.field, op: p.op };
      const arity = opArity(p.op);
      if (arity === "values") entry.values = [...(p.values ?? [])];
      if (arity === "value" && p.value !== undefined) entry.value = p.value;
      return entry;
    }),
  };
  if (args.geo === "entry") out.geo = "entry";
  return out;
}

export function readSortArgs(raw: unknown): StructuredSortArgs {
  const value = raw && typeof raw === "object" && !Array.isArray(raw) ? (raw as Record<string, unknown>) : {};
  const key = typeof value.key === "string" && STRUCTURED_SORT_KEYS.includes(value.key) ? value.key : STRUCTURED_SORT_KEYS[0]!;
  return { key, order: value.order === "desc" ? "desc" : "asc" };
}

/** Whether a step reads the effective geo, so its geo toggle means something. */
export function readsGeo(args: StructuredFilterArgs): boolean {
  return args.predicates.some((p) => PREDICATE_FIELDS[p.field]?.geo);
}

/** Whether a step names a field that reads the probe block. */
export function readsProbe(args: StructuredFilterArgs): boolean {
  return args.predicates.some((p) => PREDICATE_FIELDS[p.field]?.probe);
}

interface StepLike {
  type?: unknown;
  disabled?: unknown;
  args?: unknown;
  mode?: unknown;
}

/**
 * The leading structured run: the enabled Structured Filter steps at the
 * chain's head, disabled steps between them not ending it and disabled steps
 * after the last not counted (operators/pushdown.go LeadingRun). Answers the
 * run's length and the chain indexes of its enabled filters.
 */
export function leadingRun(process: readonly unknown[]): { length: number; filters: number[] } {
  let length = 0;
  const filters: number[] = [];
  for (let i = 0; i < process.length; i++) {
    const step = (process[i] ?? {}) as StepLike;
    if (step.disabled) continue;
    if (step.type === STRUCTURED_FILTER_TYPE) {
      length = i + 1;
      filters.push(i);
      continue;
    }
    break;
  }
  return { length, filters };
}

/**
 * A Script Operator or Script Filter in link mode, which a fleet-bound record
 * refuses until S3 pins remote programs by digest (fleet_script_link_unavailable).
 */
export function linkModeScriptSteps(process: readonly unknown[]): number[] {
  const out: number[] = [];
  process.forEach((raw, index) => {
    const step = (raw ?? {}) as StepLike;
    if (step.type !== "Script Operator" && step.type !== "Script Filter") return;
    const args = step.args && typeof step.args === "object" ? (step.args as { mode?: unknown }) : {};
    if (args.mode === "link") out.push(index);
  });
  return out;
}

/** Enabled Response Transformer steps, which a fleet record refuses until S3. */
export function responseTransformerSteps(process: readonly unknown[]): number[] {
  const out: number[] = [];
  process.forEach((raw, index) => {
    const step = (raw ?? {}) as StepLike;
    if (step.type === "Response Transformer" && !step.disabled) out.push(index);
  });
  return out;
}

// ── catalogue rows (snapshot_envelope.go fleetRow) ──────────────────────────

export interface Geo {
  country?: string;
  region?: string;
  city?: string;
  lat?: number;
  lon?: number;
  ip?: string;
  asn?: number;
  as_org?: string;
  provider?: string;
  source?: string;
  updated_at?: string;
}

export interface CatalogueChain {
  role: string;
  root?: boolean;
  downstream_line_uuid?: string;
  path_state?: string;
  exit_geo?: Geo;
  unresolved?: boolean;
  path_service_state?: string;
}

export interface CatalogueProbe {
  verdict: string;
  at: string;
  vantage?: string;
  consecutive_failures: number;
  cold_p50_ms?: number;
  udp_ok?: boolean;
}

export interface DDNSName {
  name: string;
  verified?: boolean;
  target?: string;
}

/**
 * One catalogue row as the plugin projects it: the SDK row without `extra`
 * and `usage`, and a template without its dropped list and digest. No
 * credential field exists on it.
 */
export interface CatalogueRow {
  line_uuid: string;
  line_hash_id: string;
  node_id: string;
  node_name?: string;
  name?: string;
  /** The label core gives the line in bind entries, which the fleet node is named by. */
  label?: string;
  node_tags?: string[];
  group_ids?: string[];
  geo?: Geo;
  machine?: { vendor?: string; region?: string; next_renewal?: string };
  ddns_names?: DDNSName[];
  protocol: string;
  transport?: string;
  security?: string;
  public_host?: string;
  provider_edge?: string;
  addresses?: string[];
  managed?: boolean;
  overlay?: boolean;
  overlay_status?: string;
  status?: string;
  service_state?: string;
  chain: CatalogueChain;
  probe: CatalogueProbe | null;
  template?: { protocol: string; host: string; port: number; params?: Record<string, string> };
}

/** The name a row reads under: core's label, else the line's own name on its node. */
export function rowLabel(row: Pick<CatalogueRow, "label" | "node_name" | "name" | "line_uuid">): string {
  if (row.label) return row.label;
  const parts = [row.node_name, row.name].filter(Boolean);
  return parts.length ? parts.join(" ") : row.line_uuid;
}

/** The geo a row's country, region and city read, by the step's choice; null when unknown. */
export function effectiveGeo(row: Pick<CatalogueRow, "geo" | "chain">, choice: "exit" | "entry" = "exit"): Geo | null {
  if (choice !== "entry") {
    if (row.chain?.unresolved) return null;
    if (row.chain?.exit_geo) return row.chain.exit_geo;
  }
  return row.geo ?? null;
}

/**
 * The template protocols core can bind (fleetCredentialFields in
 * subscription_fleet_render.go). A selected row of any other protocol,
 * shadowsocks among them, has no node: render drops it with a warning.
 */
export const BIND_SHAPE_PROTOCOLS: readonly string[] = ["anytls", "hysteria2", "socks", "trojan", "tuic", "vless", "vmess"];

export function hasBindShape(row: Pick<CatalogueRow, "template" | "protocol">): boolean {
  return BIND_SHAPE_PROTOCOLS.includes(row.template?.protocol ?? row.protocol);
}

/**
 * Values the selection's sample holds for a field, for the predicate's
 * typeahead: what the catalogue actually says, most frequent first, so an
 * operator picks "HK" rather than guessing whether it is "hk" or "Hong Kong".
 */
export function sampleValues(rows: readonly CatalogueRow[], field: string, choice: "exit" | "entry" = "exit"): string[] {
  const counts = new Map<string, number>();
  const add = (value: string | number | undefined | null): void => {
    if (value === undefined || value === null || value === "") return;
    const key = String(value);
    counts.set(key, (counts.get(key) ?? 0) + 1);
  };
  for (const row of rows) {
    const geo = effectiveGeo(row, choice);
    switch (field) {
      case "country": add(geo?.country); break;
      case "region": add(geo?.region); break;
      case "city": add(geo?.city); break;
      case "asn": add(geo?.asn); break;
      case "as_org": add(geo?.as_org); break;
      case "geo_provider": add(geo?.provider); break;
      case "node_tag": row.node_tags?.forEach(add); break;
      case "group": row.group_ids?.forEach(add); break;
      case "protocol": add(row.protocol); break;
      case "transport": add(row.transport); break;
      case "security": add(row.security); break;
      case "service_state": add(row.service_state); break;
      case "status": add(row.status); break;
      case "overlay_status": add(row.overlay_status); break;
      case "machine_vendor": add(row.machine?.vendor); break;
      case "chain_role": add(row.chain?.role); break;
      case "path_state": add(row.chain?.path_state); break;
      case "line_uuid": add(row.line_uuid); break;
      case "probe_vantage": add(row.probe?.vantage); break;
      default: break;
    }
  }
  for (const closed of CLOSED_VALUES[field] ?? []) if (!counts.has(closed)) counts.set(closed, 0);
  return [...counts.entries()].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0])).map(([value]) => value);
}

// ── fleet_preview (subscription_fleet_proxy.go) ─────────────────────────────

export type FleetPreviewRequest = { process: unknown[]; sample?: number } | { line_uuids: string[] };

/**
 * One leading structured step's count. Pushed steps run together in core, so
 * only the last pushed step reports, with the rows core answered as `out`
 * and no `in`, which no catalogue page states.
 */
export interface FleetPreviewStep {
  index: number;
  in?: number;
  out: number;
  pushed: boolean;
}

export interface FleetPreviewReply {
  catalogue_version: string;
  selector_fields: string[];
  unavailable?: string[];
  pushed?: Record<string, unknown>;
  covered: number;
  steps: FleetPreviewStep[];
  count: number;
  sample?: CatalogueRow[];
  rows?: CatalogueRow[];
  estimated_envelope_bytes: number;
}

/** The lookup form's ceiling: the method refuses more (line_uuids_too_many). */
export const LINE_LOOKUP_BATCH = 256;
/** The sample the editor asks for: enough to type ahead over, small against the 1 MiB reply. */
export const FLEET_SAMPLE = 50;
/** model.MaxSubscriptionRawBytes: the snapshot core keeps for one record. */
export const MAX_SNAPSHOT_BYTES = 4 * 1024 * 1024;

/** Whether the catalogue says probe data does not exist on this server yet. */
export function probeUnavailable(reply: Pick<FleetPreviewReply, "unavailable"> | null | undefined): boolean {
  return Boolean(reply?.unavailable?.includes("probe"));
}

/** How many of the selection's lines fit the snapshot bound, at the selection's own row size. */
export function linesThatFit(reply: Pick<FleetPreviewReply, "count" | "estimated_envelope_bytes">): number {
  if (reply.count <= 0 || reply.estimated_envelope_bytes <= 0) return 0;
  const perRow = reply.estimated_envelope_bytes / reply.count;
  return Math.max(0, Math.floor(MAX_SNAPSHOT_BYTES / perRow));
}

/**
 * The short form of an lcv1- version or a revision hash, as the proof lines
 * print it. `ellipsis: false` cuts it the way a short commit hash is cut, for
 * a sentence where an ellipsis would run into the punctuation after it.
 */
export function shortVersion(version: string | undefined, length = 8, ellipsis = true): string {
  if (!version) return "";
  const dash = version.indexOf("-");
  const head = dash > 0 && dash < 6 ? version.slice(0, dash + 1) : "";
  const body = version.slice(head.length);
  if (body.length <= length) return version;
  return `${head}${body.slice(0, length)}${ellipsis ? "…" : ""}`;
}

/**
 * The first condition of the leading run that is not finished yet (no value,
 * a value out of range), as 1-based step and condition numbers; null when
 * every condition can be evaluated. A selection with one is not read: its
 * counts would describe a condition nobody has finished typing.
 */
export function unfinishedCondition(process: readonly unknown[]): { step: number; condition: number } | null {
  const run = leadingRun(process);
  for (let position = 0; position < run.filters.length; position++) {
    const args = readFilterArgs((process[run.filters[position]!] as { args?: unknown } | undefined)?.args);
    if (args.predicates.length === 0) return { step: position + 1, condition: 1 };
    const at = args.predicates.findIndex((p) => predicateProblem(p) !== "");
    if (at !== -1) return { step: position + 1, condition: at + 1 };
  }
  return null;
}

/** A read method's message without the code it leads with, which the notice shows apart. */
export function withoutLeadingCode(message: string): string {
  return message.replace(/^\s*[a-z][a-z0-9_]*:\s*/, "");
}

/**
 * The row cache behind the detail drawer: rows looked up by line_uuid in
 * batches of at most 256, kept per catalogue version. A version move empties
 * it, because a row read under another version may have moved (its service
 * state, its addresses, its chain).
 */
export class LineRowCache {
  private version = "";
  private readonly rows = new Map<string, CatalogueRow>();
  private readonly inflight = new Map<string, Promise<void>>();

  constructor(private readonly lookup: (lineUUIDs: string[]) => Promise<FleetPreviewReply>) {}

  /** Rows already in hand, for a sample the editor read anyway. */
  remember(version: string, rows: readonly CatalogueRow[]): void {
    this.adopt(version);
    for (const row of rows) this.rows.set(row.line_uuid, row);
  }

  get(lineUUID: string): CatalogueRow | undefined {
    return this.rows.get(lineUUID);
  }

  get catalogueVersion(): string {
    return this.version;
  }

  /** Looks up every line not yet in hand, in batches of LINE_LOOKUP_BATCH. */
  async ensure(lineUUIDs: readonly string[]): Promise<void> {
    const missing = [...new Set(lineUUIDs)].filter((id) => id && !this.rows.has(id) && !this.inflight.has(id));
    const waits: Promise<void>[] = lineUUIDs.map((id) => this.inflight.get(id)).filter((p): p is Promise<void> => Boolean(p));
    for (let start = 0; start < missing.length; start += LINE_LOOKUP_BATCH) {
      const batch = missing.slice(start, start + LINE_LOOKUP_BATCH);
      const run = this.lookup(batch).then((reply) => {
        this.adopt(reply.catalogue_version);
        for (const row of reply.rows ?? []) this.rows.set(row.line_uuid, row);
      });
      const settled = run.finally(() => {
        for (const id of batch) this.inflight.delete(id);
      });
      for (const id of batch) this.inflight.set(id, settled);
      waits.push(settled);
    }
    await Promise.all(waits);
  }

  private adopt(version: string): void {
    if (version && version !== this.version) {
      if (this.version) this.rows.clear();
      this.version = version;
    }
  }
}

// ── identities (vpn-core's identity list, relayed) ──────────────────────────

export interface Identity {
  id: string;
  name?: string;
  group?: string;
  /** active, expired, over_quota or disabled. */
  status: string;
  reason?: string;
  bound_lines: number;
  expires_at?: string;
}

export interface IdentitiesReply {
  identities: Identity[];
  count: number;
}

export const IDENTITY_ACTIVE = "active";

/** An identity whose share serves the decoy whatever the plan binds. */
export function identityInactive(status: string | undefined): boolean {
  return Boolean(status) && status !== IDENTITY_ACTIVE;
}

// ── bind.preview, grouped for reading ───────────────────────────────────────

/** Excluded entries grouped by reason, largest group first, each keeping core's fix text. */
export function groupExclusions(excluded: readonly BindExclusion[]): { reason: string; entries: BindExclusion[] }[] {
  const groups = new Map<string, BindExclusion[]>();
  for (const entry of excluded) {
    const list = groups.get(entry.reason) ?? [];
    list.push(entry);
    groups.set(entry.reason, list);
  }
  return [...groups.entries()]
    .map(([reason, entries]) => ({ reason, entries }))
    .sort((a, b) => b.entries.length - a.entries.length || a.reason.localeCompare(b.reason));
}

/** bind.preview's reason for a line the identity holds no binding on. */
export const REASON_NOT_BOUND = "not_bound";

/** The share would answer the decoy: core refuses the plan for this identity. */
export function previewRefused(reply: Pick<BindPreviewReply, "refused"> | null | undefined): boolean {
  return Boolean(reply?.refused);
}

/**
 * vpn-core's Users view, where an identity's line bindings are made. A
 * parameterless internal route, which the console's navigation allowlist
 * admits (lattice-dashboard pluginNavigationModel.ts).
 */
export const VPN_CORE_USERS_ROUTE = "/plugins/latticenet.vpn-core/users";

// ── plans (s2-plan section 2.8; lane 6b reads these) ────────────────────────

/** Core's approval statuses (sdk/model/model.go). */
export type ApprovalStatus = "pending" | "approved" | "rejected" | "applied";

/**
 * A plan's status row. `stale` and `action` are lane 5b's additions to
 * core's row; until that server lands they are absent, and tsgen's
 * regenerated PlanStatusRow then carries them itself.
 */
export type PlanStatus = PlanStatusRow & { stale?: boolean; action?: "promote" | "delete" };

export type PlanState = ApprovalStatus | "stale" | "failed";

/** The approval reason core writes when a granted apply_revision call failed. */
export const APPLY_FAILED_PREFIX = "apply_revision failed";

/**
 * What a plan chip reads: "failed" for a plan approved whose apply failed
 * (core records it approved with the failure as its reason), "stale" from
 * core's own boolean (a stale plan is recorded rejected), else core's status.
 * Never parsed from the reason text except the one prefix core writes.
 */
export function planState(s: Pick<PlanStatus, "status" | "reason" | "stale">): PlanState {
  if (s.status === "approved" && (s.reason ?? "").startsWith(APPLY_FAILED_PREFIX)) return "failed";
  if (s.stale === true) return "stale";
  if (s.status === "pending" || s.status === "approved" || s.status === "rejected" || s.status === "applied") return s.status;
  return "pending";
}
