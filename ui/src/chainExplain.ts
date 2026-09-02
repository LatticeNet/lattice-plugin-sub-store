/**
 * chainExplain.ts, the per-step account of a chain.
 *
 * The engine answers "what does the chain produce" as one node set, and it
 * accepts a partial run stopped after any step. Running it once per enabled
 * step therefore yields what each operation did, which is what the official
 * compare table shows and what someone tuning a twelve-step chain is reading
 * for. This module turns those counts into lines; the screen owns the calls.
 */
import type { ChainStep } from "./components/ProcessChain.vue";

export interface StepDelta {
  /** Index in the chain, counting disabled steps, so it matches the editor. */
  index: number;
  label: string;
  /** Nodes after this step ran. */
  after: number;
  /** Nodes before it ran: the source count for the first enabled step. */
  before: number;
}

/** The chain positions that run, in order: disabled steps are skipped. */
export function enabledStepIndexes(steps: readonly ChainStep[]): number[] {
  return steps.map((step, index) => (step.disabled ? -1 : index)).filter((index) => index >= 0);
}

export function stepLabelOf(step: ChainStep, index: number): string {
  const name = (step.customName ?? "").trim() || step.type;
  return `${index + 1}. ${name}`;
}

/**
 * Fold the counts observed after each enabled step into deltas. `counts`
 * is aligned with `enabledStepIndexes(steps)`; a run that stopped early
 * (an error mid-chain) yields deltas only for the steps that answered.
 */
export function stepDeltas(steps: readonly ChainStep[], sourceCount: number, counts: readonly number[]): StepDelta[] {
  const indexes = enabledStepIndexes(steps);
  const out: StepDelta[] = [];
  let before = sourceCount;
  for (let i = 0; i < counts.length && i < indexes.length; i += 1) {
    const index = indexes[i];
    out.push({ index, label: stepLabelOf(steps[index], index), before, after: counts[i] });
    before = counts[i];
  }
  return out;
}

/** One sentence per step, the way the strip prints it. */
export function describeDelta(delta: StepDelta): string {
  if (delta.after === delta.before) return `${delta.label}: ${delta.after} nodes, none removed`;
  if (delta.after < delta.before) return `${delta.label}: kept ${delta.after} of ${delta.before}`;
  return `${delta.label}: ${delta.before} became ${delta.after}`;
}
