/**
 * scriptNetwork.ts, the fleet_script_network_unavailable refusal (S2-SR-2).
 *
 * A render that reaches fleet nodes runs its scripts with the network
 * withheld until S3's isolation lands, and a Script or Resolve Domain step
 * that asks for the network fails the call instead of being skipped. The
 * plugin's render, preview and preview_draft answer an error whose message
 * leads with the code (system-go/fleet_script_network.go). Core lifts it
 * into plans.propose, bind.preview and the editor's preview paths as a 409
 * whose error object names the record and the step
 * (lattice-server internal/server/substore_script_network.go):
 *
 *   {"code":"fleet_script_network_unavailable","record":"<id>",
 *    "step":{"position":<1-based>,"type":"<step type>"},"message":"<sentence>"}
 *
 * The bridge relays a host error's message and code only, so what reaches
 * this UI is core's sentence (the console's client appends the request id):
 *
 *   fleet_script_network_unavailable: a step asked for the network while
 *   record "coll" rendered, ...; step 2 (Script Operator) of record "prov"
 *   is the first step in that render that can ask for it, ...
 *
 * The record a refusal is about is the one holding the step, "prov" here,
 * which is not the record that rendered when the step sits in a member of
 * a combination. Core's own sentences reach this parser's tests through
 * core-types.ts (scriptNetworkRefusalSamples), so a change of wording on
 * either side fails there. The typed object, a reply's `refused`, and the
 * plugin's sentence (which names only the record) are read too, and
 * nothing else is: a message that does not lead with the code is not this
 * refusal. Every field is checked and bounded before a surface shows it.
 *
 * Core cannot say which step asked: the engine does not report it, so core
 * names the first enabled step that can ask. A surface says so unless the
 * step is proven (refusedStepIn's `certain`).
 */

export const SCRIPT_NETWORK_CODE = "fleet_script_network_unavailable";

/** The step types that can ask for the network: the scripts and the domain resolver. */
export const NETWORK_STEP_TYPES: ReadonlySet<string> = new Set(["Script Operator", "Script Filter", "Resolve Domain Operator"]);

export interface RefusedStep {
  /** 1-based, over the record's stored steps, disabled ones included. */
  position: number;
  type: string;
}

/** A step a surface resolved for a refusal, and whether it is proven to be the one that asked. */
export interface ResolvedStep extends RefusedStep {
  /**
   * The record that rendered holds the step, renders its own chain alone,
   * and that chain has no other step that can ask. Otherwise the step is
   * the first that can ask, which is all core can say.
   */
  certain: boolean;
}

export interface ScriptNetworkRefusal {
  /** The record holding the step core names, else the record that rendered; "" for a draft or when none is named. */
  record: string;
  /** The record that rendered, "" for a draft, null when the refusal does not say. */
  rendered: string | null;
  step: RefusedStep | null;
  /** Core's or the plugin's sentence without the leading code, "" when there is none. */
  message: string;
}

/** Core's bound on a record id (substoreBindMaxIDBytes), in characters here. */
const MAX_ID = 256;
const MAX_TYPE = 48;
const MAX_MESSAGE = 600;
const MAX_POSITION = 4096;
/** No sentence core writes is longer: two bounded ids and the fixed words. */
const MAX_SENTENCE = 4096;

/** An id as core validates one: not empty, no surrounding space, no control character, bounded. */
function cleanId(value: unknown): string {
  if (typeof value !== "string") return "";
  if (!value || value !== value.trim() || value.length > MAX_ID) return "";
  return /[\u0000-\u001f\u007f-\u009f]/.test(value) ? "" : value;
}

function cleanType(value: unknown): string {
  if (typeof value !== "string") return "";
  return value.replace(/[^A-Za-z0-9 ]/g, "").replace(/\s+/g, " ").trim().slice(0, MAX_TYPE).trim();
}

function cleanPosition(value: unknown): number {
  return typeof value === "number" && Number.isInteger(value) && value >= 1 && value <= MAX_POSITION ? value : 0;
}

function cleanStep(value: unknown): RefusedStep | null {
  const step = value as { position?: unknown; type?: unknown } | null;
  if (!step || typeof step !== "object") return null;
  const position = cleanPosition(step.position);
  return position ? { position, type: cleanType(step.type) } : null;
}

