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

## Plugin UI

The `ui/` frame is a tabbed Vue 3 app inside the single `sub-store` manifest
view:

- **Subscriptions** — one source of nodes (this fleet's vpn-core export, a
  provider link, or a paste) and **combinations** that merge several of them.
- **Files** — a document the core serves whose proxy list is filled in from a
  subscription or a combination, so a client configuration stays current
  without anyone editing it. Plain text is served as written.
- **Pipelines** — saved conversion recipes: target + operator chain, run over
  pasted content.
- **Convert** — one-shot conversion of pasted content by the embedded engine.
- **Settings** — defaults, backup export and restore.

Subscriptions, combinations and files are one store behind a `kind`
discriminator, so they share `list`/`get`/`save`/`delete` and adding a kind
needs no new signed method.

Every backend call is a `lattice.plugin.call` through the one bridge instance
owned by `src/App.vue`; screens receive it via `src/host.ts` and never open
their own handshake. `src/Shell.vue` holds the tabs and knows nothing about the
bridge, which is what lets `dev/` mount the same screens against a fake host.

All method names live in `src/client.ts` in two tiers:

- **active** — declared by the manifest on the integration line (thirteen
  `…/subscription` methods plus seven `…/engine` methods);
- **pending** — proposed but undeclared methods (empty between contract waves;
  the subset test trips the moment a pending method becomes declared).

`src/contract.test.ts` enforces: active ⊆ manifest, pending ∩ manifest = ∅, and
the engine service fully wired. No screen may call a method the signed manifest
does not declare — engine tabs gate on `canCall` and render an honest
"engine not available" panel against pre-engine manifests.

Verification (`ui/`): `npm test`, `npm run typecheck`, `npm run build`,
`npm run verify:build` — the scanner must keep rejecting inline script/style
and any external URL in `dist`.

### Manual browser test plan

The dev harness under `dev/` covers everything short of the real bridge. What
it cannot answer is whether the host declares the interfaces this build calls,
so the live pass is about the boundary, not the screens:

1. Console → Extensions → Sub-Store: frame loads, no console errors, theme
   tokens applied (toggle light/dark in console settings and watch the frame).
2. Subscriptions tab: create one from this fleet's nodes, preview it, save;
   build a combination over it; confirm both survive a frame reload.
3. Files tab: paste a Mihomo config, point it at that combination, preview —
   the served document must keep your rules and groups and carry the fleet's
   nodes. Switch the type to plain text and confirm the palette narrows to the
   document rewrite step.
4. Pipelines tab: create a pipeline (id, target, operators JSON), edit it, run
   it over pasted raw content, two-step delete; confirm the list survives a
   frame reload (server-side records).
5. Convert tab: paste subscription content, pick a target, convert; node counts
   and byte size render, select-all copy works despite the sandbox blocking
   programmatic clipboard.
6. Resize: frame height follows content in every tab, no clipped buttons.

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

## Embedded Sub-Store core

The embedded conversion engine uses QuickJS-on-wazero with a pinned upstream
Sub-Store `ProxyUtils` bundle. The pin is recorded in
`tools/substore-core/pin.json`; the checked-in runtime payload is
`system-go/lib/substore-core.js`. The current pin uses upstream commit
`48d83214ffe3e1de86a03d80247f2d8202885948`, backend package `sub-store`
`2.36.22`, and bundle SHA-256
`994423340ddfbbcb4c858dc497bbbd249aac89b736a03606ada2f8958b1f0d4b`.

Rebuild the pinned bundle with:

```sh
node tools/substore-core/build.mjs --output system-go/lib/substore-core.js
node --test tools/substore-core/build.test.mjs
```

When bumping upstream, update `tools/substore-core/pin.json` and
`system-go/lib/substore-core.js` together, then rerun the system runtime tests
and the deterministic packer. Any checked-in byte change changes the signed
bundle digest and requires the LatticeNet manifest signing path before release.
Do not replace the embedded engine with a Node sidecar or reverse proxy; remote
fetches must remain host-brokered capabilities rather than runtime-owned
network access.

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
node --test tools/substore-core/build.test.mjs
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

## Looking at the UI

`cd ui && npm run dev` opens a harness at `/dev.html` that mounts the real
screens against a fake host with canned records: a fleet-sourced subscription, a
provider link, a pasted one, a combination that gathers them, and two files — a
Mihomo config drawing its nodes from the combination, and a plain rule list.

It exists because the plugin UI is otherwise unviewable outside a dashboard —
and while it was unviewable, an operator picker that rendered empty and a data
load that never ran both reached production. The harness delays its handshake on
purpose, so a screen that loads before the host is ready and never retries shows
the same empty state it would in production rather than looking fine.

It applies the host's design tokens itself, with a light/dark toggle, because
the shipped bundle has no colours of its own — polishing against the fallbacks
would mean polishing colours nobody sees.

Nothing under `dev/` can reach the signed bundle: the production build has
`index.html` as its only entry, and `verify:build` fails if a dev marker appears
in `dist` anyway.
