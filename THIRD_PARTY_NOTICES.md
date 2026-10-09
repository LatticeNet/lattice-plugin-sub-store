# Third-party notices

This plugin is MIT-licensed (see `LICENSE`), except for the component below,
which ships inside the plugin's artifact under its own licence.

## Sub-Store ProxyUtils bundle

- File: `system-go/lib/substore-core.js`, embedded in the plugin binary.
- Project: Sub-Store, https://github.com/sub-store-org/Sub-Store
- Licence: GNU Affero General Public License v3.0 (AGPL-3.0).
- Upstream commit: `48d83214ffe3e1de86a03d80247f2d8202885948`, backend package
  `sub-store` version `2.36.22`.
- Built from `src/core/proxy-utils/index.js` as a minified browser IIFE; the
  build inputs are recorded in `tools/substore-core/pin.json`.
- SHA-256 of the shipped file:
  `994423340ddfbbcb4c858dc497bbbd249aac89b736a03606ada2f8958b1f0d4b`.

The corresponding source is the upstream repository at the commit above,
together with the build tooling in `tools/substore-core/`. The upstream source
is not modified; the build minifies it and injects one core-js polyfill
(`core-js/actual/object/has-own`), as `pin.json` records. The plugin runs the
result inside a sealed QuickJS runtime. Design 28 replaces it with a native Go engine slice by slice
and removes the file in its final slice, at which point this notice is removed
with it.
