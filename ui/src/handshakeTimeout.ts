import { getCurrentInstance, onBeforeUnmount, ref, watch, type Ref } from "vue";

/** How long the shell waits for the host handshake before saying so. */
export const HANDSHAKE_TIMEOUT_MS = 3000;

/**
 * True once the handshake has been missing for HANDSHAKE_TIMEOUT_MS.
 *
 * Opened standalone, the frame has no host: the bridge keeps waiting, every
 * screen shows "Loading…", and nothing ever changes — the operator cannot tell
 * a slow start from a page that will never work. This timeout is what turns
 * that silence into a statement. A handshake that lands late simply resets it,
 * so a slow-but-real host never gets stuck behind the notice.
 */
export function useHandshakeTimeout(
  init: Ref<unknown>,
  timeoutMs: number = HANDSHAKE_TIMEOUT_MS,
): Ref<boolean> {
  const expired = ref(false);
  let timer: ReturnType<typeof setTimeout> | undefined;

  function clear(): void {
    if (timer !== undefined) {
      clearTimeout(timer);
      timer = undefined;
    }
  }

  if (!init.value) {
    timer = setTimeout(() => {
      expired.value = true;
    }, timeoutMs);
  }

  watch(init, (value) => {
    if (value) {
      expired.value = false;
      clear();
    }
  });

  // Guarded: the composable is also exercised bare in tests, where there is no
  // component to unmount and the registration would only warn.
  if (getCurrentInstance()) {
    onBeforeUnmount(clear);
  }

  return expired;
}
