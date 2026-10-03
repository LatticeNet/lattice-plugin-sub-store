#!/usr/bin/env node
// Lists the module-scope state on the parse and produce path of the pinned
// Sub-Store core and compares it with the reviewed list in state-audit.json.
//
// The plugin's convert method runs parse and produce on a QuickJS runtime that
// stays warm across calls, so one identity's credentials could reach another
// identity's document if the core kept data at module scope between calls. The
// warm-runtime tests can only search what is reachable from globalThis; a
// variable captured inside the bundle's closures is invisible to them. This
// audit reads the upstream source instead, at the pinned commit, and fails on
// any module-scope binding, container, write or ambient store access that no
// one has reviewed. A pin update has to pass it before state-audit.json can
// name the new bundle, and the Go test TestEmbeddedCoreIsTheStateAuditedCore
// refuses an embedded bundle that state-audit.json does not name.
import { createHash } from 'node:crypto';
import { existsSync, readFileSync, writeFileSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import { dirname, join, posix, relative, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const defaultPinPath = join(here, 'pin.json');
const defaultAuditPath = join(here, 'state-audit.json');
const defaultBundlePath = join(here, '..', '..', 'system-go', 'lib', 'substore-core.js');

// The convert path: ProxyUtils.parse (preprocessors, parsers) and
// ProxyUtils.produce (producers), plus whatever upstream source they import.
// core/proxy-utils/index.js is scanned but its imports are not followed: it
// also defines process, download and sync, whose imports reach the whole
// backend, none of which parse or produce calls.
export const entryPoints = [
  'core/proxy-utils/index.js',
  'core/proxy-utils/parsers/index.js',
  'core/proxy-utils/preprocessors/index.js',
  'core/proxy-utils/producers/index.js',
];
const unfollowed = new Set(['core/proxy-utils/index.js']);

const importPattern = /(?:^|[\s;])(?:import|export)\s[^'"]*?\sfrom\s+['"]([^'"]+)['"]|(?:^|[\s;])import\s+['"]([^'"]+)['"]/gm;

// Module scope is column zero: upstream is formatted with a four-space indent,
// so anything at column zero is outside every function and block.
const bindingPattern = /^(?:export\s+)?(?:let|var)\s+([A-Za-z_$][\w$]*)/;
const containerPattern = /^(?:export\s+)?const\s+([A-Za-z_$][\w$]*)\s*=\s*(?:new\s+[A-Za-z_$][\w$.]*|\{|\[)/;
const defaultExportPattern = /^export\s+default\s+([A-Za-z_$][\w$]*)\s*;?\s*$/;
const ambientPattern = /\$\.(?:read|write|delete|cache)\b|\$persistentStore|\$prefs|globalThis|\$substore|\$options|localStorage|sessionStorage/;

export function resolveImport(srcRoot, fromFile, specifier) {
  let base;
  if (specifier.startsWith('@/')) {
    base = specifier.slice(2);
  } else if (specifier.startsWith('.')) {
    base = posix.normalize(posix.join(posix.dirname(fromFile), specifier));
  } else {
    return null; // a package from node_modules, pinned by upstream's lockfile
  }
  for (const candidate of [base, `${base}.js`, `${base}/index.js`]) {
    if (candidate.endsWith('.js') && existsSync(join(srcRoot, candidate))) {
      return candidate;
    }
  }
  throw new Error(`cannot resolve ${specifier} from ${fromFile}`);
}

// importsOf lists the upstream files one file imports.
export function importsOf(srcRoot, file, text) {
  const out = new Set();
  for (const match of text.matchAll(importPattern)) {
    const resolved = resolveImport(srcRoot, file, match[1] ?? match[2]);
    if (resolved) out.add(resolved);
  }
  return out;
}

// collectFiles walks the imports from the entry points and returns every file
// on the path with its text and its imports.
export function collectFiles(srcRoot) {
  const sources = new Map();
  const imports = new Map();
  const queue = [...entryPoints];
  while (queue.length > 0) {
    const file = queue.shift();
    if (sources.has(file)) continue;
    const text = readFileSync(join(srcRoot, file), 'utf8');
    sources.set(file, text);
    const own = importsOf(srcRoot, file, text);
    imports.set(file, own);
    if (unfollowed.has(file)) continue;
    for (const next of own) {
      if (!sources.has(next)) queue.push(next);
    }
  }
  const files = [...sources.keys()].sort();
  return { files, sources: new Map(files.map((file) => [file, sources.get(file)])), imports };
}

function escapeRegExp(text) {
  return text.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

function normalizeLine(line) {
  return line.trim().replace(/\s+/g, ' ').slice(0, 160);
}

// declarationsFor finds one file's module-scope bindings and containers, and
// which of them other modules can import.
export function declarationsFor(file, text) {
  const lines = text.split('\n');
  const out = [];
  const defaultExports = new Set();
  lines.forEach((line, index) => {
    const exported = line.startsWith('export ');
    const binding = bindingPattern.exec(line);
    if (binding) {
      out.push({ file, kind: 'module-binding', name: binding[1], exported, index });
      return;
    }
    const container = containerPattern.exec(line);
    if (container) {
      out.push({ file, kind: 'module-container', name: container[1], exported, index });
      return;
    }
    const defaultExport = defaultExportPattern.exec(line);
    if (defaultExport) defaultExports.add(defaultExport[1]);
  });
  for (const declaration of out) {
    if (defaultExports.has(declaration.name)) declaration.exported = true;
  }
  return out;
}

function writePattern(name) {
  const n = escapeRegExp(name);
  const head = `(?<![\\w$.])${n}(?![\\w$])`;
  return new RegExp(
    `${head}\\s*(?:\\[[^\\]]*\\]|\\.[A-Za-z_$][\\w$]*)*\\s*(?:=(?![=>])|\\+=|-=|\\+\\+|--)|` +
      `${head}(?:\\.[A-Za-z_$][\\w$]*)*\\.(?:set|add|push|pop|shift|unshift|splice|delete|clear|sort|reverse|fill)\\(|` +
      `Object\\.assign\\(\\s*${n}(?![\\w$])`,
  );
}

// auditSources reports the module-scope state of a set of files, given as a
// map from path to text. Findings are keyed by name or by the trimmed line,
// never by line number, so upstream edits that only move code do not ask for
// a new review.
//
// What it lists: every module-scope let or var, every module-scope const that
// holds an object, array or constructed instance, every line that writes to
// one of those (in its own file, or anywhere for one that is exported), and
// every line that touches a store or the global object. What it cannot see: a
// write through an alias, or state kept inside a third-party package. The
// warm-runtime tests in system-go are the backstop for both.
export function auditSources(sources, imports = new Map()) {
  const findings = [];
  const declarations = [];
  for (const [file, text] of sources) {
    for (const declaration of declarationsFor(file, text)) {
      declarations.push(declaration);
      findings.push({ file, kind: declaration.kind, name: declaration.name });
    }
  }
  for (const [file, text] of sources) {
    const lines = text.split('\n');
    for (const declaration of declarations) {
      const own = declaration.file === file;
      if (!own && !(declaration.exported && imports.get(file)?.has(declaration.file))) continue;
      const pattern = writePattern(declaration.name);
      const writes = new Set();
      lines.forEach((line, index) => {
        if (own && index === declaration.index) return;
        if (pattern.test(line)) writes.add(normalizeLine(line));
      });
      const name = own ? declaration.name : `${declaration.file}:${declaration.name}`;
      for (const line of [...writes].sort()) {
        findings.push({ file, kind: 'module-write', name, line });
      }
    }
    const ambient = new Set();
    for (const line of lines) {
      if (ambientPattern.test(line)) ambient.add(normalizeLine(line));
    }
    for (const line of [...ambient].sort()) {
      findings.push({ file, kind: 'ambient-store', line });
    }
  }
  return findings;
}

export function findingKey(finding) {
  return [finding.file, finding.kind, finding.name ?? '', finding.line ?? ''].join('\u0000');
}

// compare reports what the audit file does not cover: findings no one has
// reviewed, reviewed entries the source no longer has, and entries whose
// reason was never written.
export function compare(findings, reviewed) {
  const reviewedKeys = new Map(reviewed.map((entry) => [findingKey(entry), entry]));
  const foundKeys = new Set(findings.map(findingKey));
  return {
    unreviewed: findings.filter((finding) => !reviewedKeys.has(findingKey(finding))),
    stale: reviewed.filter((entry) => !foundKeys.has(findingKey(entry))),
    unexplained: reviewed.filter((entry) => typeof entry.reason !== 'string' || entry.reason.trim() === '' || entry.reason.startsWith('TODO')),
  };
}

export function parseArgs(args) {
  const out = { pinPath: defaultPinPath, auditPath: defaultAuditPath, bundlePath: defaultBundlePath, write: false };
  for (let i = 0; i < args.length; i++) {
    const arg = args[i];
    const value = () => {
      if (i + 1 >= args.length) throw new Error(`${arg} requires a value`);
      return args[++i];
    };
    switch (arg) {
      case '--source':
        out.source = value();
        break;
      case '--pin':
        out.pinPath = value();
        break;
      case '--audit':
        out.auditPath = value();
        break;
      case '--bundle':
        out.bundlePath = value();
        break;
      case '--write':
        out.write = true;
        break;
      case '--help':
      case '-h':
        out.help = true;
        break;
      default:
        throw new Error(`unknown argument ${arg}`);
    }
  }
  return out;
}

function usage() {
  return 'usage: node tools/substore-core/audit-state.mjs --source <upstream checkout at pin.commit> [--write]';
}

function sha256File(path) {
  return createHash('sha256').update(readFileSync(path)).digest('hex');
}

function main(args) {
  const opts = parseArgs(args);
  if (opts.help) {
    console.log(usage());
    return 0;
  }
  if (!opts.source) throw new Error(`--source is required\n${usage()}`);
  const pin = JSON.parse(readFileSync(opts.pinPath, 'utf8'));
  const source = resolve(opts.source);
  const head = spawnSync('git', ['-C', source, 'rev-parse', 'HEAD'], { encoding: 'utf8' });
  if (head.status !== 0 || head.stdout.trim() !== pin.commit) {
    throw new Error(`source checkout is ${head.stdout.trim() || 'not a git checkout'}, want ${pin.commit}`);
  }
  const bundleSHA = sha256File(opts.bundlePath);
  if (bundleSHA !== pin.output_sha256) {
    throw new Error(`embedded bundle ${relative(process.cwd(), opts.bundlePath)} is ${bundleSHA}, pin says ${pin.output_sha256}`);
  }
  const srcRoot = join(source, 'backend', 'src');
  const { files, sources, imports } = collectFiles(srcRoot);
  const findings = auditSources(sources, imports);
  const audit = existsSync(opts.auditPath) ? JSON.parse(readFileSync(opts.auditPath, 'utf8')) : { reviewed: [] };
  const reviewed = Array.isArray(audit.reviewed) ? audit.reviewed : [];

  if (opts.write) {
    // Keeps every reason already written and marks new findings TODO, so the
    // verify run fails until a person has read each one.
    const reasons = new Map(reviewed.map((entry) => [findingKey(entry), entry.reason]));
    const next = {
      commit: pin.commit,
      output_sha256: pin.output_sha256,
      files,
      reviewed: findings.map((finding) => ({ ...finding, reason: reasons.get(findingKey(finding)) ?? 'TODO: review' })),
    };
    writeFileSync(opts.auditPath, `${JSON.stringify(next, null, 2)}\n`);
    console.log(`wrote ${findings.length} findings over ${files.length} files to ${opts.auditPath}`);
    return 0;
  }

  const problems = [];
  if (audit.commit !== pin.commit) problems.push(`state-audit.json reviews commit ${audit.commit}, pin is ${pin.commit}`);
  if (audit.output_sha256 !== pin.output_sha256) problems.push(`state-audit.json reviews bundle ${audit.output_sha256}, pin is ${pin.output_sha256}`);
  if (JSON.stringify(audit.files ?? []) !== JSON.stringify(files)) problems.push('the set of files on the parse and produce path changed');
  const { unreviewed, stale, unexplained } = compare(findings, reviewed);
  for (const finding of unreviewed) problems.push(`unreviewed ${finding.kind} in ${finding.file}: ${finding.name ?? finding.line}`);
  for (const entry of stale) problems.push(`reviewed ${entry.kind} in ${entry.file} is gone: ${entry.name ?? entry.line}`);
  for (const entry of unexplained) problems.push(`no reason for ${entry.kind} in ${entry.file}: ${entry.name ?? entry.line}`);
  if (problems.length > 0) {
    for (const problem of problems) console.error(problem);
    console.error(`substore-core state audit: ${problems.length} problem(s); run with --write, review each entry, and write its reason`);
    return 1;
  }
  console.log(JSON.stringify({ commit: pin.commit, output_sha256: pin.output_sha256, files: files.length, findings: findings.length }));
  return 0;
}

if (import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    process.exitCode = main(process.argv.slice(2));
  } catch (error) {
    console.error(`substore-core state audit: ${error.message}`);
    process.exitCode = 1;
  }
}
