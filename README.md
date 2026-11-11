# sandbox-runtime

This project is SRI, the Sandbox Runtime Interface: an interface for
managing the lifecycle of sandboxes, this is platform-agnostic abstraction,
managing the root filesystems they boot from, and provisioning their
networking, deliberately shaped after Kubernetes' CRI (Container
Runtime Interface), down to the OCI images it boots from.

sandbox-runtime also contains **backbone**, SRI's first
implementation: it runs sandboxes as Firecracker microVMs instead of
containers. Each sandbox gets its own kernel, its own network
namespace, and (optionally) its own `jailer` chroot and dropped
privileges, so a compromised workload is contained by real
virtualization rather than by namespace/cgroup isolation alone. Other
implementations can exist behind the same interface without its
callers changing.

The full interface is defined in
[`pkg/api/runtime/v1/api.proto`](pkg/api/runtime/v1/api.proto):

- **`RuntimeService`** creates and starts sandboxes.
- **`RootFSService`** pulls a sandbox's root filesystem from an OCI
  registry and caches it locally, the same model as pulling a
  container image.
- **`NetworkService`** provisions a sandbox's namespace, tap device,
  and NAT path to the internet, independently of how the sandbox
  itself is run. See [`docs/network-design.md`](docs/network-design.md)
  for the design.



## Vision

Creating and starting a sandbox, managing the root filesystem it
boots from, and provisioning its networking is the minimum needed to
run a sandbox at all, it's deliberately where SRI starts, not where
it ends.

A sandbox that's a real microVM rather than a process in namespaces
can offer lifecycle operations a container can't: pause a running
sandbox, snapshot its full state — memory, devices, vCPU — to disk,
and resume it later, on the same host or a different one. Firecracker
already supports exactly this. Bringing it into SRI turns a sandbox's
startup time from "however long a kernel and init take" into "however
long it takes to restore a snapshot", the sandbox equivalent of a
container image's layer cache, but for a sandbox's entire running
state rather than just its filesystem. The same mechanism also opens
the door to live-migrating a sandbox off a draining host instead of
just restarting it elsewhere from scratch.

That's the direction the interface is growing: snapshot and resume
alongside create and start, as first-class, backend-agnostic
operations (not a Firecracker-specific escape hatch) plus whatever
else falls out of treating a sandbox's entire lifecycle, not just its
creation, as the thing SRI manages. None of that exists yet; it's why
the API is versioned `v1` and backbone is an early implementation, not
a finished one.

## Configuration

Configuration is a single TOML file, loaded from
`/etc/sandbox-runtime/config.toml` by default (override with
`--config`). See [`.local/config.toml`](.local/config.toml) for a
complete, annotated example, including the Firecracker/jailer and
network sections.

## Building

```sh
go build ./cmd/...
```

Regenerate the protobuf/gRPC code after editing `api.proto`:

```sh
make proto-tools   # once, to install protoc-gen-go / protoc-gen-go-grpc
make proto
```

## Testing

```sh
make test              # unit tests
make test-integration  # exercises the real mkfs toolchain in a
                        # disposable Linux container (works from any
                        # host OS); tear down with test-integration-down
```

Namespace/bridge networking tests skip automatically outside Linux or
without `CAP_SYS_ADMIN`.

## Sandbox rootfs images

```sh
make images            # build both sample rootfs images locally
make images-busybox    # minimal busybox image
make images-ubuntu     # ubuntu image
make images-ubuntu-push  # build and push a multi-platform (amd64+arm64)
                          # manifest; override REGISTRY_IMAGE/PLATFORMS
                          # as needed
```

## Status

Early-stage and under active development. The `validate-*`
subcommands under `cmd/` are manual testing aids, not part of the
daemon's real interface.
