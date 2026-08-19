/**
 * Declarative argument forms for the operator catalogue.
 *
 * Seventeen operators would be seventeen bespoke components if each one owned
 * its own form. Instead each describes its arguments as fields and a single
 * renderer draws them, so adding an operator is a table entry rather than a new
 * file, and every operator's form behaves the same way.
 *
 * An operator with no entry here still works: the editor falls back to raw JSON
 * arguments. That matters because the catalogue is extracted from the bundled
 * engine by a test — a pin bump can introduce an operator this table has never
 * heard of, and the honest response is a usable text box rather than a form
 * that silently drops the arguments it does not understand.
 */

export type FieldKind =
  | "text"
  | "textarea"
  | "script"
  | "number"
  | "switch"
  | "tristate"
  | "select"
  | "multiselect"
  | "pairs";

export interface OperatorField {
  /** Key inside the operator's `args` object. */
  key: string;
  label: string;
  kind: FieldKind;
  hint?: string;
  placeholder?: string;
  options?: readonly { value: string; label: string }[];
  /** For `pairs`: the two column labels. */
  columns?: readonly [string, string];
  default?: unknown;
}

export interface OperatorSchema {
  /** Operator type exactly as the engine spells it. */
  type: string;
  /**
   * The short name shown on a button and in the chain.
   *
   * The engine's type strings are wire identifiers — "Add Proxies From
   * Subscription Operator" is not a label anyone wants to read on a button,
   * and a row of them reads as noise rather than as a list of choices.
   */
  label: string;
  /** Short sentence: what it does to the node list. */
  summary: string;
  /** "filter" keeps or drops nodes; "rewrite" changes them; "script" runs JS. */
  group: "filter" | "rewrite" | "script";
  /**
   * How `args` is shaped on the wire.
   *
   * Most operators take an object keyed by field. Several take the value
   * directly — `Regex Delete Operator` is handed `["cn"]`, `Sort Operator` is
   * handed `"asc"` — and wrapping those in `{value: …}` produces an operator
   * the engine either ignores or throws on. Every entry here was read out of
   * the bundled engine's constructor, not inferred from the name; inferring is
   * how they came to be wrong in the first place.
   */
  wire?: "object" | "bare";
  fields: readonly OperatorField[];
}

const REGIONS = [
  { value: "HK", label: "Hong Kong" },
  { value: "TW", label: "Taiwan" },
  { value: "JP", label: "Japan" },
  { value: "KR", label: "Korea" },
  { value: "SG", label: "Singapore" },
  { value: "US", label: "United States" },
  { value: "UK", label: "United Kingdom" },
  { value: "DE", label: "Germany" },
  { value: "FR", label: "France" },
  { value: "CN", label: "China" },
] as const;

const NODE_TYPES = [
  { value: "vless", label: "VLESS" },
  { value: "vmess", label: "VMess" },
  { value: "trojan", label: "Trojan" },
  { value: "ss", label: "Shadowsocks" },
  { value: "ssr", label: "ShadowsocksR" },
  { value: "hysteria", label: "Hysteria" },
  { value: "hysteria2", label: "Hysteria2" },
  { value: "tuic", label: "TUIC" },
  { value: "wireguard", label: "WireGuard" },
  { value: "http", label: "HTTP" },
  { value: "socks5", label: "SOCKS5" },
  { value: "snell", label: "Snell" },
] as const;

