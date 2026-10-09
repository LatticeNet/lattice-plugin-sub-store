// The value the oracle's Math.random returns.
//
// On the parse and produce paths upstream calls Math.random only to pick a
// port from a port list: while parsing a Hysteria2 link whose authority holds
// a list, and while producing a node that has `ports` and no `port` (no parse
// golden has such a node). A seeded pseudo-random generator makes the golden
// reproducible but leaves a value an independent implementation could only
// match by replaying the generator and upstream's exact sequence of draws.
// A constant draw of 0 is a value upstream can legitimately see, and every
// upstream choice then collapses to its first option (the first entry of a
// list, the lower bound of a range), which a deterministic implementation can
// make on purpose.
//
// ORACLE_RANDOM_DRAW overrides the constant. regen.mjs uses it to find every
// case whose parse depends on the draw (see the oracle_dependency meta field).
export const DRAW_ENV = 'ORACLE_RANDOM_DRAW';

export function fixedDraw(env = process.env) {
    const raw = env[DRAW_ENV];
    const value = raw === undefined || raw === '' ? 0 : Number(raw);
    if (!Number.isFinite(value) || value < 0 || value >= 1) {
        throw new Error(`${DRAW_ENV} must be a number in [0, 1), got ${raw}`);
    }
    return () => value;
}
