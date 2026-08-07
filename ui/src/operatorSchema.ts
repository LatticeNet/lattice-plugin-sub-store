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
        key: "value",
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
        key: "value",
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
        key: "value",
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
        key: "mode",
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
    type: "Add Proxies From Subscription Operator",
    label: "Append another subscription",
    summary: "Append nodes from another subscription into this one.",
    group: "rewrite",
    fields: [
      {
        key: "value",
        label: "Subscription names",
        kind: "textarea",
        placeholder: "One name per line",
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
        hint: "Define transformFunction(res) — it receives {status, headers, body} and returns it. Runs inside the engine's sandbox — no filesystem, no network, no host calls.",
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
        hint: "Receives the proxy list and returns it. Runs inside the engine's sandbox — no filesystem, no network, no host calls.",
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
