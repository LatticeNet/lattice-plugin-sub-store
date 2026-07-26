# Sub-Store Engine Spike

This tool measures whether an upstream Sub-Store `ProxyUtils` bundle can run in
embedded Go JavaScript engines without granting the JavaScript runtime sockets
or filesystem access.

The bundle path is explicit so the upstream checkout and pin stay outside this
repository during Phase 1:

```sh
go run ./cmd/spike -bundle /tmp/hephaestus-substore-proxy-utils.iife.js -json
```

Add output-budget sweep cases without changing the default representative set:

```sh
go run ./cmd/spike -bundle /tmp/hephaestus-substore-proxy-utils.iife.js \
  -iterations 3 \
  -sweep-target sing-box \
  -sweep-nodes 1000,2500,4000,4300,4500 \
  -json
```

Build-size probes:

```sh
GOOS=linux GOARCH=amd64 go build -trimpath -buildvcs=false -o /tmp/goja-size ./cmd/goja-size
GOOS=linux GOARCH=amd64 go build -trimpath -buildvcs=false -o /tmp/qjs-size ./cmd/qjs-size
```
