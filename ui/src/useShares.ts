/**
 * useShares.ts, the host's share list, read once and shared.
 *
 * Three surfaces print it: the Published column on the record list, the
 * Shares lens, and the count on the lens switch. Each reading it on its own
 * is how the counts across tabs came to disagree with each other. One copy
 * per host, like the record catalogue; every reader sees the same rows and
 * the same "not read yet".
 */
import { computed, ref, type Ref } from "vue";

import { BINDINGS, callMethod, type SubStoreShareRow, type SubStoreSharesResponse } from "./client";
import type { HostContext } from "./host";
import { safeErrorMessage } from "./subStoreModel";

interface ShareStore {
  /** `undefined` until the list has been read, so a column can say so. */
  shares: Ref<SubStoreShareRow[] | undefined>;
  error: Ref<string>;
  loading: Ref<boolean>;
  inFlight: Promise<void> | null;
}

const stores = new WeakMap<object, ShareStore>();

function storeFor(host: HostContext): ShareStore {
  const existing = stores.get(host);
  if (existing) return existing;
  const fresh: ShareStore = { shares: ref(undefined), error: ref(""), loading: ref(false), inFlight: null };
  stores.set(host, fresh);
  return fresh;
}

export function useShares(host: HostContext) {
  const store = storeFor(host);
  const available = computed(() => host.available(BINDINGS.sharesList));

  /** Read the list; a read already in flight is joined, not repeated. */
  function load(): Promise<void> {
    if (store.inFlight) return store.inFlight;
    if (!host.bridge || !available.value) return Promise.resolve();
    const bridge = host.bridge;
    store.loading.value = true;
    store.inFlight = callMethod<SubStoreSharesResponse>(bridge, BINDINGS.sharesList, {})
      .promise.then((response) => {
        store.shares.value = response.shares ?? [];
        store.error.value = "";
      })
      .catch((cause) => {
        store.error.value = safeErrorMessage(cause, "The share list could not be read");
      })
      .finally(() => {
        store.loading.value = false;
        store.inFlight = null;
      });
    return store.inFlight;
  }

  return { shares: store.shares, error: store.error, loading: store.loading, available, load };
}