function cleanMessage(value: unknown): string {
  if (typeof value !== "string") return "";
  const text = value.replace(new RegExp(`^\\s*${SCRIPT_NETWORK_CODE}\\s*:\\s*`), "").replace(/\s+/g, " ").trim();
  return text.length > MAX_MESSAGE ? `${text.slice(0, MAX_MESSAGE - 1)}…` : text;
}

function fromObject(value: Record<string, unknown>): ScriptNetworkRefusal | null {
  if (value.code !== undefined && value.code !== SCRIPT_NETWORK_CODE) return null;
  return {
    record: cleanId(value.record),
    rendered: typeof value.rendered === "string" ? cleanId(value.rendered) : null,
    step: cleanStep(value.step),
    message: cleanMessage(value.message),
  };
}

/**
 * A Go %q string's body, unescaped: core writes record ids with %q, which
 * escapes a quote, a backslash and anything unprintable, and leaves
 * printable Unicode as it is. Null for an escape Go does not write.
 */
function unquoteGo(body: string): string | null {
  let out = "";
  for (let i = 0; i < body.length; i += 1) {
    const ch = body[i]!;
    if (ch !== "\\") {
      out += ch;
      continue;
    }
    const next = body[i + 1];
    const simple: Record<string, string> = { '"': '"', "\\": "\\", "'": "'", n: "\n", t: "\t", r: "\r", a: "\x07", b: "\b", f: "\f", v: "\v" };
    if (next !== undefined && next in simple) {
      out += simple[next];
      i += 1;
      continue;
    }
    const width = next === "x" ? 2 : next === "u" ? 4 : next === "U" ? 8 : 0;
    const hex = width ? body.slice(i + 2, i + 2 + width) : "";
    if (!width || !new RegExp(`^[0-9a-fA-F]{${width}}$`).test(hex)) return null;
    const code = Number.parseInt(hex, 16);
    if (code > 0x10ffff) return null;
    out += String.fromCodePoint(code);
    i += 1 + width;
  }
  return out;
}

/** A quoted id, as core and the plugin write one; the capture is the escaped body. */
const QUOTED = String.raw`"((?:[^"\\]|\\.){1,1024})"`;
/** Core names the step and the record holding it: `step N (Type) of record "X"`, or `of the draft`. */
const HOLDER = new RegExp(String.raw`\bstep (\d{1,4}) \(([A-Za-z0-9 ]{1,48})\) of (?:record ${QUOTED}|(the draft))`);
/** Core names the record that rendered: `while record "X" rendered`, or `while the draft rendered`. */
const RENDERED = new RegExp(String.raw`\bwhile (?:record ${QUOTED}|(the draft)) rendered\b`);
/** The plugin names the record that rendered and no step: `subscription "X" is fleet-bound`, or `the fleet draft`. */
const PLUGIN = new RegExp(String.raw`^(?:(?:collection|subscription|file|record) ${QUOTED}|(the fleet draft)) is fleet-bound\b`);
/** Any other sentence that names a step: `step N (Type)` or `step N: Type`. */
const ANY_STEP = /\bstep (\d{1,4})(?: \(([A-Za-z0-9 ]{1,48})\)|: ([A-Za-z][A-Za-z0-9 ]{0,47}?)(?=[,;.]|$))?/;

function quotedId(body: string | undefined): string {
  if (body === undefined) return "";
  const id = unquoteGo(body);
  return id === null ? "" : cleanId(id);
}

