#!/usr/bin/env node
// Judges the vendored checker's end-to-end run (S1 plan section 5.1):
//
//   node tools/conformance-end-to-end.mjs <end-to-end report> <strict parse report>
//
// check.mjs --end-to-end produces from the candidate's own parse output and
// compares it with produce goldens that upstream wrote from the golden nodes.
// Where the native parse matches its golden only under the parse-stage
// allowlist (external, underscore, ca), the candidate's nodes are the golden
// nodes with nodes dropped or keys stripped, so the produce golden describes
// a document Lattice deliberately does not write, and the allowlist has no
// produce-stage entry to cover it. The second report names those cases: it
// is a check.mjs run of the same candidate with an empty allowlist
// (divergences: []), in which exactly they fail to parse.
//
// The run passes when every case parses, every produce failure of the
// end-to-end report is one of those cases, and both reports cover the same
// corpus. It prints the numbers and exits 1 otherwise, 2 on unusable input.
import { readFileSync } from 'node:fs';
import { pathToFileURL } from 'node:url';

// judge returns the summary lines and the problems found.
export function judge(endToEnd, strict) {
  const problems = [];
  if (endToEnd.end_to_end !== true) problems.push('the first report is not from an --end-to-end run');
  if (strict.end_to_end === true) problems.push('the strict parse report is from an --end-to-end run');
  const parse = endToEnd.summary.parse;
  if (parse.cases === 0 || parse.cases !== strict.summary.parse.cases) {
    problems.push(`the reports cover different corpora: ${parse.cases} and ${strict.summary.parse.cases} parse cases`);
  }
  for (const f of endToEnd.failures.parse) problems.push(`parse ${f.case} at ${f.path}`);
  const allowlisted = new Set(strict.failures.parse.map((f) => f.case));
  const lines = [`end to end: parse ${parse.passed}/${parse.cases}`];
  for (const [target, failures] of Object.entries(endToEnd.failures.produce)) {
    const { cases, passed } = endToEnd.summary.produce[target];
    const explained = new Set();
    for (const f of failures) {
      if (allowlisted.has(f.case)) explained.add(f.case);
      else problems.push(`${target} ${f.case} at ${f.path}: golden ${f.golden}, candidate ${f.candidate}`);
    }
    lines.push(`  ${target}: ${passed}/${cases}, ${explained.size} failing cases parse only under the allowlist`);
  }
  lines.push(`  ${allowlisted.size} cases parse only under the allowlist: ${[...allowlisted].sort().join(', ')}`);
  return { lines, problems };
}

function main(args) {
  if (args.length !== 2) {
    console.error('usage: node tools/conformance-end-to-end.mjs <end-to-end report> <strict parse report>');
    return 2;
  }
  let endToEnd, strict;
  try {
    [endToEnd, strict] = args.map((file) => JSON.parse(readFileSync(file, 'utf8')));
  } catch (e) {
    console.error(`conformance-end-to-end: ${e.message}`);
    return 2;
  }
  const { lines, problems } = judge(endToEnd, strict);
  console.log(lines.join('\n'));
  for (const p of problems) console.error(`  FAIL ${p}`);
  return problems.length > 0 ? 1 : 0;
}

if (import.meta.url === pathToFileURL(process.argv[1]).href) process.exit(main(process.argv.slice(2)));
