import assert from 'node:assert/strict';
import test from 'node:test';

import { buildEsbuildArgs, parseArgs, validatePin } from './build.mjs';

const validPin = {
  upstream: 'https://github.com/sub-store-org/Sub-Store.git',
  commit: '48d83214ffe3e1de86a03d80247f2d8202885948',
  backend_package: 'sub-store',
  backend_version: '2.36.22',
  package_manager: 'pnpm@11.0.9',
  source_path: 'src/products/proxy-utils.esm.js',
  global_name: 'SubStoreProxyUtils',
  format: 'iife',
  platform: 'browser',
  minify: true,
  inject: ['core-js/actual/object/has-own'],
  output_bytes: 1266414,
  output_sha256: '9e77eb7b65dbe1e65a7c9eed7d618fbc786aab0b5d360945d65c2e8b84d8428c',
};

test('validatePin accepts the checked-in production pin shape', () => {
  assert.doesNotThrow(() => validatePin(validPin));
});

test('validatePin rejects non-IIFE output', () => {
  assert.throws(() => validatePin({ ...validPin, format: 'esm' }), /iife/);
});

test('validatePin requires a full commit and expected digest', () => {
  assert.throws(() => validatePin({ ...validPin, commit: 'master' }), /40-hex/);
  assert.throws(() => validatePin({ ...validPin, output_sha256: 'abc' }), /64-hex/);
});

test('parseArgs requires explicit output and preserves source checkout option', () => {
  assert.deepEqual(parseArgs(['--output', '/tmp/core.js', '--source', '/tmp/Sub-Store', '--skip-install']), {
    pinPath: new URL('./pin.json', import.meta.url).pathname,
    keepWorkdir: false,
    skipInstall: true,
    output: '/tmp/core.js',
    source: '/tmp/Sub-Store',
  });
});

test('buildEsbuildArgs pins the QuickJS-loadable global without setting a target', () => {
  const args = buildEsbuildArgs(validPin, ['/upstream/node_modules/core-js/actual/object/has-own.js'], '/tmp/core.js');
  assert.deepEqual(args.slice(0, 4), ['pnpm', 'exec', 'esbuild', 'src/products/proxy-utils.esm.js']);
  assert(args.includes('--format=iife'));
  assert(args.includes('--global-name=SubStoreProxyUtils'));
  assert(args.includes('--platform=browser'));
  assert(args.includes('--minify'));
  assert(args.includes('--inject:/upstream/node_modules/core-js/actual/object/has-own.js'));
  assert(args.includes('--outfile=/tmp/core.js'));
  assert(!args.some((arg) => arg.startsWith('--target=')));
});
