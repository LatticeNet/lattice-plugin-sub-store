# lattice-plugin-sub-store

Official LatticeNet system plugin. Built from `lattice-plugin-template`.

- **Type:** `system`
- **Publisher:** `latticenet`
- Registered in [lattice-plugin-index](https://github.com/LatticeNet/lattice-plugin-index).

The plugin artifact (`system-go/`) implements the Lattice system-plugin stdio
contract (newline-JSON `{action,payload}` on stdin -> `{ok,plan,message,result,error}`
on stdout). Broker host-call responses are returned on fd 3, advertised by
`LATTICE_HOST_RESPONSE_FD`, so stdin can still close after the initial request.
The Lattice system runner executes it for lifecycle and dashboard calls. See
`manifest.json` for the declared capability set.

## Build

```sh
cd system-go && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -o lattice-plugin-sub-store .
```