export const OPERATOR_SCHEMAS: readonly OperatorSchema[] = [
  {
    type: "Region Filter",
    label: "Region filter",
    summary: "Keep only nodes whose name matches the chosen regions.",
    group: "filter",
    fields: [
      { key: "value", label: "Regions", kind: "multiselect", options: REGIONS },
      {
        key: "keep",
        label: "Keep the matches",
        kind: "switch",
        default: true,
        hint: "Off inverts the filter: the chosen regions are the ones removed.",
      },
    ],
  },
  {
    type: "Type Filter",
    label: "Protocol filter",
    summary: "Keep only nodes of the chosen protocols.",
    group: "filter",
    fields: [
      { key: "value", label: "Protocols", kind: "multiselect", options: NODE_TYPES },
      { key: "keep", label: "Keep the matches", kind: "switch", default: true },
    ],
  },
  {
    type: "Regex Filter",
    label: "Regex filter",
    summary: "Keep or drop nodes whose name matches a pattern.",
    group: "filter",
    fields: [
      {
        key: "regex",
        label: "Patterns",
        kind: "textarea",
        placeholder: "One regular expression per line",
        hint: "A node matching any line matches the filter.",
      },
      { key: "keep", label: "Keep the matches", kind: "switch", default: true },
    ],
  },
  {
    type: "Conditional Filter",
    label: "Conditional filter",
    summary: "Keep nodes satisfying an expression over their fields.",
    group: "filter",
    fields: [
      {
        key: "rule",
        label: "Expression",
        kind: "textarea",
        placeholder: "type=vless AND port=443",
      },
    ],
  },
  {
    type: "Useless Filter",
    label: "Drop junk nodes",
    summary: "Drop nodes that carry traffic or expiry notices rather than a server.",
    group: "filter",
    fields: [],
  },
  {
    type: "Remove Duplicate Filter",
    label: "Drop duplicates",
    summary: "Drop nodes that repeat an earlier one.",
    group: "filter",
    fields: [],
  },
  {
    type: "Regex Rename Operator",
    wire: "bare",
    label: "Regex rename",
    summary: "Rewrite node names by pattern.",
    group: "rewrite",
    fields: [
      {
        key: "value",
        label: "Replacements",
        kind: "pairs",
        columns: ["Pattern", "Replace with"],
        hint: "Applied in order. Later rules see the output of earlier ones.",
      },
    ],
  },
  {
    type: "Regex Delete Operator",
    wire: "bare",
    label: "Regex delete",
    summary: "Strip matching text out of node names.",
    group: "rewrite",
    fields: [
      {
        key: "value",
        label: "Patterns",
        kind: "textarea",
        placeholder: "One regular expression per line",
      },
    ],
  },
  {
    type: "Flag Operator",
    label: "Country flags",
    summary: "Add or remove the country flag in front of a node name.",
    group: "rewrite",
    fields: [
      {
        key: "mode",
        label: "Action",
        kind: "select",
        default: "add",
        options: [
          { value: "add", label: "Add flags" },
          { value: "remove", label: "Remove flags" },
        ],
      },
    ],
  },
  {
    type: "Sort Operator",
    wire: "bare",
    label: "Sort",
    summary: "Order the node list.",
    group: "rewrite",
    fields: [
      {
        key: "value",
        label: "Order",
        kind: "select",
        default: "asc",
        options: [
          { value: "asc", label: "Ascending by name" },
          { value: "desc", label: "Descending by name" },
          { value: "random", label: "Random" },
        ],
      },
    ],
  },
  {
    type: "Regex Sort Operator",
    wire: "bare",
    label: "Regex sort",
    summary: "Order nodes by which pattern they match first.",
    group: "rewrite",
    fields: [
      {
        key: "value",
        label: "Patterns in order",
        kind: "textarea",
        placeholder: "One regular expression per line",
        hint: "Nodes matching the first line come first, and so on.",
      },
    ],
  },
  {
    type: "Handle Duplicate Operator",
    label: "Handle duplicates",
    summary: "Decide what happens to nodes that share a name.",
    group: "rewrite",
    fields: [
      {
        key: "action",
        label: "Action",
        kind: "select",
        default: "rename",
        options: [
          { value: "rename", label: "Rename the duplicates" },
          { value: "delete", label: "Delete the duplicates" },
          { value: "skip", label: "Leave them alone" },
        ],
      },
      { key: "template", label: "Rename template", kind: "text", placeholder: "$name $counter" },
      { key: "link", label: "Separator", kind: "text", placeholder: "-" },
      { key: "position", label: "Counter position", kind: "text", placeholder: "back" },
    ],
  },
  {
    type: "Quick Setting Operator",
    label: "Quick settings",
    summary: "Force protocol switches on every node.",
    group: "rewrite",
    fields: [
      { key: "udp", label: "UDP", kind: "tristate" },
      { key: "tfo", label: "TCP Fast Open", kind: "tristate" },
      { key: "scert", label: "Skip cert verify", kind: "tristate" },
      { key: "vmess aead", label: "VMess AEAD", kind: "tristate" },
    ],
  },
  {
    type: "Resolve Domain Operator",
    label: "Resolve domains",
    summary: "Replace hostnames with resolved addresses.",
    group: "rewrite",
    fields: [
      {
        key: "provider",
        label: "Resolver",
        kind: "select",
        default: "cloudflare",
        options: [
          { value: "cloudflare", label: "Cloudflare" },
          { value: "google", label: "Google" },
          { value: "ali", label: "Ali" },
          { value: "tencent", label: "Tencent" },
        ],
      },
      {
        key: "type",
        label: "Mode",
        kind: "select",
        default: "auto",
        options: [
          { value: "auto", label: "Auto" },
          { value: "remove-failed", label: "Remove nodes that fail to resolve" },
          { value: "IP-ONLY", label: "Keep only nodes that are already IPs" },
        ],
      },
      { key: "cache", label: "Use the resolver cache", kind: "switch", default: true },
    ],
  },
  {
    // The engine destructures {sourceType, sourceName, position} and resolves
    // the named source through produceArtifact. `value` — what this used to
    // write — matched nothing, so the operator resolved no source at all.
    //
    // It also acts only when the pipeline carries a $file, which this plugin's
    // file rendering does not yet set, so the step is inert here. The arguments
    // are correct now regardless; a control that writes the wrong shape is a
    // second bug waiting behind the first.
    type: "Add Proxies From Subscription Operator",
    label: "Append another subscription",
    summary: "Append nodes from another subscription. Needs the file pipeline.",
    group: "rewrite",
    fields: [
      {
        key: "sourceType",
        label: "Kind",
        kind: "select",
        default: "subscription",
        options: [
          { value: "subscription", label: "Subscription" },
          { value: "collection", label: "Combination" },
        ],
      },
      {
        key: "sourceName",
        label: "Name",
        kind: "text",
        placeholder: "The name it is stored under",
      },
      {
        key: "position",
        label: "Where",
        kind: "select",
        default: "replace",
        options: [
          { value: "replace", label: "Replace" },
          { value: "before", label: "Before" },
          { value: "after", label: "After" },
        ],
      },
    ],
  },
  {
    // The response path is where a document is edited. A proxy operator is
    // skipped there by design, so this is the only step that can change a
    // plain-text file.
    type: "Response Transformer",
    label: "Rewrite document",
    summary: "Run JavaScript over the served document.",
    group: "script",
    fields: [
      {
        key: "mode",
        label: "Source",
        kind: "select",
        default: "script",
        options: [
          { value: "script", label: "Inline script" },
          { value: "link", label: "Remote script URL" },
        ],
      },
      {
        key: "content",
        label: "Script",
        kind: "script",
        hint: "Define transformFunction(res) — it receives {status, headers, body} and returns it. Runs inside the engine's sandbox: no filesystem, and network only through $substore.http, which goes out under the server's egress guard (8 requests per call).",
      },
    ],
  },
  {
    type: "Script Operator",
    label: "Script",
    summary: "Run JavaScript over the whole node list.",
    group: "script",
    fields: [
      {
        key: "mode",
        label: "Source",
        kind: "select",
        default: "script",
        options: [
          { value: "script", label: "Inline script" },
          { value: "link", label: "Remote script URL" },
        ],
      },
      {
        key: "content",
        label: "Script",
        kind: "script",
        hint: "Receives the proxy list and returns it. Runs inside the engine's sandbox: no filesystem, and network only through $substore.http, which goes out under the server's egress guard (8 requests per call).",
      },
    ],
  },
  {
    type: "Script Filter",
    label: "Script filter",
    summary: "Run JavaScript that decides which nodes to keep.",
    group: "script",
    fields: [
      {
        key: "mode",
        label: "Source",
        kind: "select",
        default: "script",
        options: [
          { value: "script", label: "Inline script" },
          { value: "link", label: "Remote script URL" },
        ],
      },
      {
        key: "content",
        label: "Script",
        kind: "script",
        hint: "Returns an array of booleans, one per node. Runs inside the engine's sandbox.",
      },
    ],
  },
] as const;

