#!/usr/bin/env node
import { spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { mkdirSync, mkdtempSync, readFileSync, rmSync, statSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const defaultPinPath = join(here, 'pin.json');

export function validatePin(pin) {
  const requiredStrings = [
    'upstream',
    'commit',
    'backend_package',
    'backend_version',
    'package_manager',
    'source_path',
    'global_name',
    'format',
    'platform',
    'output_sha256',
  ];
  for (const key of requiredStrings) {
    if (typeof pin[key] !== 'string' || pin[key].trim() === '') {
      throw new Error(`pin.${key} must be a non-empty string`);
    }
  }
  if (!/^[0-9a-f]{40}$/i.test(pin.commit)) {
    throw new Error('pin.commit must be a full 40-hex git commit');
  }
  if (!/^[0-9a-f]{64}$/i.test(pin.output_sha256)) {
    throw new Error('pin.output_sha256 must be a 64-hex sha256');
  }
  if (!Number.isInteger(pin.output_bytes) || pin.output_bytes <= 0) {
    throw new Error('pin.output_bytes must be a positive integer');
  }
  if (!Array.isArray(pin.inject) || pin.inject.some((value) => typeof value !== 'string' || value.trim() === '')) {
    throw new Error('pin.inject must be a list of module specifiers');
  }
  if (pin.format !== 'iife') {
    throw new Error('pin.format must stay iife for QuickJS global loading');
  }
  if (pin.platform !== 'browser') {
    throw new Error('pin.platform must stay browser so Node globals are not bundled as runtime assumptions');
  }
  if (pin.minify !== true) {
    throw new Error('pin.minify must stay true to reproduce the signed core bundle size');
  }
}

export function parseArgs(args) {
  const out = { pinPath: defaultPinPath, keepWorkdir: false, skipInstall: false };
  for (let i = 0; i < args.length; i++) {
    const arg = args[i];
    switch (arg) {
      case '--pin':
        out.pinPath = requireValue(args, ++i, arg);
        break;
      case '--output':
        out.output = requireValue(args, ++i, arg);
        break;
      case '--workdir':
        out.workdir = requireValue(args, ++i, arg);
        break;
      case '--source':
        out.source = requireValue(args, ++i, arg);
        break;
      case '--keep-workdir':
        out.keepWorkdir = true;
        break;
      case '--skip-install':
        out.skipInstall = true;
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

export function buildEsbuildArgs(pin, injectPaths, outputPath) {
  return [
    'pnpm',
    'exec',
    'esbuild',
    pin.source_path,
    '--bundle',
    `--format=${pin.format}`,
    `--global-name=${pin.global_name}`,
    `--platform=${pin.platform}`,
    '--minify',
    ...injectPaths.map((injectPath) => `--inject:${injectPath}`),
    `--outfile=${outputPath}`,
  ];
}

function requireValue(args, index, flag) {
  const value = args[index];
  if (!value || value.startsWith('--')) {
    throw new Error(`${flag} requires a value`);
  }
  return value;
}

function usage() {
  return `usage: node tools/substore-core/build.mjs --output <path> [--source <checkout>] [--workdir <dir>] [--skip-install]`;
}

function readPin(path) {
  const pin = JSON.parse(readFileSync(path, 'utf8'));
  validatePin(pin);
  return pin;
}

function run(command, args, options = {}) {
  const result = spawnSync(command, args, {
    cwd: options.cwd,
    env: process.env,
    encoding: 'utf8',
    stdio: options.capture ? ['ignore', 'pipe', 'pipe'] : 'inherit',
  });
  if (result.status !== 0) {
    const stderr = options.capture && result.stderr ? `: ${result.stderr.trim()}` : '';
    throw new Error(`${command} ${args.join(' ')} failed${stderr}`);
  }
  return options.capture ? result.stdout.trim() : '';
}

function ensureCheckout(pin, opts) {
  if (opts.source) {
    const source = resolve(opts.source);
    const head = run('git', ['-C', source, 'rev-parse', 'HEAD'], { capture: true });
    if (head !== pin.commit) {
      throw new Error(`source checkout is ${head}, want ${pin.commit}`);
    }
    return source;
  }

  const workdir = opts.workdir ? resolve(opts.workdir) : mkdtempSync(join(tmpdir(), 'lattice-substore-core-'));
  mkdirSync(workdir, { recursive: true });
  const source = join(workdir, 'Sub-Store');
  run('git', ['clone', '--no-checkout', pin.upstream, source]);
  run('git', ['-C', source, 'fetch', '--depth', '1', 'origin', pin.commit]);
  run('git', ['-C', source, 'checkout', '--detach', pin.commit]);
  return source;
}

function verifyBackendPackage(pin, backendDir) {
  const pkg = JSON.parse(readFileSync(join(backendDir, 'package.json'), 'utf8'));
  if (pkg.name !== pin.backend_package) {
    throw new Error(`backend package ${pkg.name}, want ${pin.backend_package}`);
  }
  if (pkg.version !== pin.backend_version) {
    throw new Error(`backend version ${pkg.version}, want ${pin.backend_version}`);
  }
  if (pkg.packageManager !== pin.package_manager) {
    throw new Error(`package manager ${pkg.packageManager}, want ${pin.package_manager}`);
  }
}

function resolveInjects(pin, backendDir) {
  return pin.inject.map((specifier) =>
    run(process.execPath, ['-p', `require.resolve(${JSON.stringify(specifier)})`], {
      cwd: backendDir,
      capture: true,
    }),
  );
}

function verifyOutput(pin, outputPath) {
  const data = readFileSync(outputPath);
  const sha256 = createHash('sha256').update(data).digest('hex');
  const bytes = statSync(outputPath).size;
  if (bytes !== pin.output_bytes) {
    throw new Error(`output bytes ${bytes}, want ${pin.output_bytes}`);
  }
  if (sha256 !== pin.output_sha256) {
    throw new Error(`output sha256 ${sha256}, want ${pin.output_sha256}`);
  }
  return { bytes, sha256 };
}

async function main(args) {
  const opts = parseArgs(args);
  if (opts.help) {
    console.log(usage());
    return;
  }
  if (!opts.output) {
    throw new Error(`--output is required\n${usage()}`);
  }
  const pin = readPin(opts.pinPath);
  let cleanupDir = null;
  const checkoutOpts = { ...opts };
  if (!opts.source && !opts.workdir) {
    cleanupDir = mkdtempSync(join(tmpdir(), 'lattice-substore-core-'));
    checkoutOpts.workdir = cleanupDir;
  }
  const source = ensureCheckout(pin, checkoutOpts);
  try {
    const backendDir = join(source, 'backend');
    verifyBackendPackage(pin, backendDir);
    if (!opts.skipInstall) {
      run('corepack', ['pnpm', 'install', '--frozen-lockfile'], { cwd: backendDir });
    }
    const injectPaths = resolveInjects(pin, backendDir);
    run('corepack', buildEsbuildArgs(pin, injectPaths, resolve(opts.output)), { cwd: backendDir });
    const result = verifyOutput(pin, resolve(opts.output));
    console.log(
      JSON.stringify({
        upstream: pin.upstream,
        commit: pin.commit,
        backend_version: pin.backend_version,
        output: resolve(opts.output),
        ...result,
      }),
    );
  } finally {
    if (cleanupDir && !opts.keepWorkdir) {
      rmSync(cleanupDir, { recursive: true, force: true });
    }
  }
}

if (import.meta.url === pathToFileURL(process.argv[1]).href) {
  main(process.argv.slice(2)).catch((error) => {
    console.error(`substore-core build: ${error.message}`);
    process.exit(1);
  });
}
