import assert from 'node:assert/strict';
import test from 'node:test';

import { judge } from './conformance-end-to-end.mjs';

// report builds a check.mjs --report object from parse and produce failures.
function report({ endToEnd = false, cases = 4, parseFailures = [], produce = {} }) {
  return {
    end_to_end: endToEnd || undefined,
    summary: {
      parse: { cases, passed: cases - parseFailures.length },
      produce: Object.fromEntries(Object.entries(produce).map(([t, fs]) => [t, { cases, passed: cases - fs.length }])),
    },
    failures: {
      parse: parseFailures.map((c) => ({ case: c, path: '$', golden: 'g', candidate: 'c' })),
      produce: Object.fromEntries(Object.entries(produce).map(([t, fs]) => [t, fs.map((c) => ({ case: c, path: '$', golden: 'g', candidate: 'c' }))])),
    },
  };
}

const strict = report({ parseFailures: ['clash-a', 'surge-external'], produce: { uri: [] } });

test('produce failures in cases that parse only under the allowlist pass', () => {
  const e2e = report({ endToEnd: true, produce: { uri: ['clash-a'], json: ['clash-a', 'surge-external'] } });
  const { lines, problems } = judge(e2e, strict);
  assert.deepEqual(problems, []);
  assert.match(lines.join('\n'), /json: 2\/4, 2 failing cases parse only under the allowlist/);
});

test('a produce failure in any other case fails', () => {
  const e2e = report({ endToEnd: true, produce: { uri: ['clash-a', 'vless-b'] } });
  assert.deepEqual(judge(e2e, strict).problems, ['uri vless-b at $: golden g, candidate c']);
});

test('a parse failure in the end-to-end run fails', () => {
  const e2e = report({ endToEnd: true, parseFailures: ['vless-b'], produce: { uri: [] } });
  assert.deepEqual(judge(e2e, strict).problems, ['parse vless-b at $']);
});

test('reports of different corpora or swapped runs fail', () => {
  const e2e = report({ endToEnd: true, cases: 5, produce: { uri: [] } });
  assert.deepEqual(judge(e2e, strict).problems, ['the reports cover different corpora: 5 and 4 parse cases']);
  assert.deepEqual(judge(report({ produce: { uri: [] } }), report({ endToEnd: true, produce: { uri: [] } })).problems, [
    'the first report is not from an --end-to-end run',
    'the strict parse report is from an --end-to-end run',
  ]);
});