const BY_TYPE = new Map(OPERATOR_SCHEMAS.map((schema) => [schema.type, schema]));

export function schemaFor(type: string): OperatorSchema | undefined {
  return BY_TYPE.get(type);
}

/** Starting arguments for a newly added step, from the schema's defaults. */
export function defaultArgs(type: string): Record<string, unknown> {
  const schema = schemaFor(type);
  if (!schema) return {};
  const args: Record<string, unknown> = {};
  for (const field of schema.fields) {
    if (field.default !== undefined) args[field.key] = field.default;
  }
  return args;
}

/**
 * Convert an operator's editor arguments to what the engine reads.
 *
 * `wire: "bare"` operators are handed their value directly — `Sort Operator`
 * receives `"asc"`, `Regex Delete Operator` receives `["cn"]`. Wrapping those in
 * `{value: …}` gave the constructor an object where it expected a string or an
 * array, so the operator sat in the chain doing nothing or threw at serve time.
 */
export function toWireArgs(type: string, args: Record<string, unknown>): unknown {
  const schema = schemaFor(type);
  const cleaned = dropBlankPairs(schema, args);
  if (!schema || schema.wire !== "bare") return cleaned;
  const field = schema.fields[0];
  return field ? cleaned[field.key] : cleaned;
}

/**
 * A pair row the operator started and left entirely empty is not a rule.
 *
 * It is dropped here, on the way to the wire, rather than while they type: the
 * editor keeps every row they created, because deleting a row out from under
 * someone mid-edit — which is what filtering on each keystroke did — reads as
 * the UI eating their work.
 */
