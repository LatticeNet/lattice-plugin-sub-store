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
 * engine by a test. A pin bump can introduce an operator this table has never
 * heard of, and the honest response is a usable text box rather than a form
 * that silently drops the arguments it does not understand.
 */
import { t } from "./i18n";

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
   * The engine's type strings are wire identifiers, "Add Proxies From
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
   * directly, `Regex Delete Operator` is handed `["cn"]`, `Sort Operator` is
   * handed `"asc"`, and wrapping those in `{value: …}` produces an operator
   * the engine either ignores or throws on. Every entry here was read out of
   * the bundled engine's constructor, not inferred from the name; inferring is
   * how they came to be wrong in the first place.
   */
  wire?: "object" | "bare";
  fields: readonly OperatorField[];
}

/**
 * The shape of each operator's form, without its words. The words (labels,
 * summaries, hints, placeholders, the text of a select's options) live in the
 * message table under `t.operators[type]`, read whenever a form is drawn, so
 * the same schema answers in every locale. An option that carries its own
 * `label` is a proper name (a protocol, a provider) and is not translated.
 */
interface FieldSpec {
  key: string;
  kind: FieldKind;
  options?: readonly { value: string; label?: string }[];
  default?: unknown;
}

interface OperatorSpec {
  type: string;
  group: OperatorSchema["group"];
  wire?: OperatorSchema["wire"];
  fields: readonly FieldSpec[];
}

const REGION_CODES = ["HK", "TW", "JP", "KR", "SG", "US", "UK", "DE", "FR", "CN"] as const;

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

const SCRIPT_SOURCE = [{ value: "script" }, { value: "link" }] as const;

const OPERATOR_SPECS: readonly OperatorSpec[] = [
  {
    type: "Region Filter",
    group: "filter",
    fields: [
      { key: "value", kind: "multiselect", options: REGION_CODES.map((value) => ({ value })) },
      { key: "keep", kind: "switch", default: true },
    ],
  },
  {
    type: "Type Filter",
    group: "filter",
    fields: [
      { key: "value", kind: "multiselect", options: NODE_TYPES },
      { key: "keep", kind: "switch", default: true },
    ],
  },
  {
    type: "Regex Filter",
    group: "filter",
    fields: [
      { key: "regex", kind: "textarea" },
      { key: "keep", kind: "switch", default: true },
    ],
  },
  {
    type: "Conditional Filter",
    group: "filter",
    // The placeholder is the expression grammar itself, the same in every locale.
    fields: [{ key: "rule", kind: "textarea" }],
  },
  { type: "Useless Filter", group: "filter", fields: [] },
  { type: "Remove Duplicate Filter", group: "filter", fields: [] },
  {
    type: "Regex Rename Operator",
    wire: "bare",
    group: "rewrite",
    fields: [{ key: "value", kind: "pairs" }],
  },
  {
    type: "Regex Delete Operator",
    wire: "bare",
    group: "rewrite",
    fields: [{ key: "value", kind: "textarea" }],
  },
  {
    type: "Flag Operator",
    group: "rewrite",
    fields: [{ key: "mode", kind: "select", default: "add", options: [{ value: "add" }, { value: "remove" }] }],
  },
  {
    type: "Sort Operator",
    wire: "bare",
    group: "rewrite",
    fields: [{ key: "value", kind: "select", default: "asc", options: [{ value: "asc" }, { value: "desc" }, { value: "random" }] }],
  },
  {
    type: "Regex Sort Operator",
    wire: "bare",
    group: "rewrite",
    fields: [{ key: "value", kind: "textarea" }],
  },
  {
    type: "Handle Duplicate Operator",
    group: "rewrite",
    fields: [
      { key: "action", kind: "select", default: "rename", options: [{ value: "rename" }, { value: "delete" }, { value: "skip" }] },
      { key: "template", kind: "text" },
      { key: "link", kind: "text" },
      { key: "position", kind: "text" },
    ],
  },
  {
    type: "Quick Setting Operator",
    group: "rewrite",
    fields: [
      { key: "udp", kind: "tristate" },
      { key: "tfo", kind: "tristate" },
      { key: "scert", kind: "tristate" },
      { key: "vmess aead", kind: "tristate" },
    ],
  },
  {
    type: "Resolve Domain Operator",
    group: "rewrite",
    fields: [
      {
        key: "provider",
        kind: "select",
        default: "cloudflare",
        options: [{ value: "cloudflare" }, { value: "google" }, { value: "ali" }, { value: "tencent" }],
      },
      { key: "type", kind: "select", default: "auto", options: [{ value: "auto" }, { value: "remove-failed" }, { value: "IP-ONLY" }] },
      { key: "cache", kind: "switch", default: true },
    ],
  },
  {
    // The engine destructures {sourceType, sourceName, position} and resolves
    // the named source through produceArtifact. `value`, what this used to
    // write, matched nothing, so the operator resolved no source at all.
    //
    // It also acts only when the pipeline carries a $file, which this plugin's
    // file rendering does not yet set, so the step is inert here. The arguments
    // are correct now regardless; a control that writes the wrong shape is a
    // second bug waiting behind the first.
    type: "Add Proxies From Subscription Operator",
    group: "rewrite",
    fields: [
      { key: "sourceType", kind: "select", default: "subscription", options: [{ value: "subscription" }, { value: "collection" }] },
      { key: "sourceName", kind: "text" },
      { key: "position", kind: "select", default: "replace", options: [{ value: "replace" }, { value: "before" }, { value: "after" }] },
    ],
  },
  {
    // The response path is where a document is edited. A proxy operator is
    // skipped there by design, so this is the only step that can change a
    // plain-text file.
    type: "Response Transformer",
    group: "script",
    fields: [
      { key: "mode", kind: "select", default: "script", options: SCRIPT_SOURCE },
      { key: "content", kind: "script" },
    ],
  },
  {
    type: "Script Operator",
    group: "script",
    fields: [
      { key: "mode", kind: "select", default: "script", options: SCRIPT_SOURCE },
      { key: "content", kind: "script" },
    ],
  },
  {
    type: "Script Filter",
    group: "script",
    fields: [
      { key: "mode", kind: "select", default: "script", options: SCRIPT_SOURCE },
      { key: "content", kind: "script" },
    ],
  },
];

