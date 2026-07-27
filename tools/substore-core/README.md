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
