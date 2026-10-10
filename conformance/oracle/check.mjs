#!/usr/bin/env node
// Judge an implementation against the goldens.
//
//   node oracle/check.mjs [--candidate "<command>"] [--targets uri,json]
//                         [--cases <prefix,...>] [--report <file>]
//                         [--conformance <file>] [--expect <file>] [--no-fail]
//                         [--label <text>] [--end-to-end]
//
// The candidate is any command that speaks the oracle protocol on stdin and
// stdout (see run.mjs); it defaults to the oracle itself, which must score
// 100 percent against its own goldens. For every case the candidate parses
// the corpus input; parse conformance is a deep comparison of its nodes with
// goldens/parse/<case>.json, key order ignored. For every case and target
// that has a golden, the candidate produces from the golden nodes with the
// case's options (and _subName from its record_name, lib/corpus.mjs); produce conformance compares the canonical structural form
// of its output with the golden's, and for URI and V2Ray also compares the
// bytes. Allowlist normalisations (allowlist/divergences.yaml) apply before
// a comparison is failed; they never apply to URI and V2Ray, whose bytes must
// match. A without_line_breaking_nodes entry also lets a failing case match
// the golden upstream wrote without the input's line-breaking nodes
// (goldens/produce/<target>/<case>.line-safe.canon.json).
//
// --end-to-end produces from the candidate's own parse output instead of the
// golden nodes, in whatever key order the candidate returned them, and
// judges it against the same produce goldens. A case whose parse failed
// fails every target. This is how an implementation shows that its producers
// do not depend on the order its parser builds a node in; upstream itself
// does not pass it (README, Goldens).
//
// --conformance writes conformance.json, the three numbers design 28
// publishes:
//   {upstream_commit, generated_at, parse:{cases, passed},
//    produce:{<target>:{cases, passed}}, script:{cases, passed, reason},
//    divergences:[ids]}
// Script conformance is not measured until S3, which fixes the named
// community script set; until then its cases and passed are null and reason
// says why, so the shape does not change when the number arrives.
// --expect compares the computed numbers with an existing conformance.json
// (generated_at ignored) and fails on any difference.
// --report writes every failure with the first differing path; --label adds
// a provenance line to it.
//
// Exit status: 0 when everything passes, 1 when a comparison fails (unless
// --no-fail), 2 when --expect does not match, 3 on a harness error.
import fs from 'node:fs';
import path from 'node:path';
import { LINE_SAFE_SUFFIX, applicable, comparesWithoutLineBreaks, loadAllowlist, normalise } from './lib/allowlist.mjs';
import { canonical, sortKeys, stableJSON } from './lib/canonical.mjs';
import { Runner } from './lib/client.mjs';
import { filterCases, listCases, produceInput } from './lib/corpus.mjs';
import { ALLOWLIST_FILE, GOLDENS_DIR, ORACLE_DIR, ROOT, readPin } from './lib/paths.mjs';
import { TARGETS, findTarget } from './lib/targets.mjs';

function arg(name, fallback) {
    const i = process.argv.indexOf(name);
    return i >= 0 ? process.argv[i + 1] : fallback;
}
const flag = (name) => process.argv.includes(name);

const candidate = arg('--candidate', `node ${JSON.stringify(path.join(ORACLE_DIR, 'run.mjs'))}`);
const goldensDir = path.resolve(arg('--goldens', GOLDENS_DIR));
const allowlistFile = path.resolve(arg('--allowlist', ALLOWLIST_FILE));
const reportFile = arg('--report', null);
const label = arg('--label', null);
const conformanceFile = arg('--conformance', null);
const expectFile = arg('--expect', null);
const quiet = flag('--quiet');
const endToEnd = flag('--end-to-end');
const prefixes = (arg('--cases', '') || '').split(',').filter(Boolean);
const targetIds = (arg('--targets', '') || '').split(',').filter(Boolean);
const targets = targetIds.length ? targetIds.map((t) => findTarget(t) || fail(`unknown target ${t}`)) : TARGETS;

const SCRIPT_NOT_MEASURED = 'not measured until S3: design 28 fixes the named community script set when the script slice starts';

function fail(msg) {
    process.stderr.write(`check: ${msg}\n`);
    process.exit(3);
}

// conformance.json numbers mean "produced from the golden nodes".
if (endToEnd && (conformanceFile || expectFile)) fail('--end-to-end does not write or compare conformance.json; use --report');

