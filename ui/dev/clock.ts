/**
 * `?clock=<ISO time>` starts the harness's clock at that moment.
 *
 * The fixtures place every fetch and every expiry relative to now, so "six
 * days left" stays six days on any day. What a fixed start adds is the rest:
 * an absolute date in a title, the month a far expiry falls in, and two runs
 * of the e2e suite drawing the same page. The clock still ticks from the
 * start, so "observed 3s ago" keeps moving the way it does in production.
 *
 * This module is imported first, before the fixtures are built, so they are
 * built on the same clock the screens read. Never imported by `src/`.
 */
const asked = new URLSearchParams(window.location.search).get("clock");
const start = asked ? Date.parse(asked) : Number.NaN;

if (Number.isFinite(start)) {
  const RealDate = Date;
  const offset = start - RealDate.now();
  class HarnessDate extends RealDate {
    constructor(...args: ConstructorParameters<DateConstructor> | []) {
      if (args.length === 0) super(RealDate.now() + offset);
      else super(...(args as ConstructorParameters<DateConstructor>));
    }
    static override now(): number {
      return RealDate.now() + offset;
    }
  }
  globalThis.Date = HarnessDate as DateConstructor;
}

export {};
