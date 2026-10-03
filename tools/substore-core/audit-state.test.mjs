import assert from 'node:assert/strict';
import test from 'node:test';

import { auditSources, compare, parseArgs } from './audit-state.mjs';

function kinds(findings) {
  return findings.map((f) => `${f.kind} ${f.file} ${f.line ?? f.name}`).sort();
}

test('auditSources lists module-scope bindings, containers, their writes and store access', () => {
  const sources = new Map([
    [
      'producers/a.js',
      [
        "import $ from '@/core/app';",
        'let parser;',
        'const table = { a: 1 };',
        'const seen = new Map();',
        'const limit = 3;',
        'export default function produce(proxy) {',
        '    const local = {};',
        '    local.parser = 1;',
        '    if (!parser) parser = build();',
        '    seen.set(proxy.name, proxy.password);',
        '    $.write(proxy, "#last");',
        '    return table[proxy.type] === 1;',
        '}',
      ].join('\n'),
    ],
  ]);
  assert.deepEqual(kinds(auditSources(sources)), [
    'ambient-store producers/a.js $.write(proxy, "#last");',
    'module-binding producers/a.js parser',
    'module-container producers/a.js seen',
    'module-container producers/a.js table',
    'module-write producers/a.js if (!parser) parser = build();',
    'module-write producers/a.js seen.set(proxy.name, proxy.password);',
  ]);
});

test('auditSources follows an exported container only into files that import it', () => {
  const sources = new Map([
    ['shared.js', 'export const VALUES = [];\nconst hidden = {};\nexport default hidden;\n'],
    ['importer.js', "import { VALUES } from './shared';\nfunction f() {\n    VALUES.push(1);\n}\n"],
    ['stranger.js', 'function g() {\n    const VALUES = [];\n    VALUES.push(1);\n}\n'],
  ]);
  const imports = new Map([
    ['shared.js', new Set()],
    ['importer.js', new Set(['shared.js'])],
    ['stranger.js', new Set()],
  ]);
  const writes = auditSources(sources, imports).filter((f) => f.kind === 'module-write');
  assert.deepEqual(writes, [{ file: 'importer.js', kind: 'module-write', name: 'shared.js:VALUES', line: 'VALUES.push(1);' }]);
});

test('compare reports unreviewed, stale and unexplained entries', () => {
  const findings = [
    { file: 'a.js', kind: 'module-binding', name: 'parser' },
    { file: 'a.js', kind: 'module-container', name: 'seen' },
  ];
  const reviewed = [
    { file: 'a.js', kind: 'module-binding', name: 'parser', reason: 'memoized code' },
    { file: 'a.js', kind: 'module-container', name: 'gone', reason: 'was a table' },
    { file: 'b.js', kind: 'ambient-store', line: '$.read(x)', reason: 'TODO: review' },
  ];
  const result = compare(findings, reviewed);
  assert.deepEqual(result.unreviewed.map((f) => f.name), ['seen']);
  assert.deepEqual(result.stale.map((f) => f.name ?? f.line), ['gone', '$.read(x)']);
  assert.deepEqual(result.unexplained.map((f) => f.line), ['$.read(x)']);
});

test('parseArgs requires known flags', () => {
  assert.equal(parseArgs(['--source', '/tmp/Sub-Store', '--write']).write, true);
  assert.throws(() => parseArgs(['--sauce', '/tmp']), /unknown argument/);
  assert.throws(() => parseArgs(['--source']), /requires a value/);
});
