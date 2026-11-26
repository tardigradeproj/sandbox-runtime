# Running sandbox-runtime locally

sandbox-runtime is two separate commands under one binary: the backbone
daemon, and the Backbone Console (the browser UI). They're independent
processes, the console has no on-disk state of its own, and proxies every
RuntimeService/RootFSService/NetworkService call to the daemon over plain
gRPC.

```sh
sandbox-runtime grpc    --config <path>
sandbox-runtime console --listen <addr> --backbone-addr <addr>
```

## The backbone daemon

```sh
sandbox-runtime grpc --config /etc/sandbox-runtime/config.toml
```

See [`.local/config.toml`](../.local/config.toml) for a complete, annotated
config example. `GRPC.ListenAddress` defaults to a unix socket
(`unix:///var/run/sandbox-runtime.sock`); actual sandbox lifecycle
(create/start/stop/checkpoint) requires Linux and, for jailer-enabled
configs, root.

## The console

```sh
sandbox-runtime console --listen 127.0.0.1:9090 \
  --backbone-addr unix:///var/run/sandbox-runtime.sock
```

`--backbone-addr` just needs to match wherever the `grpc` command above is
actually listening. Open `--listen`'s address in a browser
(`http://127.0.0.1:9090/` for the defaults above).

Running `console` without a `grpc` process already serving at
`--backbone-addr` isn't useful on its own — every RuntimeService/
RootFSService/NetworkService call it receives just fails to dial out.
Running `console` more than once against the same `grpc` process, on the
other hand, is fine.

## Building

```sh
make console-build   # npm ci && npm run build, into ui/frontend/dist
make build           # console-build, then go build -o bin/sandbox-runtime ./cmd
```

The built React app is embedded into the Go binary, so `console-build` has
to run (and the binary has to be rebuilt) before a frontend change shows up
— rebuilding only one of the two leaves the other stale.

## Frontend dev loop

For fast UI iteration without rebuilding the Go binary on every change:

```sh
# terminal 1
sandbox-runtime grpc --config <path>

# terminal 2
sandbox-runtime console --listen 127.0.0.1:9090 --backbone-addr <addr>

# terminal 3
cd ui/frontend && npm run dev
```

Vite prints its own port (usually 5173) and proxies Connect calls to
`127.0.0.1:9090` (see `ui/frontend/vite.config.ts`) — open the Vite URL, not
the console's own port directly.

## Running against a remote host

Firecracker/jailer need real Linux, so day-to-day development often means
running the daemon on a remote box and viewing the console from a local
browser. Tunnel the console's port over SSH:

```sh
ssh -N -L 9090:127.0.0.1:9090 <user>@<host>
# then open http://127.0.0.1:9090/
```

## Exec/PTY

The console's Exec tab talks to a sandbox's own `sandboxd` `ProcessService`
directly at its guest IP, neither the backbone daemon nor the console's own
gRPC proxy is in that path. It only works for sandboxes booted from a rootfs
image that bundles `sandboxd` (see `make images-ubuntu-sandboxd-24.04`/
`26.04` in the root `Makefile`).