/** What core's or the plugin's sentence names: the record holding the step, the record that rendered, the step. */
function fromSentence(text: string): ScriptNetworkRefusal {
  const rest = text.slice(0, MAX_SENTENCE);
  const message = cleanMessage(text);
  const rendered = RENDERED.exec(rest);
  const renderedId = rendered ? (rendered[2] ? "" : quotedId(rendered[1])) : null;
  const holder = HOLDER.exec(rest);
  if (holder) {
    const position = cleanPosition(Number(holder[1]));
    return {
      record: holder[4] ? "" : quotedId(holder[3]),
      rendered: renderedId,
      step: position ? { position, type: cleanType(holder[2]) } : null,
      message,
    };
  }
  if (rendered) return { record: renderedId ?? "", rendered: renderedId, step: null, message };
  const plugin = PLUGIN.exec(rest);
  if (plugin) {
    const id = plugin[2] ? "" : quotedId(plugin[1]);
    return { record: id, rendered: id, step: null, message };
  }
  // Neither core's nor the plugin's wording: an id after "record", and a step if one is named.
  const record = /\brecord ([^\s:;,"]{1,256})/.exec(rest)?.[1] ?? "";
  const step = ANY_STEP.exec(rest);
  const position = step ? cleanPosition(Number(step[1])) : 0;
  return { record: cleanId(record), rendered: null, step: position ? { position, type: cleanType(step![2] ?? step![3] ?? "") } : null, message };
}

function fromText(text: string): ScriptNetworkRefusal | null {
  // One short lowercase prefix may come first ("script http: "), never a sentence.
  const lead = new RegExp(`^\\s*(?:[a-z][a-z0-9 _./-]{0,40}:\\s*)?${SCRIPT_NETWORK_CODE}\\s*:\\s*`).exec(text);
  if (!lead) {
    const trimmed = text.trim();
    if (trimmed.startsWith("{")) return fromJSON(trimmed);
    return null;
  }
  const rest = text.slice(lead[0].length).trim();
  if (rest.startsWith("{")) return fromJSON(rest) ?? fromSentence(rest);
  return fromSentence(rest);
}

function fromJSON(text: string): ScriptNetworkRefusal | null {
  try {
    const value = JSON.parse(text) as unknown;
    return value && typeof value === "object" && !Array.isArray(value) ? fromObject(value as Record<string, unknown>) : null;
  } catch {
    return null;
  }
}

/**
 * The refusal in whatever shape it came: an Error or a message, the typed
 * object, a reply carrying it as `refused` (or a bind preview's bare
 * `refused` code), or an error carrying it as `refusal`, `data` or
 * `details`. Null for anything else.
 */
export function scriptNetworkRefusal(source: unknown): ScriptNetworkRefusal | null {
  if (source === null || source === undefined) return null;
  if (typeof source === "string") return source === SCRIPT_NETWORK_CODE ? { record: "", rendered: null, step: null, message: "" } : fromText(source);
  if (typeof source !== "object") return null;
  const value = source as Record<string, unknown>;
  for (const key of ["refusal", "data", "details"]) {
    const inner = value[key];
    if (inner && typeof inner === "object" && (inner as { code?: unknown }).code === SCRIPT_NETWORK_CODE) return fromObject(inner as Record<string, unknown>);
  }
  if (value.refused !== undefined) {
    const inner = scriptNetworkRefusal(value.refused);
    if (inner) return inner;
  }
  if (value.code === SCRIPT_NETWORK_CODE && (value.record !== undefined || value.step !== undefined)) return fromObject(value);
  if (source instanceof Error || typeof value.message === "string") {
    const fromMessage = fromText(String(value.message ?? ""));
    if (fromMessage) return fromMessage;
  }
  return value.code === SCRIPT_NETWORK_CODE ? fromObject(value) : null;
}

/** The enabled steps of a chain that can ask for the network, 1-based. */
export function networkSteps(process: readonly unknown[]): RefusedStep[] {
  const out: RefusedStep[] = [];
  process.forEach((raw, index) => {
    const step = raw as { type?: unknown; disabled?: unknown } | null;
    if (step && typeof step.type === "string" && NETWORK_STEP_TYPES.has(step.type) && step.disabled !== true) out.push({ position: index + 1, type: step.type });
  });
  return out;
}

/**
 * The step a refusal is about in a chain the surface holds: the one the
 * refusal names when it names one, else the chain's only step that can ask
 * for the network. Null when neither says, so the surface names the record
 * and offers its steps rather than guessing between several.
 *
 * `ownChainOnly` says the surface's record renders its own chain and no
 * other record's (a subscription, not a combination or a file). Only then,
 * with the record that rendered holding the step and no other step of its
 * chain able to ask, is the step proven; anywhere else it is the first step
 * that can ask, which is what core names.
 */
export function refusedStepIn(refusal: ScriptNetworkRefusal, process: readonly unknown[], ownChainOnly = false): ResolvedStep | null {
  const candidates = networkSteps(process);
  const alone = ownChainOnly && refusal.rendered !== null && refusal.rendered === refusal.record && candidates.length === 1;
  if (refusal.step) {
    const stored = process[refusal.step.position - 1] as { type?: unknown } | undefined;
    const type = refusal.step.type || (stored && typeof stored.type === "string" ? stored.type : "");
    return { position: refusal.step.position, type, certain: alone && candidates[0]!.position === refusal.step.position };
  }
  return candidates.length === 1 ? { ...candidates[0]!, certain: false } : null;
}
