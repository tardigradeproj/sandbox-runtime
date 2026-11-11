- Expose Firecracker jailer's NUMA node pinning (`JailerConfig.NumaNode`)
  as a config option instead of the hardcoded `firecracker.Int(0)` in
  `internal/backbone/runtime/runtime_linux.go` (`buildJailerConfig`).
- Expose jailer's cgroup version/limits as config instead of the
  hardcoded `CgroupVersion: "2"` in `buildJailerConfig` (leaving it
  unset isn't a safe fallback -- jailer hard-fails looking for a v1
  hierarchy on v2-only hosts) and actual cgroup resource limits, which
  aren't wired up at all yet.
