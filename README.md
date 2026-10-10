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

- **active** declared by the manifest: 21 `…/subscription` methods, 7
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
`kv:write` also covers `kv.delete`, which the split record store uses to
archive, restore and purge records. That host call and the per-method
`http_response_bytes` budget both first exist in the server release
`compatibility.server` names as its floor; the release before it refuses the
manifest. Every method that can reach a provider body declares 8 MiB, the cap
the provider fetch itself enforces: `fetch`, `probe`, `render`, `publish`,
`preview` and `preview_draft`. The others keep the host default of 256 KiB, and
a script's own requests stay at 256 KiB per response in every method, enforced
by the plugin where the method budget is larger.

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
`994423340ddfbbcb4c858dc497bbbd249aac89b736a03606ada2f8958b1f0d4b`. The bundle
is AGPL-3.0 upstream code shipped inside this MIT plugin; see
`THIRD_PARTY_NOTICES.md`.

Every QuickJS runtime the engine creates, the warm one and each per-call
isolated one, is sealed before the core loads. The qjs wasm build publishes
its `qjs:std`, `qjs:os` and `qjs:bjson` modules as `globalThis.std`, `os` and
`bjson` and mounts a host directory read-write as the guest's `/`; the engine
deletes those three globals and mounts a path that does not exist and cannot
be created, so no script, the core included, can read, list or write a file,
and the WASI environment is empty. The modules stay registered, so a dynamic
`import("qjs:os")` still resolves, but it reaches nothing.
`system-go/substore_engine_sandbox_test.go` asserts this from a call script, a
user script operator and the core's own top level, on both runtimes. Both
runtimes also carry an in-process deadline: the isolated runtime's context
expires with the call, and a watchdog cancels the warm runtime's context and
retires it, so a catastrophic regex or an oversized document on the scriptless
path costs one call its budget, not the worker.

Node-list conversions go through a native dispatcher first
(`system-go/engine_dispatch.go`). A render, a collection, a preview or a
`convert` answers in Go when its target is URI, V2Ray, JSON, sing-box or
ClashMeta and every enabled step of its chain compiles natively; otherwise the
whole chain runs on the bundle's isolated runtime, never half in each. Resolve
Domain, the two script steps, a pattern RE2 refuses and arguments only the
bundle reads keep a chain on the bundle. Operator patterns compile with their
ECMAScript meaning of `\s`, `\S` and the dot (`system-go/operators/regex.go`).
The warm runtime still serves the node count of a refresh whose chain runs on
the bundle, previews of scriptless chains that do, and the legacy engine
service.

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
node --test tools/substore-core/build.test.mjs tools/conformance-end-to-end.test.mjs
cd system-go && go test -race ./...
cd ../ui && npm ci && npm test && npm run typecheck && npm run build && npm run verify:build
npx playwright install chromium && npm run test:e2e
cd ../tools/pluginpack && go test -race ./...
cd ../perfgate && go test -race ./...
```

Release automation must build the UI with Node.js 22 and both Linux runtime
binaries with Go 1.26.9 and `-trimpath -buildvcs=false`. Both pinned toolchains
are part of the signed byte contract. It then packs a deterministic artifact,
sets `bundle.digest_sha256`, signs the manifest with the trusted LatticeNet
Ed25519 publisher seed, and publishes the alpha release without making it GitHub
Latest.

## Conformance data

`conformance/` is a copy of the private conformance harness
(`lattice-substore-conformance`) laid out as the harness root, so its checker
runs unmodified: the corpus, the goldens, the divergence allowlist,
`oracle/check.mjs` with its library, the upstream pin and the package files.
It is Lattice-authored code and synthetic data. The harness's behaviour
specifications are written from reading upstream Sub-Store and are never
copied. `conformance/HARNESS_COMMIT` names the harness commit the copy came
from; refresh it only with the sync script, which copies exactly those paths
from that commit:

```sh
tools/conformance-sync.sh ../lattice-substore-conformance v0.1.0-alpha.1
```

`TestVendoredDataCarriesNoUpstreamText` refuses upstream text and any path the
script does not copy, and `TestVendoredCheckerMatchesHarnessCommit` compares
every vendored file with the harness at that commit when a harness checkout is
present (`LATTICE_SUBSTORE_CONFORMANCE`, or the sibling directory); CI has
none and skips it. `system-go/cmd/substore-conformance` is the candidate the
checker drives over its line protocol; it is never part of the plugin
artifact:

```sh
(cd system-go && go build -o "${TMPDIR:-/tmp}/substore-conformance" ./cmd/substore-conformance)
npm ci --prefix conformance/oracle
node conformance/oracle/check.mjs --candidate "${TMPDIR:-/tmp}/substore-conformance" \
  --targets uri,v2ray,json,singbox,clashmeta,stash,shadowrocket,surge,quantumultx \
  --report "${TMPDIR:-/tmp}/conformance-report.json"
