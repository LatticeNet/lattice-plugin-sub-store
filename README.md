# lattice-plugin-sub-store

Official self-contained LatticeNet system plugin for building and serving proxy
subscriptions. An operator points a record at this fleet's vpn-core nodes, at a
provider link, or at pasted content, combines records, converts them for a
client, and publishes the result. The conversion engine is embedded in the
bundle, so nothing depends on a standalone Sub-Store instance.

The repository owns the complete plugin experience:

- `system-go/` implements the `latticenet.sub-store/subscription` and
  `latticenet.sub-store/engine` services over the stdio JSON runtime;
- `ui/` is the sandboxed Extensions tab delivered from the signed bundle;
- `tools/pluginpack/` creates deterministic `tar+gzip` artifacts;
- `tools/substore-core/` pins and rebuilds the embedded conversion engine;
- `manifest.json` declares the UI, operator scopes, capabilities, runtime
  platforms, compatibility, and the exact outbound RPC dependencies.

The Dashboard contains no Sub-Store page, API fallback, secret persistence, or
plugin-specific component. Disabling or removing this plugin removes its tab and
runtime behavior without changing the base console.

## Plugin UI

The `ui/` frame is a tabbed Vue 3 app inside the single `sub-store` manifest
view. It ships three tabs:

- **Subscriptions** one source of nodes (this fleet's vpn-core export, a
  provider link, or a paste) and **combinations** that merge several of them.
- **Files** a document the core serves whose proxy list is filled in from a
  subscription or a combination, so a client configuration stays current without
  anyone editing it. Three file types: a config the engine rewrites, plain text
  served as written, and a script that generates the document.
- **Settings** defaults, migration from another Sub-Store, and backup export and
  restore.

Pipelines and Convert were removed rather than left as tabs that did nothing.
The engine methods behind them are still declared and still used by preview and
render.

Subscriptions, combinations and files are one store behind a `kind`
discriminator, so they share `list`, `get`, `save` and `delete`, and adding a
kind needs no new signed method. They also share one budget: 256 records across
all kinds, which is why the counter on one tab can sit below the cap while the
new-record button is disabled.

Nothing in this plugin is reachable by a client until a share is published for
it, in the dashboard under Networking. Deleting a record does not retract a
share, and the UI says so at each point where that matters.

Every backend call is a `lattice.plugin.call` through the one bridge instance
owned by `src/App.vue`; screens receive it via `src/host.ts` and never open their
own handshake. `src/Shell.vue` holds the tabs and knows nothing about the bridge,
which is what lets `dev/` mount the same screens against a fake host.

All method names live in `src/client.ts` in two tiers:

- **active** declared by the manifest: 16 `…/subscription` methods, 7
  `…/engine` methods, and `…/shares.list`, which is core-backed and used only to
  tell an operator whether a record already has a published share;
- **pending** proposed but undeclared methods (empty between contract waves; the
  subset test trips the moment a pending method becomes declared).

`src/contract.test.ts` enforces active ⊆ manifest, pending ∩ manifest = ∅, and
that the retired `latticenet.sub-store/import/*` service has not reappeared. No
screen may call a method the signed manifest does not declare. Screens gate on
`host.available(...)` for the binding they need, and render a panel saying the
methods are unavailable to this session, which covers both an older bundle and a
token without the scope.

Verification (`ui/`): `npm test`, `npm run typecheck`, `npm run build`,
`npm run verify:build`. The scanner must keep rejecting inline script or style
and any external URL in `dist`.

### Manual browser test plan

The dev harness under `dev/` covers everything short of the real bridge. What it
cannot answer is whether the host declares the interfaces this build calls, so
the live pass is about the boundary, not the screens:

1. Console, then Extensions, then Sub-Store: the frame loads, no console errors,
   theme tokens applied (toggle light and dark in console settings and watch the
   frame).
2. Subscriptions tab: create one from this fleet's nodes, preview it, save it;
   build a combination over it; confirm both survive a frame reload.
3. Files tab: paste a Mihomo config, point it at that combination, preview. The
   served document must keep your rules and groups and carry the fleet's nodes.
   Repeat for the plain and script types.
4. Publish a record to a destination you control, then confirm the row reports
   what the destination answered rather than a bare success.
