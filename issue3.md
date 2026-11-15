# Implement `CreateSandbox` from a checkpoint

## Context

`CheckpointSandbox` already captures a paused sandbox's VM snapshot, compressed overlay, and `sidecar.json` into `<checkpoint.dir>/<uid>/`. There's no way to actually use one yet -- `CreateSandbox` only ever boots from a rootfs image. This is the missing read path.

## Proto

```proto
message SandboxConfig {
  SandboxMetadata metadata = 1;
  oneof boot_source {
    RootFSMount rootfs = 2;      // today's only path
    string checkpoint_ref = 7;   // from CheckpointSandboxResponse
  }
  BootConfig boot = 3;  // ignored when boot_source is checkpoint_ref
  SandboxResources resources = 4;
  map<string, string> labels = 5;
  map<string, string> annotations = 6;
}
```

Wrapping the existing `rootfs` field into a `oneof` at the same field number is wire-compatible in proto3 -- no existing caller breaks.

## Behavior

- When `checkpoint_ref` is set, `CreateSandbox` reads `<checkpoint.dir>/<checkpoint_ref>/sidecar.json`, decompresses the checkpoint's overlay into the sandbox's own dir (`compress.Decompress`), and points `machineCfg.Snapshot` (`firecracker-go-sdk`'s `SnapshotConfig`) at `mem_file`/`snapshot_file` instead of configuring a fresh kernel/boot-args/drives.
- `boot`/`resources` are ignored for a checkpoint-sourced sandbox -- the VM shape is whatever the checkpoint already captured.
- `NotFound` if `checkpoint_ref` doesn't resolve to a real checkpoint directory.
- `StartSandbox` doesn't change either way -- same `InstanceStart` action regardless of how the machine was configured.
- Gets a new `AttachNetwork` call like any other sandbox (new tap/veth/IP); the checkpoint sidecar's `IPAddress` is historical record only, never reused.

## Out of scope

- Any new RPC -- `CreateSandbox` already exists, this only adds a second `boot_source`.
- Cross-node restore (the checkpoint directory must already be local).
- Deleting a checkpoint after a successful restore.

## Testing

Same fake-firecracker re-exec harness `runtime_linux_test.go` already uses for `CreateSandbox`: seed a fake checkpoint dir (`sidecar.json` + dummy mem/snapshot/overlay files), assert `CreateSandbox` resolves it and configures the snapshot path correctly -- no real `firecracker` binary needed.