```

The runner answers `parse` from `system-go/parse` with the external opt-in off,
so the hostile-content rules apply as they do to remote input, and `produce`
from `system-go/producers`. It applies no operator, node ceiling or zero-node
refusal. The `conformance` CI job runs the checker from the golden nodes with
`--expect conformance/conformance.json`, so a corpus, golden or allowlist
change regenerates that file in the same commit:

```sh
node conformance/oracle/check.mjs --candidate "${TMPDIR:-/tmp}/substore-conformance" \
  --targets uri,v2ray,json,singbox,clashmeta,stash,shadowrocket,surge,quantumultx \
  --conformance conformance/conformance.json
```

It then runs the checker with `--end-to-end`, producing from the runner's own
parse output. A case whose parse matches its golden only under the allowlist
cannot match produce goldens written from nodes Lattice does not build, so
`tools/conformance-end-to-end.mjs` judges that run: it passes when every
produce failure is one of the cases a second run with an empty allowlist
fails to parse. `TestProduceEndToEndFromOwnParse` holds the same rule in Go.

### Conformance numbers

Design 28 publishes three numbers per release. Measured on the native
engine against harness commit 7186dbf (upstream 2.42.3, a3e6106):

| Number | Result |
|---|---|
| Parse | 639 of 639 corpus cases (100 percent) |
| Produce, URI | 611 of 611, byte for byte |
| Produce, V2Ray | 611 of 611, byte for byte |
| Produce, JSON | 611 of 611 |
| Produce, sing-box | 611 of 611 |
| Produce, ClashMeta | 611 of 611 |
| Produce, Stash | 611 of 611 |
| Produce, Shadowrocket | 611 of 611 |
| Produce, Surge | 611 of 611 (609 byte for byte, which the checker does not require) |
| Produce, Quantumult X | 611 of 611 (610 byte for byte, which the checker does not require) |
| Script | not measured until S3, which fixes the named community script set |

The parse number counts corpus cases as `check.mjs` does, not lines: a case is
one subscription document, and it passes when every node the document yields
deep-equals the golden. One case (`clash-norm-ca-not-pem`) is a whole-document
failure in the golden and passes by failing the same way. The run relied on
four of the five allowlist entries: `external`, `underscore` and `ca` at
parse, and `line-safety` for Surge and Quantumult X (`require` applies to
scripts and stays pending until S3). `line-safety` covers the two cases whose
golden carries a line break from a node field, `clash-socks5-name-newline`
and `clash-ssh`: the native Surge and Quantumult X producers reject a node
whose entry would hold a control character, U+2028 or U+2029, where upstream
writes the text as it is and lets a feed add lines such as a `[Script]`
section to the profile. Those cases match the harness's line-safe golden,
upstream's output without the line-breaking nodes, which is why Surge and
Quantumult X match 609 and 610 cases byte for byte. End to end, from the
runner's own parse, the nine targets match 600, 599, 590, 605, 605, 607, 605,
609 and 610 of 611; every miss is one of the 21 cases that parse only under
the parse-stage entries, and the other 590 cases match for every target.

The Stash, Shadowrocket, Surge and Quantumult X producers (S2) are judged here
and in CI, but `producers.Native` still answers false for their targets, so
the dispatcher sends them to the embedded bundle with the five Tier 2 targets
until their harness ids join `routed` in `system-go/producers/producer.go`.

`system-go/perfgen` generates the perf gate's synthetic VLESS Reality nodes.
The pipeline benchmarks time design 28's measure, the nodes through four
non-script operators to sing-box; the parse of the same nodes' links is a
benchmark of its own. The `perf` job runs the pipeline, parse and producer
benchmarks ten times on ubuntu-24.04 and `tools/perfgate` judges the medians
against the S1 targets and `system-go/testdata/bench/ubuntu-24.04.txt`. That
baseline is the job's output from two runs that landed on different CPUs (an
Intel Xeon 8573C and an AMD EPYC 7763, which the label hands out; the second
is up to 1.57 times slower), so the 1.5 times rule does not trip on the CPU a
run happens to get. Move it the same way, from at least two CPUs, with the
reason in the commit. The `memory` job runs the
allocation and heap gates without the race detector and records the built
worker's resident set (`TestWorkerVmRSS`), which S2 starts enforcing.

## Looking at the UI

`cd ui && npm run dev` opens a harness at `/dev.html` that mounts the real
screens against a fake host with canned records: five subscriptions, two
combinations, and four files. `/dev-frame.html` shows the same harness inside a
frame of a fixed width, 375 by 812 unless `?w=` and `?h=` say otherwise, so a
review at a phone width or at 1440 does not depend on the browser window; every
other query parameter is handed through to the harness.

The page is the shared plugin chassis from `@latticenet/plugin-bridge/chassis`
(page header, proof line, stat strip, toolbar with the lens tabs, the table card
with its group rows), the same skeleton the vpn-core Lines page draws.
`ui/package.json` pins `@latticenet/plugin-bridge` `0.2.0-alpha.1` from the
package registry (GitHub Packages, `ui/.npmrc`); that version resolves once the
bridge release is published, and until then `npm ci` cannot install it.

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