5. Settings tab: export a backup, restore it, and confirm the confirmation
   dialog lists what the envelope will overwrite before it arms.
6. Resize the host pane to 375px and 1440px. The frame remains the viewport,
   each sheet stays centred inside it, long output has one reachable scroll
   surface, and no button or evidence strip is clipped.

## Security boundary

The UI runs in an opaque-origin iframe with scripts only. It has no direct API
client and sends all operations through the nonce-bound Lattice bridge. The host
filters callable methods by the signed manifest and the current operator's RBAC
scopes. The bundle document is served with `connect-src 'none'`.

The runtime declares six host-risk capabilities: `rpc:call`, `http:egress`,
`http:operator-target`, `kv:read`, `kv:write`, and `subscription:serve`. The last
is what lets the core serve a published subscription document at a share URL.

The signed outbound RPC dependencies are exactly `latticenet.vpn-core/nodes.export`
and `latticenet.vpn-core/subscription-sources` (`compose`, `graph_options`).

Two methods declare an invocation-bound operator target: `migrate` on `base_url`,
and `publish` on `destination`. The host captures that value from the
authenticated call before starting the runtime, and `http.operator.do` can reach
only the same origin beneath that exact path for the lifetime of the call. The
plugin cannot silently substitute another internal service.

Those grants exist only while the plugin is active. Ordinary `http:egress`
remains unable to reach private targets. Remote endpoints require HTTPS; loopback
HTTP is allowed for local deployments. Credentials, query strings, fragments,
traversal paths, metadata and link-local destinations, and unsafe redirects are
rejected by the plugin and the host transport.

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
Do not replace the embedded engine with a Node sidecar or a reverse proxy;
remote fetches must stay host-brokered capabilities rather than runtime-owned
network access.

## Scope migration and rollback

The server floor in `manifest.json` (`compatibility.server`) provides directional
runtime compatibility:

| Existing grant | vpn-core | Sub-Store | Native proxy APIs |
| --- | --- | --- | --- |
| `proxy:read/admin` | matching read/admin allowed | matching read/admin allowed | allowed |
| `vpncore:read/admin` | allowed | denied | matching read/admin allowed |
| `substore:read/admin` | denied | allowed | denied |

Read never implies admin, and `prefix:*` follows the same directions. Delegation
is directed: legacy proxy grants may delegate equal-strength canonical scopes for
migration; canonical scopes cannot delegate proxy scopes or each other.

Roll out the compatible server first, then the matching Dashboard, then this
canonical-scope manifest. Roll back in reverse: restore the plugin manifests to
legacy `proxy:*` declarations first, then the Dashboard, and remove server
compatibility last, only after canonical grants have been migrated or removed.

## Local verification

`ui/.npmrc` points `@latticenet` at GitHub Packages, so `npm ci` needs a
`GITHUB_TOKEN` in the environment.

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
Ed25519 publisher seed, and publishes the alpha release without making it GitHub
Latest.

## Looking at the UI

`cd ui && npm run dev` opens a harness at `/dev.html` that mounts the real
screens against a fake host with canned records: five subscriptions, two
combinations, and four files. `/dev-frame.html` shows the same harness inside a
frame of a fixed width, 375 by 812 unless `?w=` and `?h=` say otherwise, so a
review at a phone width or at 1440 does not depend on the browser window; every
other query parameter is handed through to the harness.

The page is the shared plugin chassis from `@latticenet/plugin-bridge/chassis`
(page header, proof line, stat strip, toolbar with the lens tabs, the table card
with its group rows), the same skeleton the vpn-core Lines page draws. Until
the chassis is published, `ui/package.json` points the dependency at the packed
build in `ui/vendor/`; swap it back to a registry version once one exists.

It exists because the plugin UI is otherwise unviewable outside a dashboard, and
while it was unviewable, an operator picker that rendered empty and a data load
that never ran both reached production. The harness delays its handshake on
purpose, so a screen that loads before the host is ready and never retries shows
the same empty state it would in production rather than looking fine.

It applies the host's design tokens itself, with a light and dark toggle, because
the shipped bundle has no colours of its own. Polishing against the fallbacks
would mean polishing colours nobody sees.

Nothing under `dev/` can reach the signed bundle: the production build has
`index.html` as its only entry, and `verify:build` fails if a dev marker appears
in `dist` anyway.
