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

## Backend URL safety

The `base_url` supplied by the dashboard must be an absolute URL that includes
the Sub-Store secret path, for example `https://sub.example.com/<secret-path>`.
Remote backends must use `https://`. Cleartext `http://` is accepted only for
`localhost` or loopback hosts such as `127.0.0.1` and `[::1]`. The plugin
rejects URLs with embedded credentials, query strings, fragments, invalid ports,
missing secret paths, remote cleartext HTTP, or path traversal segments before
making a brokered `http.do` call.

The dashboard keeps this secret URL out of server-side configuration. It is
remembered only for the current browser session unless the operator explicitly
chooses to persist it in local browser storage on a trusted device.

## Build

```sh
cd system-go && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -o lattice-plugin-sub-store .
```
