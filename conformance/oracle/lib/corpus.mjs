// Corpus discovery. A case is a .txt input with a .meta.json beside it, under
// corpus/inputs/<family>/ or corpus/fleet/. The case id is the file name
// without .txt and is unique across the corpus.
import fs from 'node:fs';
import path from 'node:path';
import { CORPUS_DIR } from './paths.mjs';

// The values of the oracle_dependency meta field: what, besides upstream's
// code, a case's golden depends on. The README's Goldens section states the
// contract for each.
//   math-random  the parse draws from Math.random; the golden holds the
//                first choice (lib/draw.mjs), and regen.mjs fails when the
//                declaration and the oracle disagree.
//   node-base64  upstream's Base64 decoding takes Node's path; a
//                browser-style script host rejects the line instead.
export const ORACLE_DEPENDENCIES = new Set(['math-random', 'node-base64']);

function walk(dir, out) {
    if (!fs.existsSync(dir)) return out;
    for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
        const p = path.join(dir, entry.name);
        if (entry.isDirectory()) walk(p, out);
        else if (entry.name.endsWith('.txt')) out.push(p);
    }
    return out;
}

export function listCases(root = CORPUS_DIR) {
    const files = [...walk(path.join(root, 'inputs'), []), ...walk(path.join(root, 'fleet'), [])];
    const seen = new Map();
    const cases = files.map((file) => {
        const id = path.basename(file, '.txt');
        if (seen.has(id)) throw new Error(`duplicate case id ${id}: ${seen.get(id)} and ${file}`);
        seen.set(id, file);
        const metaFile = file.replace(/\.txt$/, '.meta.json');
        if (!fs.existsSync(metaFile)) throw new Error(`${file} has no ${path.basename(metaFile)}`);
        const meta = JSON.parse(fs.readFileSync(metaFile, 'utf8'));
        if (meta.id !== id) throw new Error(`${metaFile}: id ${meta.id} does not match the file name`);
        const dependency = meta.oracle_dependency ?? null;
        if (dependency !== null && !ORACLE_DEPENDENCIES.has(dependency)) {
            throw new Error(`${metaFile}: unknown oracle_dependency ${dependency}`);
        }
        return {
            id,
            file: path.relative(root, file),
            input: fs.readFileSync(file, 'utf8'),
            meta,
            options: meta.options || {},
            produceAlways: meta.produce === 'always',
            recordName: meta.record_name ?? null,
            dependency,
        };
    });
    return cases.sort((a, b) => (a.id < b.id ? -1 : a.id > b.id ? 1 : 0));
}

// produceInput is the node list a case produces from: the nodes as given,
// or, when the case's meta names a record_name, each node with _subName set
// to it and its keys sorted again. The plugin sets _subName to the record's
// name before producing, and the Surge module header reads it from the first
// node (specs/producers/surge.md). The nodes are not changed.
export function produceInput(c, nodes) {
    if (c.recordName === null || !Array.isArray(nodes)) return nodes;
    return nodes.map((n) => {
        const withName = { ...n, _subName: c.recordName };
        return Object.fromEntries(Object.keys(withName).sort().map((k) => [k, withName[k]]));
    });
}

// filterCases keeps cases whose id starts with one of the given prefixes.
export function filterCases(cases, prefixes) {
    if (!prefixes || prefixes.length === 0) return cases;
    return cases.filter((c) => prefixes.some((p) => c.id.startsWith(p)));
}
