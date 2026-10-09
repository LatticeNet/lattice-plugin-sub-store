/**
 * The age on the proof line, "observed 13s ago", as the console prints it
 * (design 23, 3.1). The absolute time goes in a title, for the operator who
 * needs to match it against a log.
 *
 * The age ticks, or "13s ago" would be a lie a minute later. The clock only
 * re-renders a label: it reads nothing, and it wakes only when the label
 * would change: each second for the first minute, then each minute, then
 * each hour.
 *
 * The same file as lattice-plugin-vpn-core/ui/src/observedAge.ts.
 */
import { computed, getCurrentScope, onScopeDispose, ref, watch, type ComputedRef } from "vue";

import { formatDateTime, formatUnit, t } from "./i18n";

/**
 * "43s", "2m", "3h", "2d": the console's formatAge (lib/format.ts), with the
 * units the active locale writes narrow (43秒, 43 с).
 */
export function formatAge(ms: number): string {
  const s = Math.max(0, Math.floor(ms / 1000));
  if (s < 60) return formatUnit(s, "second");
  const m = Math.floor(s / 60);
  if (m < 60) return formatUnit(m, "minute");
  const h = Math.floor(m / 60);
  if (h < 48) return formatUnit(h, "hour");
  return formatUnit(Math.floor(h / 24), "day");
}

/** Milliseconds until `formatAge` of an age that is `ms` old next changes. */
export function untilNextAge(ms: number): number {
  const age = Math.max(0, ms);
  const unit = age < 60_000 ? 1000 : age < 3_600_000 ? 60_000 : 3_600_000;
  return unit - (age % unit);
}

/** The absolute time for the title: "observed Sep 30, 2026, 12:12:16". */
export function observedTitle(at: number): string {
  return t.common.observedAt(formatDateTime(at));
}

export interface ObservedAge {
  /** "13s", or "" before the first read. */
  age: ComputedRef<string>;
  /** The absolute time for a title attribute, or "". */
  title: ComputedRef<string>;
}

export function useObservedAge(at: () => number | undefined): ObservedAge {
  const now = ref(Date.now());
  let timer: ReturnType<typeof setTimeout> | undefined;
  const schedule = () => {
    clearTimeout(timer);
    const value = at();
    if (value === undefined) return;
    timer = setTimeout(() => {
      now.value = Date.now();
      schedule();
    }, untilNextAge(Date.now() - value));
  };
  watch(at, () => {
    now.value = Date.now();
    schedule();
  }, { immediate: true });
  if (getCurrentScope()) onScopeDispose(() => clearTimeout(timer));
  return {
    age: computed(() => {
      const value = at();
      return value === undefined ? "" : formatAge(now.value - value);
    }),
    title: computed(() => {
      const value = at();
      return value === undefined ? "" : observedTitle(value);
    }),
  };
}
