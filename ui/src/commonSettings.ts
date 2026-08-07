import type { ChainStep } from "./components/ProcessChain.vue";

/**
 * "Common settings": the handful of operators people reach for constantly,
 * surfaced as plain radio choices instead of buried in the operator chain.
 *
 * This is the shape upstream uses, and the reason is worth stating: turning UDP
 * on for every node is a setting, not a pipeline step, even though it is
 * implemented as one. Making someone add a "Quick Setting Operator" and fill in
 * a form to do it is exposing an implementation detail as a task.
 *
 * These map onto real chain steps in both directions, so nothing here is a
 * parallel storage path — the chain remains the single source of truth, and an
 * operator who edits the step directly sees the toggles follow.
 */

export type TriState = "default" | "on" | "off";

export interface CommonSettings {
  /** Drop nodes that carry traffic or expiry notices rather than a server. */
  dropUseless: boolean;
  udp: TriState;
  skipCertVerify: TriState;
  tcpFastOpen: TriState;
  vmessAead: TriState;
}

export function emptyCommonSettings(): CommonSettings {
  return {
    dropUseless: false,
    udp: "default",
    skipCertVerify: "default",
    tcpFastOpen: "default",
    vmessAead: "default",
  };
}

const QUICK = "Quick Setting Operator";
const USELESS = "Useless Filter";

/** Argument keys exactly as the engine spells them. */
const QUICK_KEYS: Record<Exclude<keyof CommonSettings, "dropUseless">, string> = {
  udp: "udp",
  skipCertVerify: "scert",
  tcpFastOpen: "tfo",
  vmessAead: "vmess aead",
};

function triFrom(value: unknown): TriState {
  if (value === undefined || value === null) return "default";
  return value ? "on" : "off";
}

function triTo(state: TriState): boolean | undefined {
  if (state === "default") return undefined;
  return state === "on";
}

/**
 * Read the settings out of a chain.
 *
 * Only steps that are enabled count: a disabled Quick Setting step is not in
 * force, and showing its values as active would misdescribe what the pipeline
 * does.
 */
export function readCommonSettings(steps: readonly ChainStep[]): CommonSettings {
  const settings = emptyCommonSettings();
  for (const step of steps) {
    if (step.disabled) continue;
    if (step.type === USELESS) settings.dropUseless = true;
    if (step.type === QUICK) {
      const args = (step.args ?? {}) as Record<string, unknown>;
      for (const [field, key] of Object.entries(QUICK_KEYS)) {
        settings[field as keyof typeof QUICK_KEYS] = triFrom(args[key]);
      }
    }
  }
  return settings;
}

/**
 * Write the settings back into a chain, preserving everything else.
 *
 * The steps are placed at the FRONT when newly added: filtering out useless
 * entries and normalising protocol switches before any rename or sort runs is
 * what an operator means by these toggles. An existing step is edited where it
 * already sits, because moving someone's step is not this function's business.
 */
export function applyCommonSettings(
  steps: readonly ChainStep[],
  settings: CommonSettings,
): ChainStep[] {
  const next = steps.map((step) => ({ ...step }));

  // ── useless filter: present or absent ────────────────────────────────────
  const uselessAt = next.findIndex((step) => step.type === USELESS && !step.disabled);
  if (settings.dropUseless && uselessAt === -1) {
    next.unshift({ type: USELESS });
  } else if (!settings.dropUseless && uselessAt !== -1) {
    next.splice(uselessAt, 1);
  }

  // ── quick setting: present when any switch is not "default" ──────────────
  const quickArgs: Record<string, unknown> = {};
  for (const [field, key] of Object.entries(QUICK_KEYS)) {
    const value = triTo(settings[field as keyof typeof QUICK_KEYS]);
    if (value !== undefined) quickArgs[key] = value;
  }
  const wanted = Object.keys(quickArgs).length > 0;
  const quickAt = next.findIndex((step) => step.type === QUICK && !step.disabled);

  if (wanted && quickAt === -1) {
    next.unshift({ type: QUICK, args: quickArgs });
  } else if (wanted) {
    // Merge rather than replace: the step may carry keys these toggles do not
    // cover, and dropping them would silently undo hand-edited arguments.
    const existing = (next[quickAt].args ?? {}) as Record<string, unknown>;
    const merged: Record<string, unknown> = { ...existing };
    for (const key of Object.values(QUICK_KEYS)) delete merged[key];
    next[quickAt] = { ...next[quickAt], args: { ...merged, ...quickArgs } };
  } else if (quickAt !== -1) {
    const existing = (next[quickAt].args ?? {}) as Record<string, unknown>;
    const remaining: Record<string, unknown> = { ...existing };
    for (const key of Object.values(QUICK_KEYS)) delete remaining[key];
    // Only the toggles were in that step, so it has nothing left to do.
    if (Object.keys(remaining).length === 0) next.splice(quickAt, 1);
    else next[quickAt] = { ...next[quickAt], args: remaining };
  }

  return next;
}
