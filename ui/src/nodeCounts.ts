/**
 * nodeCounts.ts, the NODES column's "in → out" for each record.
 *
 * The server keeps no count from the last preview or fetch: `list` reports
 * fetch bookkeeping and how many operations a record has, and a preview's
 * node_count lives only in that reply. So the list computes the column itself,
 * lazily: once per record per session, through the same read-scoped preview
 * binding the row's eye uses, two calls in flight at a time, and never in the
 * way of rendering the rows, which print "?" until their count lands.
 *
 * A preview of a provider link fetches the provider, exactly what the eye
 * click does; a failure is remembered with its reason rather than retried on
 * every render, and a refresh or a save forgets the record so the next render
 * counts it again.
 */
import { reactive } from "vue";

import { formatRelativeTime } from "./rowStatus";

export type NodeCountState =
  | { status: "queued" }
  | { status: "running" }
  | { status: "ready"; source: number; result: number; at: number }
  | { status: "failed"; reason: string; at: number };

export interface NodeCountReply {
  source_node_count?: number;
  node_count: number;
}

export interface NodeCountQueue {
  stateOf(id: string): NodeCountState | undefined;
  /** Count every id not yet known, in the order given. Known ids are skipped. */
  request(ids: readonly string[]): void;
  /** Adopt a count another run already produced, so it is not fetched twice. */
  record(id: string, reply: NodeCountReply): void;
  /** Drop what is known about a record: its node set may have changed. */
  forget(id: string): void;
}

export function createNodeCountQueue(
  run: (id: string) => Promise<NodeCountReply>,
  options: { concurrency?: number; now?: () => number } = {},
): NodeCountQueue {
  const concurrency = Math.max(1, options.concurrency ?? 2);
  const now = options.now ?? Date.now;
  // A reactive Map so a cell re-renders when its own entry moves, and nothing
  // else does: a replaced Map would re-render every row for one count.
  const states = reactive(new Map<string, NodeCountState>());
  const waiting: string[] = [];
  let active = 0;

  async function runOne(id: string): Promise<void> {
    states.set(id, { status: "running" });
    try {
      const reply = await run(id);
      // Forgotten while in flight: the record changed under the count, so the
      // answer describes a node set that no longer exists.
      if (states.get(id)?.status !== "running") return;
      states.set(id, ready(reply, now()));
    } catch (cause) {
      if (states.get(id)?.status !== "running") return;
      const reason = cause instanceof Error && cause.message ? cause.message : "Preview failed";
      states.set(id, { status: "failed", reason, at: now() });
    } finally {
      active -= 1;
      pump();
    }
  }

  function pump(): void {
    while (active < concurrency && waiting.length) {
      const id = waiting.shift()!;
      if (states.get(id)?.status !== "queued") continue;
      active += 1;
      void runOne(id);
    }
  }

  return {
    stateOf: (id) => states.get(id),
    request(ids) {
      for (const id of ids) {
        if (states.has(id)) continue;
        states.set(id, { status: "queued" });
        waiting.push(id);
      }
      pump();
    },
    record(id, reply) {
      states.set(id, ready(reply, now()));
    },
    forget(id) {
      states.delete(id);
    },
  };
}

function ready(reply: NodeCountReply, at: number): NodeCountState {
  const result = reply.node_count;
  return { status: "ready", source: reply.source_node_count ?? result, result, at };
}

/** The cell text: "48 → 31", "…" while counting, "?" until something answers. */
export function nodeCountLabel(state: NodeCountState | undefined): string {
  if (!state) return "?";
  if (state.status === "ready") return `${state.source} → ${state.result}`;
  if (state.status === "failed") return "?";
  return "…";
}

/** Which run the cell's number came from and when, or why there is none. */
export function nodeCountTitle(state: NodeCountState | undefined, canPreview: boolean, now: number = Date.now()): string {
  if (!canPreview) return "This session cannot run a preview, so the node count is unknown.";
  if (!state) return "Not counted yet.";
  if (state.status === "queued" || state.status === "running") return "Counting: a preview is running for this record.";
  const when = formatRelativeTime(new Date(state.at).toISOString(), now) || "just now";
  if (state.status === "failed") return `The preview run ${when} failed: ${state.reason}`;
  return `${state.source} in, ${state.result} out, from a preview run ${when}.`;
}
