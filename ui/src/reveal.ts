import { getCurrentScope, onScopeDispose, ref, type Ref } from "vue";

import { REVEAL_MS } from "./urlMask";

export interface Reveal {
  on: Ref<boolean>;
  show: () => void;
  hide: () => void;
  toggle: () => void;
}

/**
 * A reveal that ends on its own. A masked link is shown for a minute after
 * the click and then masks itself again, so a screen left open does not
 * stay a credential dump; any new reveal restarts the minute.
 */
export function useReveal(ms: number = REVEAL_MS): Reveal {
  const on = ref(false);
  let timer: ReturnType<typeof setTimeout> | undefined;
  function hide(): void {
    on.value = false;
    if (timer !== undefined) clearTimeout(timer);
    timer = undefined;
  }
  function show(): void {
    hide();
    on.value = true;
    timer = setTimeout(hide, ms);
  }
  function toggle(): void {
    if (on.value) hide();
    else show();
  }
  if (getCurrentScope()) onScopeDispose(hide);
  return { on, show, hide, toggle };
}
