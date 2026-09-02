/**
 * chainExplain.ts, the per-operation account of a chain.
 *
 * The engine answers "what does the chain produce" as one node set, and it
 * accepts a partial run stopped after any operation. Running it once per
 * enabled operation therefore yields what each one did, which is what the
 * official compare table shows and what someone tuning a twelve-operation
 * chain is reading for. This module turns those runs into lines, and into
 * the answer "which operation dropped this node"; the caller owns the calls,
 * because the editor previews a draft and the list previews a stored record.
 */
import type { SubscriptionPreviewNode, SubscriptionPreviewResponse } from "./client";
import type { ChainStep } from "./components/ProcessChain.vue";
import { schemaFor } from "./operatorSchema";

export interface StepDelta {
  /** Index in the chain, counting disabled operations, so it matches the editor. */
  index: number;
  label: string;
  /** Nodes after this operation ran. */
  after: number;
  /** Nodes before it ran: the source count for the first enabled operation. */
  before: number;
}

/**
 * The operators the engine receives for a run cut after `upTo` (the whole
 * chain when omitted): disabled operations and the response stage are left
 * out, since the node preview never runs either. The index counts positions
 * in the FULL chain, the ones the editor and the list show.
 */
export function cutChain(steps: readonly ChainStep[], upTo?: number): ChainStep[] {
  const chain = typeof upTo === "number" ? steps.slice(0, upTo + 1) : [...steps];
  return chain.filter((step) => step && !step.disabled && step.type !== "Response Transformer");
}

/** The chain positions that run, in order: disabled operations are skipped. */
export function enabledStepIndexes(steps: readonly ChainStep[]): number[] {
  return steps.map((step, index) => (step.disabled ? -1 : index)).filter((index) => index >= 0);
}

/** The operator's own label, or the catalogue's wording for the type. */
export function stepLabelOf(step: ChainStep, index: number): string {
  const name = (step.customName ?? "").trim() || schemaFor(step.type)?.label || step.type;
  return `${index + 1}. ${name}`;
}

/**
 * Fold the counts observed after each enabled operation into deltas.
 * `counts` is aligned with `enabledStepIndexes(steps)`; a run that stopped
 * early (an error mid-chain) yields deltas only for the operations that
 * answered.
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

/** One sentence per operation, the way the strip prints it. */
export function describeDelta(delta: StepDelta): string {
  if (delta.after === delta.before) return `${delta.label}: ${delta.after} nodes, none removed`;
  if (delta.after < delta.before) return `${delta.label}: kept ${delta.after} of ${delta.before}`;
  return `${delta.label}: ${delta.before} became ${delta.after}`;
}

/**
 * What identifies a node across runs. The backend pairs result nodes back to
 * source nodes by endpoint, so the endpoint is the key when there is one; a
 * node the preview could not give an endpoint falls back to the name it had
 * before the chain touched it.
 */
export function nodeKey(node: SubscriptionPreviewNode): string {
  if (node.server) return node.port ? `${node.server}:${node.port}` : node.server;
  return node.was ?? node.name;
}

export interface StepRun {
  index: number;
  label: string;
  result: SubscriptionPreviewResponse;
}

export interface ChainExplanation {
  deltas: StepDelta[];
  /** Node key to the label of the operation that first removed it. */
  droppedBy: Map<string, string>;
  /** The last run that answered: the whole chain when every operation did. */
  final: SubscriptionPreviewResponse | null;
  /** True when `final` covers every enabled operation. */
  complete: boolean;
}

/**
 * Run the chain once per enabled operation, stopping after each, and fold
 * the answers. `run(index)` previews the chain cut after that position; the
 * last cut is the whole chain, so no extra run is needed for the final
 * result. A run that throws ends the explanation where it stood; the caller
 * reports the error, this reports what was learned before it.
 */
export async function explainChain(
  steps: readonly ChainStep[],
  run: (upTo: number) => Promise<SubscriptionPreviewResponse>,
  onRun?: (run: StepRun) => void,
): Promise<ChainExplanation> {
  const indexes = enabledStepIndexes(steps);
  const runs: StepRun[] = [];
  for (const index of indexes) {
    let result: SubscriptionPreviewResponse;
    try {
      result = await run(index);
    } catch {
      break;
    }
    const entry = { index, label: stepLabelOf(steps[index], index), result };
    runs.push(entry);
    onRun?.(entry);
  }
  return foldRuns(steps, runs, runs.length === indexes.length);
}

/** The pure half of explainChain, for callers that already hold the runs. */
export function foldRuns(steps: readonly ChainStep[], runs: readonly StepRun[], complete: boolean): ChainExplanation {
  const first = runs[0]?.result;
  const source = first ? first.source_node_count ?? first.node_count : 0;
  const deltas = stepDeltas(steps, source, runs.map((entry) => entry.result.node_count));
  const droppedBy = new Map<string, string>();
  for (const entry of runs) {
    for (const node of entry.result.dropped ?? []) {
      const key = nodeKey(node);
      if (!droppedBy.has(key)) droppedBy.set(key, entry.label);
    }
  }
  return { deltas, droppedBy, final: runs.length ? runs[runs.length - 1].result : null, complete };
}