// firstDiff returns the first path where a and b differ, with both values.
function firstDiff(a, b, p = '$') {
    if (JSON.stringify(a) === JSON.stringify(b)) return null;
    if (Array.isArray(a) && Array.isArray(b)) {
        for (let i = 0; i < Math.max(a.length, b.length); i++) {
            if (i >= a.length || i >= b.length) return { path: `${p}[${i}]`, golden: a[i] ?? '<missing>', candidate: b[i] ?? '<missing>' };
            const d = firstDiff(a[i], b[i], `${p}[${i}]`);
            if (d) return d;
        }
    }
    if (a && b && typeof a === 'object' && typeof b === 'object' && !Array.isArray(a) && !Array.isArray(b)) {
        const keys = [...new Set([...Object.keys(a), ...Object.keys(b)])].sort();
        for (const k of keys) {
            if (!(k in a) || !(k in b)) return { path: `${p}.${k}`, golden: k in a ? a[k] : '<missing>', candidate: k in b ? b[k] : '<missing>' };
            const d = firstDiff(a[k], b[k], `${p}.${k}`);
            if (d) return d;
        }
    }
    return { path: p, golden: a, candidate: b };
}

function clip(v) {
    const s = typeof v === 'string' ? v : JSON.stringify(v);
    return s.length > 240 ? `${s.slice(0, 240)}...` : s;
}

// compare runs the strict comparison and, when it fails, retries with the
// applicable allowlist normalisations: against the golden, and then, when an
// entry compares without line-breaking nodes and the case has that golden
// (alternate), against it. It returns {pass, diff, allowed}; a failure
// reports where the candidate differs from the golden itself.
function compare(golden, cand, entries, used, alternate = null) {
    const g = sortKeys(golden);
    const c = sortKeys(cand);
    let d = firstDiff(g, c);
    if (!d) return { pass: true };
    if (entries.length) {
        const nc = sortKeys(normalise(c, entries));
        const nd = firstDiff(sortKeys(normalise(g, entries)), nc);
        if (!nd) {
            // Only entries with a value step can have made this pass.
            const allowed = entries.filter((e) => e.normalise.some((step) => !step.lineSafe)).map((e) => e.id);
            for (const id of allowed) used.add(id);
            return { pass: true, allowed };
        }
        d = nd;
        if (alternate !== null && !firstDiff(sortKeys(normalise(alternate, entries)), nc)) {
            for (const e of entries) used.add(e.id);
            return { pass: true, allowed: entries.map((e) => e.id) };
        }
    }
    return { pass: false, diff: { path: d.path, golden: clip(d.golden), candidate: clip(d.candidate) } };
}

const readJSON = (f) => JSON.parse(fs.readFileSync(f, 'utf8'));

let allow;
try {
    allow = loadAllowlist(allowlistFile);
} catch (e) {
    fail(e.message);
}
const pin = readPin();
const cases = filterCases(listCases(), prefixes);
const used = new Set();
const result = {
    parse: { cases: 0, passed: 0, failures: [] },
    produce: Object.fromEntries(targets.map((t) => [t.id, { cases: 0, passed: 0, failures: [] }])),
};

const runner = new Runner(candidate, { cwd: ROOT });
let candidateInfo = null;
try {
    const v = await runner.request({ op: 'version' });
    if (v.ok) candidateInfo = { implementation: v.implementation, commit: v.commit, version: v.version };

    for (const c of cases) {
        const parseFile = path.join(goldensDir, 'parse', `${c.id}.json`);
        if (!fs.existsSync(parseFile)) fail(`no parse golden for ${c.id}; run "node oracle/regen.mjs"`);
        const goldenNodes = readJSON(parseFile);

        result.parse.cases++;
        const parsed = await runner.request({ op: 'parse', input: c.input });
        let r;
        if (goldenNodes && goldenNodes.error === true) r = { pass: !parsed.ok, diff: { path: '$', golden: 'error', candidate: 'ok' } };
        else if (!parsed.ok) r = { pass: false, diff: { path: '$', golden: 'nodes', candidate: `error: ${clip(parsed.error)}` } };
        else r = compare(goldenNodes, parsed.nodes, applicable(allow, 'parse', null, c.id), used);
        if (r.pass) result.parse.passed++;
        else result.parse.failures.push({ case: c.id, ...r.diff });

        if (!Array.isArray(goldenNodes)) continue;
        const produceNodes = endToEnd ? (r.pass ? parsed.nodes : null) : goldenNodes;
        for (const t of targets) {
            const canonFile = path.join(goldensDir, 'produce', t.id, `${c.id}.canon.json`);
            if (!fs.existsSync(canonFile)) continue;
            const goldenCanon = readJSON(canonFile);
            const bucket = result.produce[t.id];
            bucket.cases++;
            if (produceNodes === null) {
                bucket.failures.push({ case: c.id, path: '$', golden: 'output', candidate: 'no end-to-end input: the parse did not match' });
                continue;
            }
            const res = await runner.request({ op: 'produce', target: t.id, nodes: produceInput(c, produceNodes), options: c.options });
            let pr;
            if (goldenCanon && goldenCanon.error === true && Object.keys(goldenCanon).length === 1) {
                pr = res.ok ? { pass: false, diff: { path: '$', golden: 'error', candidate: 'output' } } : { pass: true };
            } else if (!res.ok) {
                pr = { pass: false, diff: { path: '$', golden: 'output', candidate: `error: ${clip(res.error)}` } };
            } else {
                const entries = applicable(allow, 'produce', t.id, c.id);
                const lineSafeFile = path.join(goldensDir, 'produce', t.id, `${c.id}${LINE_SAFE_SUFFIX}`);
                const alternate = comparesWithoutLineBreaks(entries) && fs.existsSync(lineSafeFile) ? readJSON(lineSafeFile) : null;
                pr = compare(goldenCanon, canonical(t.form, res.output), entries, used, alternate);
                if (pr.pass && t.bytes) {
                    const goldenBytes = fs.readFileSync(path.join(goldensDir, 'produce', t.id, `${c.id}.${t.ext}`), 'utf8');
                    if (goldenBytes !== res.output) {
                        let i = 0;
                        while (i < goldenBytes.length && goldenBytes[i] === res.output[i]) i++;
                        pr = { pass: false, diff: { path: `bytes@${i}`, golden: clip(goldenBytes.slice(Math.max(0, i - 40), i + 80)), candidate: clip(res.output.slice(Math.max(0, i - 40), i + 80)) } };
                    }
                }
            }
            if (pr.pass) bucket.passed++;
            else bucket.failures.push({ case: c.id, ...pr.diff });
        }
    }
} catch (e) {
    await runner.close().catch(() => {});
    fail(e.stack || e.message);
}
await runner.close();

