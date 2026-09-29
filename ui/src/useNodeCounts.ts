/**
 * useNodeCounts.ts, the "in → out" of every record, counted once per session
 * and shared by every layer that prints it.
 *
 * The overview's chips, the sources and combinations tables, the side panel
 * and the record page all show the same two numbers. Each counting for itself
 * would preview a provider link four times, and a preview of a provider link
 * fetches the provider. One queue per host, like the record catalogue and the
 * share list: the first reader asks, every other reader sees the answer land.
 */
import { BINDINGS, callMethod, type SubscriptionPreviewResponse } from "./client";
import type { HostContext } from "./host";
import { createNodeCountQueue, type NodeCountQueue } from "./nodeCounts";
import { safeErrorMessage } from "./subStoreModel";

const queues = new WeakMap<object, NodeCountQueue>();

export function useNodeCounts(host: HostContext): NodeCountQueue {
  const existing = queues.get(host);
  if (existing) return existing;
  const queue = createNodeCountQueue((id) => {
    if (!host.bridge) return Promise.reject(new Error("The console is not connected"));
    return callMethod<SubscriptionPreviewResponse>(host.bridge, BINDINGS.subPreview, { subscription_id: id })
      .promise.catch((cause) => {
        // The reason is shown in a title, so it goes through the same redaction
        // every other error does: a fetch failure quotes the link.
        throw new Error(safeErrorMessage(cause, "Preview failed"));
      });
  });
  queues.set(host, queue);
  return queue;
}
