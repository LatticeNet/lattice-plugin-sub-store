// Paths and the upstream pin, shared by every oracle script.
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const here = path.dirname(fileURLToPath(import.meta.url));

export const ROOT = path.resolve(here, '..', '..');
export const ORACLE_DIR = path.join(ROOT, 'oracle');
export const PIN_FILE = path.join(ORACLE_DIR, 'upstream.json');
export const BUILD_DIR = path.join(ROOT, 'build');
export const UPSTREAM_DIR = path.join(BUILD_DIR, 'upstream');
export const BACKEND_DIR = path.join(UPSTREAM_DIR, 'backend');
// The bundle lives inside the backend directory so that the few modules
// upstream loads through eval(require(...)) resolve against its node_modules.
export const BUNDLE_DIR = path.join(BACKEND_DIR, '.oracle');
export const BUNDLE_FILE = path.join(BUNDLE_DIR, 'proxy-utils.cjs');
export const STAMP_FILE = path.join(BUNDLE_DIR, 'stamp.json');
export const DATA_DIR = path.join(BUILD_DIR, 'oracle-data');
export const CORPUS_DIR = path.join(ROOT, 'corpus');
export const GOLDENS_DIR = path.join(ROOT, 'goldens');
export const ALLOWLIST_FILE = path.join(ROOT, 'allowlist', 'divergences.yaml');
export const CONFORMANCE_FILE = path.join(ROOT, 'conformance.json');

export function readPin() {
    const pin = JSON.parse(fs.readFileSync(PIN_FILE, 'utf8'));
    for (const key of ['repository', 'commit', 'version', 'entry']) {
        if (typeof pin[key] !== 'string' || pin[key] === '') {
            throw new Error(`${PIN_FILE}: missing ${key}`);
        }
    }
    if (!/^[0-9a-f]{40}$/.test(pin.commit)) {
        throw new Error(`${PIN_FILE}: commit must be a full 40-character SHA`);
    }
    return pin;
}