/** Placeholders that are syntax rather than words, and so the same in every locale. */
const SYNTAX_PLACEHOLDERS: Record<string, Record<string, string>> = {
  "Conditional Filter": { rule: "type=vless AND port=443" },
  "Handle Duplicate Operator": { template: "$name $counter", link: "-", position: "back" },
};

interface FieldText {
  label: string;
  hint?: string;
  placeholder?: string;
  columns?: readonly string[];
  options?: Record<string, string>;
}

interface OperatorText {
  label: string;
  summary: string;
  fields: Record<string, FieldText>;
}

/** The active locale's words for one operator type. */
function textOf(type: string): OperatorText {
  return (t.operators as unknown as Record<string, OperatorText>)[type]!;
}

function fieldText(type: string, key: string): FieldText {
  return textOf(type).fields[key] ?? { label: key };
}

/**
 * A schema whose words are getters over the message table, so a form drawn
 * after the handshake reads the console's language, and one drawn in a test
 * reads English.
 */
function localized(spec: OperatorSpec): OperatorSchema {
  const fields = spec.fields.map<OperatorField>((field) => {
    const options = field.options?.map((option) => ({
      value: option.value,
      get label() {
        if (option.label) return option.label;
        if (spec.type === "Region Filter") return (t.regions as Record<string, string>)[option.value] ?? option.value;
        return fieldText(spec.type, field.key).options?.[option.value] ?? option.value;
      },
    }));
    return {
      key: field.key,
      kind: field.kind,
      ...(field.default !== undefined ? { default: field.default } : {}),
      ...(options ? { options } : {}),
      get label() {
        return fieldText(spec.type, field.key).label;
      },
      get hint() {
        return fieldText(spec.type, field.key).hint;
      },
      get placeholder() {
        return SYNTAX_PLACEHOLDERS[spec.type]?.[field.key] ?? fieldText(spec.type, field.key).placeholder;
      },
      get columns() {
        const columns = fieldText(spec.type, field.key).columns;
        return columns ? ([columns[0] ?? "", columns[1] ?? ""] as const) : undefined;
      },
    };
  });
  return {
    type: spec.type,
    group: spec.group,
    ...(spec.wire ? { wire: spec.wire } : {}),
    fields,
    get label() {
      return textOf(spec.type).label;
    },
    get summary() {
      return textOf(spec.type).summary;
    },
  };
}

export const OPERATOR_SCHEMAS: readonly OperatorSchema[] = OPERATOR_SPECS.map(localized);

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
 * `wire: "bare"` operators are handed their value directly, `Sort Operator`
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
 * someone mid-edit, which is what filtering on each keystroke did, reads as
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
  // A bare operator whose stored args are still `{value: …}`. The old, wrong
  // shape, reads its value back out rather than losing it.
  if (!Array.isArray(raw) && typeof raw === "object") {
    const wrapped = raw as Record<string, unknown>;
    if ("value" in wrapped) return { [field.key]: wrapped.value };
    return wrapped;
  }
  return { [field.key]: raw };
}

/**
 * What a numeric operator argument becomes when the box is blank or nonsense.
 *
 * `Number("")` is `0` and `Number("x")` is `NaN`, and neither is `undefined`,
 * so a setter that unsets on empty happily stored them: clearing a numeric
 * field wrote a literal `0` and typing a stray letter wrote `NaN`, which
 * serialises to `null` on the wire. For an operator argument that bounds
 * something (a keep count, a limit) a silent `0` is not "unset", it is an
 * instruction to keep nothing, applied to a record the operator was editing for
 * an unrelated reason.
 *
 * Blank means unset. Unparseable means unset too: the alternative is writing a
 * value nobody typed, and the field still shows what they did type.
 */
export function parseNumericArg(raw: string): number | undefined {
  const text = raw.trim();
  if (!text) return undefined;
  const value = Number(text);
  return Number.isFinite(value) ? value : undefined;
}
