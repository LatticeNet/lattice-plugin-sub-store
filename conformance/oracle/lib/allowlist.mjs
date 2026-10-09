// The closed divergence allowlist (allowlist/divergences.yaml): loading,
// validation and the normalisations the checker applies.
import fs from 'node:fs';
import YAML from 'yaml';
import { findTarget } from './targets.mjs';

export const MAX_ENTRIES = 4;
const STAGES = new Set(['parse', 'produce', 'script']);
// A pending entry names the design 28 slice that lands it.
const SLICE = /^S[1-6]$/;
const TEXT_FIELDS = ['upstream', 'chosen', 'reason'];

// Keys a strip_keys pattern may never match, at any depth. Stripping one of
// these would hide a whole node's identity or a whole document rather than
// one divergent field: the node identity keys, the top-level containers of
// the YAML and JSON targets, and the structural keys of the canonical forms
// (lib/canonical.mjs).
export const PROTECTED_KEYS = [
    'type', 'name', 'server', 'port',
    'proxies', 'outbounds', 'endpoints',
    'scheme', 'userinfo', 'host', 'path', 'query', 'fragment', 'body', 'main',
    'address', 'args', 'params', 'section', 'key', 'value', 'header', 'comment', 'raw', 'unparsed',
];

export function loadAllowlist(file) {
    const doc = YAML.parse(fs.readFileSync(file, 'utf8')) ?? {};
    const entries = doc.divergences;
    if (!Array.isArray(entries)) throw new Error(`${file}: divergences must be a list`);
    if (entries.length > MAX_ENTRIES) {
        throw new Error(`${file}: ${entries.length} entries; the allowlist is closed at ${MAX_ENTRIES}`);
    }
    const ids = new Set();
    for (const [i, e] of entries.entries()) {
        const where = `${file}: entry ${i + 1}`;
        if (!e || typeof e !== 'object') throw new Error(`${where} is not a mapping`);
        if (typeof e.id !== 'string' || !/^[a-z0-9]+(-[a-z0-9]+)*$/.test(e.id)) throw new Error(`${where}: id must be kebab-case`);
        if (ids.has(e.id)) throw new Error(`${where}: duplicate id ${e.id}`);
        ids.add(e.id);
        if (!STAGES.has(e.stage)) throw new Error(`${where}: stage must be parse, produce or script`);
        if (e.pending !== undefined && (typeof e.pending !== 'string' || !SLICE.test(e.pending))) throw new Error(`${where}: pending must name the slice that lands the entry, S1 to S6`);
        for (const f of TEXT_FIELDS) {
            if (typeof e[f] !== 'string' || e[f].trim() === '') throw new Error(`${where}: ${f} is required`);
        }
        e.targets = e.targets ?? ['*'];
        e.cases = e.cases ?? ['*'];
        if (!Array.isArray(e.targets) || !Array.isArray(e.cases)) throw new Error(`${where}: targets and cases must be lists`);
        for (const t of e.targets) {
            if (t === '*') continue;
            const target = findTarget(t);
            if (!target) throw new Error(`${where}: unknown target ${t}`);
            if (target.bytes) throw new Error(`${where}: ${target.id} is compared byte for byte, so no entry can apply to it`);
        }
        for (const p of e.cases) {
            if (typeof p !== 'string' || p === '') throw new Error(`${where}: a case selector is "*" or a non-empty id prefix`);
        }
        // A pending entry holds its slot in the closed list and may leave its
        // normalisation to the slice that lands it; applicable() never
        // returns it.
        if (e.pending !== undefined && e.normalise === undefined) continue;
        if (!Array.isArray(e.normalise) || e.normalise.length === 0) throw new Error(`${where}: normalise must list at least one step`);
        for (const step of e.normalise) {
            const keys = Object.keys(step || {});
            if (keys.length !== 1) throw new Error(`${where}: each normalise step has exactly one kind`);
            if (keys[0] === 'strip_keys') {
                if (typeof step.strip_keys !== 'string' || step.strip_keys === '') throw new Error(`${where}: strip_keys needs a pattern`);
                step.re = new RegExp(step.strip_keys);
                if (step.re.test('')) throw new Error(`${where}: strip_keys ${step.strip_keys} matches the empty key, so it matches every key`);
                const hit = PROTECTED_KEYS.find((k) => step.re.test(k));
                if (hit) throw new Error(`${where}: strip_keys ${step.strip_keys} matches the protected key ${hit}`);
            } else if (keys[0] === 'drop_entries') {
                const d = step.drop_entries;
                if (!d || typeof d.field !== 'string' || d.field === '' || !Array.isArray(d.equals) || d.equals.length === 0) {
                    throw new Error(`${where}: drop_entries needs field and equals`);
                }
            } else {
                throw new Error(`${where}: unknown normalise kind ${keys[0]}`);
            }
        }
    }
    return entries;
}

// applicable returns the entries that apply to one comparison. No entry
// applies to a byte-exact target: a canonical difference there is a byte
// difference, which the checker fails regardless. A pending entry applies to
// nothing until its slice removes the pending field.
export function applicable(entries, stage, targetId, caseId) {
    if (stage === 'produce' && findTarget(targetId)?.bytes) return [];
    return entries.filter(
        (e) =>
            e.pending === undefined &&
            e.stage === stage &&
            (stage !== 'produce' || e.targets.includes('*') || e.targets.some((t) => findTarget(t)?.id === targetId)) &&
            (e.cases.includes('*') || e.cases.some((p) => caseId.startsWith(p))),
    );
}

export function normalise(value, entries) {
    let v = value;
    for (const e of entries) {
        for (const step of e.normalise) v = applyStep(v, step, null);
    }
    return v;
}

function applyStep(v, step, parentKey) {
    if (Array.isArray(v)) {
        let arr = v;
        if (step.drop_entries) {
            const { field, equals } = step.drop_entries;
            arr = arr.filter((x) => !(x && typeof x === 'object' && !Array.isArray(x) && equals.includes(x[field])));
        }
        if (step.re && parentKey === 'query') {
            arr = arr.filter((pair) => !(Array.isArray(pair) && typeof pair[0] === 'string' && step.re.test(pair[0])));
        }
        return arr.map((x) => applyStep(x, step, null));
    }
    if (v && typeof v === 'object') {
        const out = {};
        for (const [k, x] of Object.entries(v)) {
            if (step.re && step.re.test(k)) continue;
            out[k] = applyStep(x, step, k);
        }
        return out;
    }
    return v;
}
