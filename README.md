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

## Scope migration and rollback

The `>=0.2.2-alpha.2` server floor provides directional runtime compatibility:

| Existing grant | vpn-core | Sub-Store | Native proxy APIs |
| --- | --- | --- | --- |
| `proxy:read/admin` | matching read/admin allowed | matching read/admin allowed | allowed |
| `vpncore:read/admin` | allowed | denied | matching read/admin allowed |
| `substore:read/admin` | denied | allowed | denied |

Read never implies admin, and `prefix:*` follows the same directions. Delegation
is directed: legacy proxy grants may delegate equal-strength canonical scopes
for migration; canonical scopes cannot delegate proxy scopes or each other.

Roll out the compatible server first, then the matching Dashboard, then this
canonical-scope manifest. Roll back in reverse: restore the plugin manifests to
legacy `proxy:*` declarations first, then the Dashboard, and remove server
compatibility last only after canonical grants have been migrated or removed.

## Local verification

```sh
cd system-go && go test -race ./...
cd ../ui && npm ci && npm test && npm run typecheck && npm run build && npm run verify:build
cd ../tools/pluginpack && go test -race ./...
```

Release automation must build the UI with Node.js 22 and both Linux runtime
binaries with Go 1.26.4 and `-trimpath -buildvcs=false`. Both pinned toolchains
are part of the signed byte contract. It then packs a deterministic artifact,
sets `bundle.digest_sha256`, signs the manifest with the trusted LatticeNet
Ed25519 publisher seed, and publishes the alpha release without making it
GitHub Latest.
