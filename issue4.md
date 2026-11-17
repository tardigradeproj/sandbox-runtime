# Improve guest rootfs images: minimal + Ubuntu 24.04/26.04, systemd and sandboxd-as-PID-1 variants

## Context

`images/Dockerfile.busybox` and `images/Dockerfile.ubuntu` are the only two rootfs images today, and both have real gaps surfaced by actually using them: the busybox image has nothing but `/bin/sh` -- no systemd, no sudo; the ubuntu image installs `systemd`/`sudo`/`openssh-server` but none of it reliably works once `images/init` hands off to `/sbin/init` -- `sandboxd` is already copied into the image but never started, and systemd itself isn't configured for the environment it's actually booting into (no machine-id reset per clone, no provisioning story for a login user at all).

A container-oriented systemd base (e.g. [k0sproject/bootloose](https://github.com/k0sproject/bootloose), which `.local/Dockerfile` already uses for the dev container) was considered and rejected for the guest images specifically: bootloose's whole job is making systemd tolerate running inside a Docker container, which has no real console, no real hardware enumeration, no real kernel of its own -- hence it masks units like `console-getty.service`, `dev-hugepages.mount`, and `sys-fs-fuse-connections.mount`. None of that applies to a Firecracker guest: it has its own real (if minimal) kernel, a real virtual console on `ttyS0` that we specifically want interactive (see the checkpoint-restore validation work), and real devices. Borrowing bootloose's container-specific fixes risks solving problems this project doesn't have while masking the one thing it does want.

## Plan

Five tiers, all sourced from Ubuntu's own official **minimal** cloud image ([cloud-images.ubuntu.com](https://cloud-images.ubuntu.com)'s `minimal/` tree, e.g. `minimal/releases/noble/release/ubuntu-24.04-minimal-cloudimg-amd64-root.tar.xz`) -- not the much larger `server` variant (~1.1GB uncompressed vs ~400MB for minimal; the gap is mostly `cloud-init`'s own Python/babel dependency chain, `snapd`, full `vim`/docs/man pages/locale data the minimal variant doesn't ship by default). Each tier is a rootfs tarball wrapped in a thin Dockerfile that just `ADD`s it into a scratch layer -- `pkg/filesystem/oci`/`PullRootFS` don't need to change, this still looks like a normal OCI image to the puller.

1. **Minimal/raw** (`Dockerfile.minimal`) -- replaces today's busybox image. No init manager of any kind, PID 1 falls straight to `/bin/sh` -- same role busybox played, just real Ubuntu userland. (Ubuntu 26.04's minimal image also moved `pivot_root` out of the base `util-linux` package into `util-linux-extra`, not installed by default -- this tier installs it explicitly, since `images/init` needs it regardless of which tier is booting.)
2. **Ubuntu systemd** (`Dockerfile.ubuntu-systemd`, 24.04 and 26.04) -- systemd kept intact, `sandboxd` wired up as a real enabled unit.
3. **Ubuntu sandboxd-as-PID-1** (`Dockerfile.ubuntu-sandboxd-init`, 24.04 and 26.04) -- no systemd at all; `images/init` execs `sandboxd` directly as PID 1 once it's found at `/usr/local/bin/sandboxd` and `/sbin/init` is gone. This is the same `AGENT_INIT=yes` shape Kata Containers uses for `kata-agent`, and the one `firecracker-containerd`'s own guest agent already follows -- the industry's fast-boot-optimized answer to "I don't need a whole init system just to supervise one daemon." `sandboxd` already has to reap its own children correctly to run as a Pod's entrypoint in its native Kubernetes deployment, so PID 1's zombie-reaping duty is already covered without extra work.

## Behavior

- `images/init` gains one more branch: try `/sbin/init` first (systemd tiers), then `/usr/local/bin/sandboxd` (the sandboxd-as-PID-1 tiers), falling back to `/bin/sh` only if neither exists (the minimal tier). The sandboxd-as-PID-1 Dockerfile removes `/sbin/init` itself so the first branch doesn't win by accident.
- Systemd-tier-only concerns, since none of this applies without an init system driving units in the first place:
  - `/etc/machine-id` (and `/var/lib/dbus/machine-id`) need resetting to empty at image-build time -- every sandbox booted from the same packed image would otherwise share one id, confusing systemd/journald/D-Bus into treating every clone as the same machine.
  - `sandboxd.service` needs the right `After=`/readiness dependency and should be enabled by default, so it's listening on `:9090`/`:8080` as soon as the guest finishes booting, no manual start step.
  - `cloud-init` (ships enabled in the minimal cloud image by default) should be disabled/masked, not used: its datasource-probing stages run on every boot, not just the first, and cost real wall-clock time this project can't afford to spend on something as fast-booting (and, with snapshot-restore, fast-resuming) as a Firecracker microVM.
- The login-user gap (both Ubuntu variants) is solved by baking a fixed, passwordless-but-`NOPASSWD`-sudo user into the image at build time:
  ```dockerfile
  RUN useradd -m -s /bin/bash sandbox \
      && passwd -d sandbox \
      && usermod -aG sudo sandbox \
      && echo 'sandbox ALL=(ALL) NOPASSWD:ALL' > /etc/sudoers.d/sandbox
  ```
  Same model AWS's `ssm-user` uses: the password isn't the security boundary, the channel is -- today that's "whoever controls the daemon/CLI can reach the console at all," and once `sandboxd`'s `ProcessService` is wired up, `Start`/`Execute` fork the requested command directly with no login/PAM/password prompt in that path regardless. `passwd -d` (empty password field) rather than `passwd -l`/locked matters mainly for the console getty -- `sshd`'s default `PermitEmptyPasswords no` would reject this account over SSH either way.

## Out of scope

- Actually wiring `RuntimeService` to talk to `sandboxd` over the network (separate RPC work).
- Non-Ubuntu guest families.
- SSH access for this user (out of the box it won't work over SSH regardless, per the `PermitEmptyPasswords` note above; solving that, if ever needed, is separate).

## Testing

Every tier needs at minimum: `go run ./cmd validate-run-sandbox <image>` boots cleanly with an interactive console, the same manual check already used for checkpoint-restore validation. For the systemd tiers, confirm `systemctl is-active sandboxd` reports active with no manual intervention, the `sandbox` user can `sudo` with no password prompt, and `systemctl is-active cloud-init` (or equivalent) confirms it's masked, not just disabled. For the sandboxd-as-PID-1 tiers, confirm `sandboxd` is actually reachable (e.g. a `ProcessService.Execute` call) and that orphaned child processes don't accumulate as zombies over a few exec calls. Compare boot time across all three shapes (minimal, systemd, sandboxd-as-PID-1) to confirm the systemd-skipping tier actually buys something.
