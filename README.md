# lattice-plugin-sub-store

Official LatticeNet system plugin. Built from `lattice-plugin-template`.

- **Type:** `system`
- **Publisher:** `latticenet`
- Registered in [lattice-plugin-index](https://github.com/LatticeNet/lattice-plugin-index).

The plugin artifact (`system-go/`) implements the Lattice system-plugin stdio
contract (newline-JSON `{action,payload}` -> `{ok,plan,message,result,error}`);
the Lattice system runner executes it for the verify/plan/health lifecycle. The
heavy engine stays in `lattice-server` (ADR-001 D5/D6: engine in core, providers
are officially-maintained registered plugins). See `manifest.json` for the
declared capability set.

## Build

```sh
cd system-go && CGO_ENABLED=0 go build -trimpath -o lattice-plugin-sub-store .
```