const conformance = {
    upstream_commit: pin.commit,
    generated_at: new Date().toISOString().replace(/\.\d{3}Z$/, 'Z'),
    parse: { cases: result.parse.cases, passed: result.parse.passed },
    produce: Object.fromEntries(Object.entries(result.produce).map(([k, v]) => [k, { cases: v.cases, passed: v.passed }])),
    script: { cases: null, passed: null, reason: SCRIPT_NOT_MEASURED },
    divergences: [...used].sort(),
};

const failures = result.parse.failures.length + Object.values(result.produce).reduce((n, v) => n + v.failures.length, 0);

if (!quiet) {
    const pct = (p, n) => (n === 0 ? '   n/a' : `${((100 * p) / n).toFixed(1).padStart(5)}%`);
    const lines = [`candidate: ${candidate}${endToEnd ? ' (end to end)' : ''}`, `  parse        ${String(result.parse.passed).padStart(5)} / ${String(result.parse.cases).padEnd(5)} ${pct(result.parse.passed, result.parse.cases)}`];
    for (const [k, v] of Object.entries(result.produce)) lines.push(`  ${k.padEnd(12)} ${String(v.passed).padStart(5)} / ${String(v.cases).padEnd(5)} ${pct(v.passed, v.cases)}`);
    lines.push(`  ${'script'.padEnd(12)} ${SCRIPT_NOT_MEASURED}`);
    if (used.size) lines.push(`  allowlisted: ${[...used].sort().join(', ')}`);
    process.stderr.write(`${lines.join('\n')}\n`);
    const shown = [...result.parse.failures.map((f) => ['parse', f]), ...Object.entries(result.produce).flatMap(([k, v]) => v.failures.map((f) => [k, f]))].slice(0, 20);
    for (const [where, f] of shown) process.stderr.write(`  FAIL ${where} ${f.case} at ${f.path}\n    golden:    ${f.golden}\n    candidate: ${f.candidate}\n`);
    if (failures > shown.length) process.stderr.write(`  ... and ${failures - shown.length} more\n`);
}

if (reportFile) {
    const report = { label: label ?? undefined, candidate: candidateInfo ?? { command: candidate }, end_to_end: endToEnd || undefined, upstream_commit: pin.commit, summary: { parse: conformance.parse, produce: conformance.produce, divergences: conformance.divergences }, failures: { parse: result.parse.failures, produce: Object.fromEntries(Object.entries(result.produce).map(([k, v]) => [k, v.failures])) } };
    fs.writeFileSync(reportFile, `${JSON.stringify(report, null, 2)}\n`);
}
if (conformanceFile) fs.writeFileSync(conformanceFile, `${JSON.stringify(conformance, null, 2)}\n`);

if (expectFile) {
    const expected = readJSON(expectFile);
    const strip = (o) => stableJSON({ ...o, generated_at: undefined });
    if (strip(expected) !== strip(conformance)) {
        process.stderr.write(`check: ${expectFile} does not match this run (generated_at ignored); regenerate it with --conformance\n`);
        process.exit(2);
    }
}
process.exit(failures > 0 && !flag('--no-fail') ? 1 : 0);
