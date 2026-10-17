/**
 * scriptNetwork.ts, the fleet_script_network_unavailable refusal (S2-SR-2).
 *
 * A render that reaches fleet nodes runs its scripts with the network
 * withheld until S3's isolation lands, and a Script or Resolve Domain step
 * that asks for the network fails the call instead of being skipped. The
 * plugin's render, preview and preview_draft answer an error whose message
 * leads with the code (system-go/fleet_script_network.go). Core lifts it
 * into plans.propose, bind.preview and the editor's preview paths as a
 * typed refusal:
 *
 *   {"code":"fleet_script_network_unavailable","record":"<id>",
 *    "step":{"position":<1-based>,"type":"<step type>"},"message":"<sentence>"}
 *
 * The bridge carries a host error's message and code only, so the refusal
 * arrives as that object (a reply's `refused`, or a future bridge's error
 * data), as JSON after the leading code, or as the plugin's sentence. All
 * three are read here into one shape, and nothing else is: a message that
 * does not lead with the code is not this refusal. Every field is checked
 * and bounded before a surface shows it.
 */

export const SCRIPT_NETWORK_CODE = "fleet_script_network_unavailable";

/** The step types that can ask for the network: the scripts and the domain resolver. */
export const NETWORK_STEP_TYPES: ReadonlySet<string> = new Set(["Script Operator", "Script Filter", "Resolve Domain Operator"]);

export interface RefusedStep {
  /** 1-based, over the record's stored steps, disabled ones included. */
  position: number;
  type: string;
}

export interface ScriptNetworkRefusal {
  /** The record whose step asked, "" when the refusal names none (a draft). */
  record: string;
  step: RefusedStep | null;
  /** Core's or the plugin's sentence without the leading code, "" when there is none. */
  message: string;
}

const MAX_ID = 128;
const MAX_TYPE = 48;
const MAX_MESSAGE = 600;
const MAX_POSITION = 4096;

function cleanId(value: unknown): string {
  if (typeof value !== "string") return "";
  const id = value.trim();
  return id.length <= MAX_ID && /^[^\s"<>]+$/.test(id) ? id : "";
}

function cleanType(value: unknown): string {
  if (typeof value !== "string") return "";
  return value.replace(/[^A-Za-z0-9 ]/g, "").replace(/\s+/g, " ").trim().slice(0, MAX_TYPE).trim();
}

function cleanStep(value: unknown): RefusedStep | null {
  const step = value as { position?: unknown; type?: unknown } | null;
  if (!step || typeof step !== "object") return null;
  const position = step.position;
  if (typeof position !== "number" || !Number.isInteger(position) || position < 1 || position > MAX_POSITION) return null;
  return { position, type: cleanType(step.type) };
}

function cleanMessage(value: unknown): string {
  if (typeof value !== "string") return "";
  const text = value.replace(new RegExp(`^\\s*${SCRIPT_NETWORK_CODE}\\s*:\\s*`), "").replace(/\s+/g, " ").trim();
  return text.length > MAX_MESSAGE ? `${text.slice(0, MAX_MESSAGE - 1)}…` : text;
}

function fromObject(value: Record<string, unknown>): ScriptNetworkRefusal | null {
  if (value.code !== undefined && value.code !== SCRIPT_NETWORK_CODE) return null;
  return { record: cleanId(value.record), step: cleanStep(value.step), message: cleanMessage(value.message) };
}

/** What the plugin's or core's sentence names: the record and, when it says one, the step. */
function fromSentence(rest: string): ScriptNetworkRefusal {
  const record = /\b(?:collection|subscription|record) "([^"]{1,128})"/.exec(rest)?.[1] ?? /\brecord ([A-Za-z0-9][A-Za-z0-9._-]{0,127})\b/.exec(rest)?.[1] ?? "";
  const step = /\bstep (\d{1,4})(?: \(([A-Za-z0-9 ]{1,48})\)|: ([A-Za-z][A-Za-z0-9 ]{0,47}?)(?=[,;.]|$))?/.exec(rest);
  const position = step ? Number(step[1]) : 0;
  return {
    record: cleanId(record),
    step: position >= 1 && position <= MAX_POSITION ? { position, type: cleanType(step![2] ?? step![3] ?? "") } : null,
    message: cleanMessage(rest),
  };
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
  if (typeof source === "string") return source === SCRIPT_NETWORK_CODE ? { record: "", step: null, message: "" } : fromText(source);
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
 */
export function refusedStepIn(refusal: ScriptNetworkRefusal, process: readonly unknown[]): RefusedStep | null {
  if (refusal.step) {
    const stored = process[refusal.step.position - 1] as { type?: unknown } | undefined;
    const type = refusal.step.type || (stored && typeof stored.type === "string" ? stored.type : "");
    return { position: refusal.step.position, type };
  }
  const candidates = networkSteps(process);
  return candidates.length === 1 ? candidates[0]! : null;
}
