import { MAX_STATE_KEYS, validPageState, type PageState } from "../src/pageState";

/**
 * The console's half of the page-state contract, as the dev harness plays it.
 *
 * In production the console keeps the plugin's state in its own address and
 * the frame never sees that address. The harness has no console, so its own
 * address bar stands in for the console's, and these are the rules the
 * console applies to it. Kept free of side effects so the unit suite can run
 * them; `fakeHost.ts` is the only caller.
 */

/**
 * The harness's own switches on the address bar. The console's address has
 * none of these; here they sit beside the page state, so the fake console
 * leaves them alone when it hands state over and when it writes it back.
 */
const HARNESS_PARAMS = new Set(["fixture", "theme", "state", "conflict"]);

/**
 * What the console hands over as `pageState`: its route's query, filtered by
 * the contract's rules one key at a time, the harness switches left out.
 */
export function pageStateFromAddress(search: string): PageState {
  const out: PageState = {};
  for (const [key, value] of new URLSearchParams(search)) {
    if (HARNESS_PARAMS.has(key) || Object.hasOwn(out, key) || Object.keys(out).length >= MAX_STATE_KEYS) continue;
    const single = validPageState({ [key]: value });
    if (single) Object.assign(out, single);
  }
  return out;
}

/**
 * The address the console writes for a state message: the harness switches
 * as they were, then the state, with the fragment kept. Null when the message
 * breaks the contract's rules, which drops it whole.
 */
export function addressForState(search: string, state: unknown): string | null {
  const valid = validPageState(state);
  if (!valid) return null;
  const next = new URLSearchParams();
  for (const [key, value] of new URLSearchParams(search)) {
    if (HARNESS_PARAMS.has(key)) next.append(key, value);
  }
  for (const [key, value] of Object.entries(valid)) next.set(key, value);
  const query = next.toString();
  return query ? `?${query}` : "";
}

/** The console's budget per frame: 60 state messages a minute, the rest ignored. */
export function stateRateLimit(now: () => number = Date.now): () => boolean {
  const sent: number[] = [];
  return () => {
    const t = now();
    while (sent.length && t - sent[0]! >= 60_000) sent.shift();
    if (sent.length >= 60) return false;
    sent.push(t);
    return true;
  };
}
