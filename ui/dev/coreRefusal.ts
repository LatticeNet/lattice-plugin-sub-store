/**
 * dev/coreRefusal.ts, core's fleet_script_network_unavailable refusal as the
 * harness answers it.
 *
 * lattice-server's substore_script_network.go words the refusal, names the
 * first enabled step that can ask for the network (the engine does not say
 * which one asked), and answers 409; the console's client turns that into a
 * host error whose message is core's sentence with the request id appended,
 * and the bridge relays the message alone. The harness answers the same
 * text, so the UI parses what it will parse against a real core.
 * src/scriptNetwork.test.ts holds coreRefusalMessage to core's own samples
 * (core-types.ts scriptNetworkRefusalSamples), so this stand-in cannot drift
 * from core's wording unnoticed.
 *
 * Never imported by `src/`; the shipped bundle is built from index.html alone.
 */

const CODE = "fleet_script_network_unavailable";

/** The step types whose run can ask for the network (core's substoreNetworkStepTypes). */
const NETWORK_TYPES = new Set(["Script Operator", "Script Filter", "Resolve Domain Operator"]);

/** Go's %q for the ids the harness uses: quotes and backslashes escaped, controls as \xHH, printable Unicode kept. */
export function goQuote(value: string): string {
  let out = '"';
  for (const ch of value) {
    const code = ch.codePointAt(0)!;
    if (ch === '"' || ch === "\\") out += `\\${ch}`;
    else if (ch === "\n") out += "\\n";
    else if (ch === "\t") out += "\\t";
    else if (ch === "\r") out += "\\r";
    else if (code < 0x20 || code === 0x7f) out += `\\x${code.toString(16).padStart(2, "0")}`;
    else out += ch;
  }
  return `${out}"`;
}

/** Core's message for a refused render (newSubstoreScriptNetworkRefusal): `rendered` "" is a draft. */
export function coreRefusalMessage(rendered: string, holder: string, step: { position: number; type: string } | null): string {
  const name = (id: string) => (id === "" ? "the draft" : `record ${goQuote(id)}`);
  let message = `${CODE}: a step asked for the network while ${name(rendered)} rendered, and a chain that reaches fleet nodes runs without one until S3`;
  if (step) {
    message += `; step ${step.position} (${step.type}) of ${name(holder)} is the first step in that render that can ask for it, so remove the network call or the step`;
  } else {
    message += "; remove the network call or the Script or Resolve Domain step that makes it";
  }
  return message;
}

/** The first enabled step that can ask (core's substoreFirstNetworkStep), 1-based, or null. */
export function firstNetworkStep(process: readonly unknown[]): { position: number; type: string } | null {
  for (const [index, raw] of process.entries()) {
    const step = raw as { type?: unknown; disabled?: unknown } | null;
    if (step && typeof step.type === "string" && NETWORK_TYPES.has(step.type) && step.disabled !== true) return { position: index + 1, type: step.type };
  }
  return null;
}

let requests = 0;
/** The host error the console's client throws for core's 409: the message, then the request id. */
export function hostError(message: string): Error {
  requests += 1;
  return new Error(`${message} (request_id: req_dev${String(requests).padStart(4, "0")})`);
}
