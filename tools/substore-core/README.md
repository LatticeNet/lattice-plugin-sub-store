# Sub-Store Core Builder

`pin.json` records the upstream Sub-Store commit and the byte-identical
ProxyUtils bundle that Phase 1 proved under QuickJS-on-wazero.

Build the pinned bundle locally:

```sh
node tools/substore-core/build.mjs --output /tmp/substore-core.js
```

Use an existing upstream checkout when iterating:

```sh
node tools/substore-core/build.mjs \
  --source /tmp/Sub-Store \
  --skip-install \
  --output /tmp/substore-core.js
```

The build intentionally uses upstream's backend package manager (`pnpm@11.0.9`),
bundles `backend/src/products/proxy-utils.esm.js` as an IIFE global named
`SubStoreProxyUtils`, injects upstream's `Object.hasOwn` polyfill, and verifies
the expected byte count plus SHA-256. Do not add an esbuild `--target` unless the
pin is intentionally regenerated and remeasured; the Phase 1 spike hash was
produced with esbuild's default target.

This tool does not update plugin release fields. Any checked-in runtime byte
change still requires the pinned release builder, a new `bundle.digest_sha256`,
and a Zeus/operator manifest re-sign.

## Auditing module-scope state on a pin change

The plugin's `convert` method runs `parse` and `produce` on a QuickJS runtime
that stays warm across calls and identities. That is safe only while the core
keeps no input at module scope between calls. `state-audit.json` records the
review for the current pin: every module-scope binding, container, write and
store access on the parse and produce path, each with the reason it holds no
data. The Go test `TestEmbeddedCoreIsTheStateAuditedCore` fails when the
embedded bundle is not the one `state-audit.json` names, so a pin change is
not finished until the audit passes for it.

```sh
node tools/substore-core/audit-state.mjs --source /tmp/Sub-Store
```

`--source` must be an upstream checkout at `pin.commit`, and the embedded
`system-go/lib/substore-core.js` must already be the new bundle. When the audit
reports findings, rerun it with `--write`: reasons already written are kept and
each new finding is marked `TODO: review`. Read each new entry in the upstream
source and replace the marker with the reason it carries no data from one call
to the next. If one does carry data, do not write a reason: move `convert` to
the isolated runtime (`runIsolatedScript`) instead. Run the tool tests with
`node --test tools/substore-core/build.test.mjs tools/substore-core/audit-state.test.mjs`.