function dropBlankPairs(
  schema: ReturnType<typeof schemaFor>,
  args: Record<string, unknown>,
): Record<string, unknown> {
  if (!schema) return args;
  let out = args;
  for (const field of schema.fields) {
    if (field.kind !== "pairs") continue;
    const rows = args[field.key];
    if (!Array.isArray(rows)) continue;
    const kept = rows.filter((row) => {
      const pair = row as { expr?: unknown; now?: unknown };
      return String(pair?.expr ?? "").trim() !== "" || String(pair?.now ?? "").trim() !== "";
    });
    if (kept.length === rows.length) continue;
    if (out === args) out = { ...args };
    if (kept.length) out[field.key] = kept;
    else delete out[field.key];
  }
  return out;
}

/**
 * The inverse, for loading a stored step into the editor.
 *
 * Both shapes are accepted: a record written before this was understood still
 * opens, and saving it writes the shape the engine wants.
 */
export function fromWireArgs(type: string, raw: unknown): Record<string, unknown> {
  const schema = schemaFor(type);
  if (raw && typeof raw === "object" && !Array.isArray(raw) && (!schema || schema.wire !== "bare")) {
    return raw as Record<string, unknown>;
  }
  if (!schema) return {};
  const field = schema.fields[0];
  if (!field) return {};
  if (raw === undefined || raw === null) return {};
  // A bare operator whose stored args are still `{value: …}` — the old, wrong
  // shape — reads its value back out rather than losing it.
  if (!Array.isArray(raw) && typeof raw === "object") {
    const wrapped = raw as Record<string, unknown>;
    if ("value" in wrapped) return { [field.key]: wrapped.value };
    return wrapped;
  }
  return { [field.key]: raw };
}
