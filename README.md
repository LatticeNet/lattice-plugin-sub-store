# lattice-plugin-sub-store

Official self-contained LatticeNet system plugin for importing vpn-core node
links into an operator-owned Sub-Store backend.

The repository owns the complete plugin experience:

- `system-go/` implements `status` and `import` over the stdio JSON runtime;
- `ui/` is the sandboxed Extensions tab delivered from the signed bundle;
- `tools/pluginpack/` creates deterministic `tar+gzip` artifacts;
- `manifest.json` declares UI, operator scopes, capabilities, runtime platforms,
  compatibility, and the exact outbound RPC dependency.

The Dashboard contains no Sub-Store page, API fallback, secret persistence, or
plugin-specific component. Disabling or removing this plugin removes its tab and
runtime behavior without changing the base console.

## Security boundary

The UI runs in an opaque-origin iframe with scripts only. It has no direct API
client and sends all operations through the nonce-bound Lattice bridge. The host
filters callable methods by the signed manifest and the current operator's RBAC
scopes. The bundle document is served with `connect-src 'none'`.

The runtime declares two host-risk capabilities:

- `rpc:call` for the signed, method-bounded dependency
  `latticenet.vpn-core/nodes.export`;
- `http:operator-target` for operator-entered Sub-Store endpoints.

Those grants exist only while the plugin is active. Ordinary `http:egress`
remains unable to reach private targets. Remote Sub-Store endpoints require
HTTPS; loopback HTTP is allowed for local deployments. Credentials, query
strings, fragments, traversal paths, metadata/link-local destinations, and
unsafe redirects are rejected by the plugin and host transport.

Both interface methods declare `base_url` as an invocation-bound operator target.
The host captures that value from the authenticated call before starting the
runtime, and `http.operator.do` can reach only the same origin beneath that exact
secret-bearing path for the lifetime of the call. The plugin cannot silently
substitute another internal service.

The endpoint includes a secret path. It is kept only in the mounted plugin UI's
memory and is never written to local storage, session storage, cookies, the
Dashboard bundle, or server configuration.

## Local verification

```sh
cd system-go && go test -race ./...
cd ../ui && npm ci && npm test && npm run typecheck && npm run build && npm run verify:build
cd ../tools/pluginpack && go test -race ./...
```

Release automation must build both Linux runtime binaries with `-trimpath
-buildvcs=false`, build the UI, pack a deterministic artifact, set
`bundle.digest_sha256`, sign the manifest with the trusted LatticeNet Ed25519
publisher seed, and publish the alpha release without making it GitHub Latest.
