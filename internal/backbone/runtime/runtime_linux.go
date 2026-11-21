// Copyright 2026 The Tardigrade Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//go:build linux

package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	firecracker "github.com/firecracker-microvm/firecracker-go-sdk"
	"github.com/firecracker-microvm/firecracker-go-sdk/client/models"
	"github.com/sirupsen/logrus"
	"github.com/tardigradeproj/sandbox-runtime/internal/cfg"
	v1 "github.com/tardigradeproj/sandbox-runtime/pkg/api/runtime/v1"
	"github.com/tardigradeproj/sandbox-runtime/pkg/cleanup"
	"github.com/tardigradeproj/sandbox-runtime/pkg/filesystem/compress"
	"github.com/tardigradeproj/sandbox-runtime/pkg/network"
	obs "github.com/tardigradeproj/sandbox-runtime/pkg/observability"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const firecrackerSocketName = "firecracker.sock"

// sandboxStateDir is sandboxID's on-disk state directory: its overlay,
// firecracker socket/log, console log and descriptor all live here,
// nested under a "vm" subfolder so they don't clutter cfg.State
// alongside the runtime's other state (jailer, jailer-ids, network).
func sandboxStateDir(state, sandboxID string) string {
	return filepath.Join(state, "vm", sandboxID)
}

// jailerSocketPath mirrors the SDK's own jail() path computation, so
// StartSandbox can find a jailed sandbox's socket independently.
func jailerSocketPath(fc cfg.Firecracker, sandboxID string) string {
	return jailerRootPath(fc, sandboxID, firecrackerSocketName)
}

// jailerRootPath is the real, host-visible path for name inside
// sandboxID's jailer chroot (its "root/" dir) -- the same computation
// jailerSocketPath uses for the API socket specifically, generalized
// to any file a jailed firecracker process writes relative to its
// own chroot root (e.g. CheckpointSandbox's mem/snapshot files).
func jailerRootPath(fc cfg.Firecracker, sandboxID, name string) string {
	return filepath.Join(fc.Jailer.ChrootBaseDir, filepath.Base(fc.Bin), sandboxID, "root", name)
}

// spawnSandbox registers a cu callback to stop the VMM if anything
// later fails, then runs machine's FcInit handler list -- the same
// work machine.Start does, minus the final InstanceStart action,
// common to both a fresh boot and a snapshot load.
func spawnSandbox(ctx context.Context, log *logrus.Entry, cu *cleanup.Cleanup, machine *firecracker.Machine, socketPath string) error {
	cu.Add(func() {
		if err := machine.StopVMM(); err != nil {
			log.WithError(err).Warn("create sandbox: cleanup: failed to stop vmm")
		}
	})

	log.WithField(obs.SocketPath, socketPath).Debug("create sandbox: spawning firecracker process")
	if err := machine.Handlers.Run(ctx, machine); err != nil {
		log.WithError(err).Error("create sandbox: failed to spawn/configure firecracker")
		return err
	}
	return nil
}

// writeSandboxDescriptorBestEffort writes d, logging rather than
// failing the caller's RPC on error: CheckpointSandbox needs this
// later, but the sandbox itself is already up and running without it.
func writeSandboxDescriptorBestEffort(log *logrus.Entry, sandboxDir string, d sandboxDescriptor) {
	if err := writeSandboxDescriptor(sandboxDir, d); err != nil {
		log.WithError(err).Warn("create sandbox: failed to write sandbox descriptor")
	}
}

// CreateSandbox prepares a sandbox's on-disk state (writable overlay)
// and builds the Firecracker microVM configuration for it, without
// booting the guest. The microVM is actually started by StartSandbox.
func (r *Runtime) CreateSandbox(ctx context.Context, req *v1.CreateSandboxRequest) (*v1.CreateSandboxResponse, error) {
	config := req.GetConfig()

	if checkpointRef := config.GetCheckpointRef(); checkpointRef != "" {
		return r.createSandboxFromCheckpoint(ctx, req, checkpointRef)
	}

	sandboxID := config.GetMetadata().GetUid()
	if sandboxID == "" {
		return nil, status.Error(codes.InvalidArgument, "config.metadata.uid must be set")
	}
	log := logrus.WithField(obs.SandboxID, sandboxID)

	storageBytes := config.GetResources().GetStorageBytes()
	if storageBytes <= 0 {
		return nil, status.Error(codes.InvalidArgument, "config.resources.storage_bytes must be set")
	}
	memoryBytes := config.GetResources().GetMemoryBytes()
	if memoryBytes <= 0 {
		return nil, status.Error(codes.InvalidArgument, "config.resources.memory_bytes must be set")
	}

	mount := config.GetRootfs()
	image := mount.GetSpec().GetImage()
	if image == "" {
		return nil, status.Error(codes.InvalidArgument, "config.rootfs.spec.image must be set")
	}

	userData := config.GetUserData()
	if userData != nil && !r.cfg.Runtime.Firecracker.Jailer.Enabled {
		return nil, status.Error(codes.FailedPrecondition, "config.user_data requires jailer to be enabled")
	}

	log.WithField(obs.SandboxImage, image).Info("creating sandbox")

	entry, ok, err := r.store.Resolve(image)
	if err != nil {
		log.WithError(err).Error("create sandbox: failed to resolve rootfs")
		return nil, status.Errorf(codes.Internal, "create sandbox: %v", err)
	}
	if !ok {
		return nil, status.Errorf(codes.NotFound, "rootfs %q not found", image)
	}
	rootfsPath, err := r.store.ImagePath(entry.Digest)
	if err != nil {
		log.WithError(err).Error("create sandbox: failed to resolve rootfs image path")
		return nil, status.Errorf(codes.Internal, "create sandbox: %v", err)
	}

	sandboxDir := sandboxStateDir(r.cfg.State, sandboxID)
	if err := os.MkdirAll(sandboxDir, 0o755); err != nil {
		log.WithError(err).Error("create sandbox: failed to create sandbox dir")
		return nil, status.Errorf(codes.Internal, "create sandbox: %v", err)
	}
	cu := cleanup.Make(func() {
		if err := os.RemoveAll(sandboxDir); err != nil {
			log.WithError(err).Warn("create sandbox: cleanup: failed to remove sandbox dir")
		}
	})
	defer cu.Clean()

	overlayPath := filepath.Join(sandboxDir, "overlay.ext4")
	if err := createOverlayImage(ctx, overlayPath, storageBytes); err != nil {
		log.WithError(err).Error("create sandbox: failed to create overlay image")
		return nil, status.Errorf(codes.Internal, "create sandbox: %v", err)
	}
	log.WithField(obs.OverlayPath, overlayPath).Debug("create sandbox: overlay created")

	fc := r.cfg.Runtime.Firecracker

	kernelImagePath := config.GetBoot().GetKernelImagePath()
	if kernelImagePath == "" {
		kernelImagePath = fc.KernelImagePath
	}
	bootArgs := config.GetBoot().GetBootArgs()
	if bootArgs == "" {
		bootArgs = fc.DefaultBootArgs
	}

	driveOpts := []firecracker.DriveOpt{firecracker.WithReadOnly(mount.GetIsReadOnly())}
	if partuuid := mount.GetPartuuid(); partuuid != "" {
		driveOpts = append(driveOpts, firecracker.WithPartuuid(partuuid))
	}
	drives := firecracker.DrivesBuilder{}.
		WithRootDrive(rootfsPath, driveOpts...).
		AddDrive(overlayPath, false).
		Build()

	vcpuCount := int64(math.Ceil(config.GetResources().GetCpu()))
	if vcpuCount < 1 {
		vcpuCount = 1
	}
	memSizeMib := memoryBytes / (1024 * 1024)
	if memSizeMib < 1 {
		memSizeMib = 1
	}

	socketPath := filepath.Join(sandboxDir, firecrackerSocketName)
	logPath := filepath.Join(sandboxDir, "firecracker.log")

	machineCfg := firecracker.Config{
		VMID:            sandboxID,
		SocketPath:      socketPath,
		LogLevel:        fc.Log.Level,
		KernelImagePath: kernelImagePath,
		KernelArgs:      bootArgs,
		InitrdPath:      config.GetBoot().GetInitrdPath(),
		Drives:          drives,
		MachineCfg: models.MachineConfiguration{
			VcpuCount:  firecracker.Int64(vcpuCount),
			MemSizeMib: firecracker.Int64(memSizeMib),
		},
	}

	var machine *firecracker.Machine
	var jailerUID, jailerGID int
	if fc.Jailer.Enabled {
		uid, gid, realSocketPath, consoleLog, err := r.jailSandbox(fc, sandboxID, sandboxDir, overlayPath, kernelImagePath, &machineCfg, &cu)
		if err != nil {
			log.WithError(err).Error("create sandbox: failed to jail sandbox")
			return nil, status.Errorf(codes.Internal, "create sandbox: %v", err)
		}
		defer consoleLog.Close()
		socketPath = realSocketPath
		jailerUID, jailerGID = uid, gid

		guest := network.GuestNetwork(r.cfg.Network.GuestNetwork)
		machineCfg.KernelArgs = strings.TrimSpace(machineCfg.KernelArgs + " " + network.GuestIPKernelParam(guest))

		// LogFifo (not LogPath) gets hard-linked into the chroot by the SDK.
		logPath = filepath.Join(sandboxDir, "firecracker.fifo")
		machineCfg.LogFifo = logPath

		machine, err = firecracker.NewMachine(ctx, machineCfg)
		if err != nil {
			log.WithError(err).Error("create sandbox: failed to build firecracker machine config")
			return nil, status.Errorf(codes.Internal, "create sandbox: %v", err)
		}
	} else {
		machineCfg.LogPath = logPath

		consoleLog, err := openConsoleLog(sandboxDir)
		if err != nil {
			log.WithError(err).Error("create sandbox: failed to open console log")
			return nil, status.Errorf(codes.Internal, "create sandbox: %v", err)
		}
		defer consoleLog.Close()

		// Stdin omitted: the SDK defaults a nil Stdin to /dev/null.
		cmd := firecracker.VMCommandBuilder{}.
			WithBin(fc.Bin).
			WithSocketPath(socketPath).
			WithStdout(consoleLog).
			WithStderr(consoleLog).
			AddArgs("--id", sandboxID).
			Build(ctx)

		machine, err = firecracker.NewMachine(ctx, machineCfg, firecracker.WithProcessRunner(cmd))
		if err != nil {
			log.WithError(err).Error("create sandbox: failed to build firecracker machine config")
			return nil, status.Errorf(codes.Internal, "create sandbox: %v", err)
		}
	}
	if err := spawnSandbox(ctx, log, &cu, machine, socketPath); err != nil {
		return nil, status.Errorf(codes.Internal, "create sandbox: %v", err)
	}

	if userData != nil {
		if err := machine.SetMetadata(ctx, sandboxUserDataMetadata(userData)); err != nil {
			log.WithError(err).Error("create sandbox: failed to set mmds user data")
			return nil, status.Errorf(codes.Internal, "create sandbox: %v", err)
		}
	}
	cu.Release()

	writeSandboxDescriptorBestEffort(log, sandboxDir, sandboxDescriptor{
		Config: config,
		UID:    jailerUID,
		GID:    jailerGID,
	})

	log.WithFields(logrus.Fields{
		obs.RootfsPath:  rootfsPath,
		obs.OverlayPath: overlayPath,
		obs.SocketPath:  socketPath,
		obs.LogPath:     logPath,
	}).Info("sandbox created")
	return &v1.CreateSandboxResponse{SandboxId: sandboxID}, nil
}

// createSandboxFromCheckpoint resumes a sandbox from a prior
// CheckpointSandbox call under the caller's own sandbox_id, jailer
// only: a jailed snapshot records its drive at a chroot-relative
// path, which any sandbox's own chroot satisfies, so one checkpoint
// can seed many concurrent sandboxes. Non-jailed records the drive's
// real host path instead, which two sandboxes can't both own at once.
func (r *Runtime) createSandboxFromCheckpoint(ctx context.Context, req *v1.CreateSandboxRequest, checkpointRef string) (*v1.CreateSandboxResponse, error) {
	sandboxID := req.GetConfig().GetMetadata().GetUid()
	if sandboxID == "" {
		return nil, status.Error(codes.InvalidArgument, "config.metadata.uid must be set")
	}
	log := logrus.WithField(obs.SandboxID, sandboxID)
	log.Info("creating sandbox from checkpoint")

	fc := r.cfg.Runtime.Firecracker
	if !fc.Jailer.Enabled {
		return nil, status.Error(codes.FailedPrecondition, "create sandbox from checkpoint requires jailer")
	}

	dir := checkpointDir(r.cfg.Checkpoint.Dir, checkpointRef)
	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			return nil, status.Errorf(codes.NotFound, "checkpoint %q not found", checkpointRef)
		}
		return nil, status.Errorf(codes.Internal, "create sandbox: %v", err)
	}

	sandboxDir := sandboxStateDir(r.cfg.State, sandboxID)
	if err := os.Mkdir(sandboxDir, 0o755); err != nil {
		if os.IsExist(err) {
			return nil, status.Errorf(codes.AlreadyExists, "sandbox %q already exists", sandboxID)
		}
		return nil, status.Errorf(codes.Internal, "create sandbox: %v", err)
	}
	cu := cleanup.Make(func() {
		if err := os.RemoveAll(sandboxDir); err != nil {
			log.WithError(err).Warn("create sandbox: cleanup: failed to remove sandbox dir")
		}
	})
	defer cu.Clean()

	// Firecracker's snapshot device state has two drives recorded,
	// not just the overlay: a sandbox also boots with a root rootfs
	// image attached (see CreateSandbox's WithRootDrive), hard-linked
	// into its chroot at snapshot time the same way the overlay is --
	// so restoring needs to put it back too, under the same basename.
	sidecar, err := readSidecar(dir)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "create sandbox: %v", err)
	}
	image := sidecar.Config.GetRootfs().GetSpec().GetImage()
	entry, ok, err := r.store.Resolve(image)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "create sandbox: %v", err)
	}
	if !ok {
		return nil, status.Errorf(codes.NotFound, "rootfs %q not found", image)
	}
	rootfsPath, err := r.store.ImagePath(entry.Digest)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "create sandbox: %v", err)
	}

	overlayFile, comp, err := findOverlay(dir)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "create sandbox: %v", err)
	}
	overlayPath := filepath.Join(sandboxDir, "overlay.ext4")
	if err := comp.Decompress(ctx, overlayFile, overlayPath); err != nil {
		return nil, status.Errorf(codes.Internal, "create sandbox: failed to decompress overlay: %v", err)
	}

	memFilePath := filepath.Join(dir, checkpointMemFile)
	snapshotPath := filepath.Join(dir, checkpointSnapshotFile)
	logPath := filepath.Join(sandboxDir, "firecracker.fifo")

	machineCfg := firecracker.Config{
		VMID:     sandboxID,
		LogLevel: fc.Log.Level,
		LogFifo:  logPath,
	}
	uid, gid, socketPath, consoleLog, err := r.jailSandbox(fc, sandboxID, sandboxDir, overlayPath, "", &machineCfg, &cu)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "create sandbox: %v", err)
	}
	defer consoleLog.Close()

	machine, err := firecracker.NewMachine(ctx, machineCfg, firecracker.WithSnapshot(memFilePath, snapshotPath))
	if err != nil {
		return nil, status.Errorf(codes.Internal, "create sandbox: failed to build firecracker machine config: %v", err)
	}
	// WithSnapshot replaces the jailed FcInit list wholesale, which
	// drops NaiveChrootStrategy's own LinkFilesHandler -- so the
	// rootfs/overlay/mem/snapshot files need their own hard-link step
	// back in, at the same point in the list LinkFilesHandler would run.
	machine.Handlers.FcInit = machine.Handlers.FcInit.AppendAfter(
		firecracker.CreateLogFilesHandlerName,
		linkCheckpointFilesHandler(fc, sandboxID, uid, gid, rootfsPath, overlayPath, memFilePath, snapshotPath),
	)

	if err := spawnSandbox(ctx, log, &cu, machine, socketPath); err != nil {
		return nil, status.Errorf(codes.Internal, "create sandbox: %v", err)
	}
	cu.Release()

	writeSandboxDescriptorBestEffort(log, sandboxDir, sandboxDescriptor{
		Config: req.GetConfig(),
		UID:    uid,
		GID:    gid,
	})

	log.WithFields(logrus.Fields{
		obs.OverlayPath: overlayPath,
		obs.SocketPath:  socketPath,
		obs.LogPath:     logPath,
	}).Info("sandbox created from checkpoint")
	return &v1.CreateSandboxResponse{SandboxId: sandboxID}, nil
}

// linkCheckpointFilesHandler reproduces NaiveChrootStrategy's own
// LinkFilesHandler, which firecracker.WithSnapshot's handler list
// drops: without this, nothing puts the rootfs/overlay/mem/snapshot/
// fifo files into sandboxID's chroot before Firecracker -- now
// running inside it -- tries to open them. rootfsPath is read-only
// and content-addressed, so unlike the others it isn't chowned.
func linkCheckpointFilesHandler(fc cfg.Firecracker, sandboxID string, uid, gid int, rootfsPath, overlayPath, memFilePath, snapshotPath string) firecracker.Handler {
	return firecracker.Handler{
		Name: "checkpoint.LinkFiles",
		Fn: func(_ context.Context, m *firecracker.Machine) error {
			root := filepath.Join(fc.Jailer.ChrootBaseDir, filepath.Base(fc.Bin), sandboxID, "root")
			if err := os.Link(rootfsPath, filepath.Join(root, filepath.Base(rootfsPath))); err != nil {
				return fmt.Errorf("link rootfs: %w", err)
			}
			if err := os.Link(overlayPath, filepath.Join(root, "overlay.ext4")); err != nil {
				return fmt.Errorf("link overlay: %w", err)
			}
			if err := os.Chown(memFilePath, uid, gid); err != nil {
				return fmt.Errorf("chown mem file: %w", err)
			}
			if err := os.Link(memFilePath, filepath.Join(root, checkpointMemFile)); err != nil {
				return fmt.Errorf("link mem file: %w", err)
			}
			if err := os.Chown(snapshotPath, uid, gid); err != nil {
				return fmt.Errorf("chown snapshot file: %w", err)
			}
			if err := os.Link(snapshotPath, filepath.Join(root, checkpointSnapshotFile)); err != nil {
				return fmt.Errorf("link snapshot file: %w", err)
			}
			m.Cfg.Snapshot.MemFilePath = checkpointMemFile
			m.Cfg.Snapshot.SnapshotPath = checkpointSnapshotFile

			for _, fifoPath := range []*string{&m.Cfg.LogFifo, &m.Cfg.MetricsFifo} {
				if *fifoPath == "" {
					continue
				}
				name := filepath.Base(*fifoPath)
				dst := filepath.Join(root, name)
				if err := os.Link(*fifoPath, dst); err != nil {
					return fmt.Errorf("link fifo %s: %w", name, err)
				}
				if err := os.Chown(dst, uid, gid); err != nil {
					return fmt.Errorf("chown fifo %s: %w", name, err)
				}
				*fifoPath = name
			}
			return nil
		},
	}
}

// jailSandbox resolves sandboxID's jailer uid/gid, grants it access
// to the overlay and tap device, registers cu release/cleanup
// callbacks, and fills in machineCfg's jailer fields. kernelImagePath
// may be "" when there's no kernel to boot (a checkpoint restore).
// The caller must close the returned console log after spawnSandbox.
func (r *Runtime) jailSandbox(fc cfg.Firecracker, sandboxID, sandboxDir, overlayPath, kernelImagePath string, machineCfg *firecracker.Config, cu *cleanup.Cleanup) (uid, gid int, socketPath string, consoleLog *os.File, err error) {
	uid, gid, err = r.resolveJailerUIDGID(fc.Jailer, sandboxID)
	if err != nil {
		return 0, 0, "", nil, fmt.Errorf("obtain jailer uid/gid: %w", err)
	}

	// Hard-linked into the chroot as-is, so the jailed uid/gid needs
	// write access to the shared inode.
	if err := os.Chown(overlayPath, uid, gid); err != nil {
		return 0, 0, "", nil, fmt.Errorf("chown overlay image for jailer: %w", err)
	}

	// The tap device (created earlier by AttachNetwork) is owned by
	// root; without CAP_NET_ADMIN, the jailed uid can only open it if
	// it's also the device's recorded owner.
	if err := network.SetTapOwner(sandboxID, uid, gid); err != nil {
		return 0, 0, "", nil, fmt.Errorf("chown tap device for jailer: %w", err)
	}

	consoleLog, err = openConsoleLog(sandboxDir)
	if err != nil {
		return 0, 0, "", nil, fmt.Errorf("open console log: %w", err)
	}

	if fc.Jailer.UIDGID.Mode == "on_demand" {
		cu.Add(func() {
			if err := r.jailerIDs.Release(uid); err != nil {
				logrus.WithField(obs.SandboxID, sandboxID).WithError(err).Warn("create sandbox: cleanup: failed to release jailer id")
			}
		})
	}
	cu.Add(func() {
		chrootDir := filepath.Join(fc.Jailer.ChrootBaseDir, filepath.Base(fc.Bin), sandboxID)
		if err := os.RemoveAll(chrootDir); err != nil {
			logrus.WithField(obs.SandboxID, sandboxID).WithError(err).Warn("create sandbox: cleanup: failed to remove jailer chroot dir")
		}
	})

	// Namespace + tap device are provisioned independently, by
	// NetworkService (see pkg/network)
	machineCfg.NetNS = network.NetnsPath(sandboxID)
	machineCfg.NetworkInterfaces = firecracker.NetworkInterfaces{{
		StaticConfiguration: &firecracker.StaticNetworkConfiguration{
			HostDevName: network.TapDeviceName,
		},
		AllowMMDS: true,
	}}
	machineCfg.MmdsVersion = firecracker.MMDSv2
	machineCfg.JailerCfg = buildJailerConfig(fc, uid, gid, sandboxID, kernelImagePath, consoleLog)

	// Chroot-relative: the SDK's jail() rewrites this to the real
	// host path (see jailerSocketPath).
	machineCfg.SocketPath = firecrackerSocketName
	return uid, gid, jailerSocketPath(fc, sandboxID), consoleLog, nil
}

// resolveJailerUIDGID returns the uid/gid to drop jailer's privileges
// to for sandboxID, per jc.UIDGID.Mode.
func (r *Runtime) resolveJailerUIDGID(jc cfg.Jailer, sandboxID string) (uid, gid int, err error) {
	switch jc.UIDGID.Mode {
	case "precreated":
		return *jc.UIDGID.StaticUID, *jc.UIDGID.StaticGID, nil
	case "on_demand":
		id, err := r.jailerIDs.Allocate(sandboxID)
		if err != nil {
			return 0, 0, fmt.Errorf("allocate jailer id: %w", err)
		}
		return id, id, nil
	default:
		return 0, 0, fmt.Errorf("unknown jailer uid_gid mode %q", jc.UIDGID.Mode)
	}
}

// buildJailerConfig maps jailer config into the SDK's JailerConfig.
// NumaNode and CgroupVersion are hardcoded, not configurable yet (see
// TODO.md). Stdin is left nil, which the SDK defaults to /dev/null.
func buildJailerConfig(fc cfg.Firecracker, uid, gid int, sandboxID, kernelImagePath string, consoleLog *os.File) *firecracker.JailerConfig {
	return &firecracker.JailerConfig{
		ID:             sandboxID,
		UID:            firecracker.Int(uid),
		GID:            firecracker.Int(gid),
		NumaNode:       firecracker.Int(0),
		ExecFile:       fc.Bin,
		JailerBinary:   fc.Jailer.BinPath,
		ChrootBaseDir:  fc.Jailer.ChrootBaseDir,
		ChrootStrategy: firecracker.NewNaiveChrootStrategy(kernelImagePath),
		CgroupVersion:  "2",
		Stdout:         consoleLog,
		Stderr:         consoleLog,
	}
}

// openConsoleLog opens sandboxDir's console.log, truncating any stale content.
func openConsoleLog(sandboxDir string) (*os.File, error) {
	return os.OpenFile(filepath.Join(sandboxDir, "console.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
}

// StartSandbox boots a sandbox that CreateSandbox already spawned and
// configured, by sending the firecracker process the InstanceStart
// action over its API socket. It reconnects statelessly via the
// socket path rather than reusing anything kept in memory from
// CreateSandbox.
func (r *Runtime) StartSandbox(ctx context.Context, req *v1.StartSandboxRequest) (*v1.StartSandboxResponse, error) {
	sandboxID := req.GetSandboxId()
	if sandboxID == "" {
		return nil, status.Error(codes.InvalidArgument, "sandbox_id must be set")
	}
	log := logrus.WithField(obs.SandboxID, sandboxID)
	log.Info("starting sandbox")

	fc := r.cfg.Runtime.Firecracker
	sandboxDir := sandboxStateDir(r.cfg.State, sandboxID)
	socketPath := filepath.Join(sandboxDir, firecrackerSocketName)
	if fc.Jailer.Enabled {
		socketPath = jailerSocketPath(fc, sandboxID)
	}
	if _, err := os.Stat(socketPath); err != nil {
		if os.IsNotExist(err) {
			return nil, status.Errorf(codes.NotFound, "sandbox %q not found", sandboxID)
		}
		log.WithError(err).Error("start sandbox: failed to stat socket")
		return nil, status.Errorf(codes.Internal, "start sandbox: %v", err)
	}

	// A checkpoint-restored sandbox was already loaded (not booted) by
	// CreateSandbox; starting it means resuming, not InstanceStart
	desc, _ := readSandboxDescriptor(sandboxDir)
	fromCheckpoint := desc.Config.GetCheckpointRef() != ""

	client := firecracker.NewClient(socketPath, logrus.NewEntry(logrus.StandardLogger()), false)
	if fromCheckpoint {
		vm := &models.VM{State: firecracker.String(models.VMStateResumed)}
		if _, err := client.PatchVM(ctx, vm); err != nil {
			log.WithError(err).Error("start sandbox: failed to resume vm")
			return nil, status.Errorf(codes.Internal, "start sandbox: %v", err)
		}
	} else {
		action := models.InstanceActionInfoActionTypeInstanceStart
		if _, err := client.CreateSyncAction(ctx, &models.InstanceActionInfo{ActionType: &action}); err != nil {
			log.WithError(err).Error("start sandbox: failed to start instance")
			return nil, status.Errorf(codes.Internal, "start sandbox: %v", err)
		}
	}

	log.Info("sandbox started")
	return &v1.StartSandboxResponse{}, nil
}

// sandboxUserDataMetadata converts userData into the map the guest's
// metadata endpoint serves
func sandboxUserDataMetadata(userData *v1.SandboxUserData) map[string]any {
	metadata := map[string]any{}
	if s := userData.GetBoot(); s != "" {
		metadata["boot"] = s
	}
	if m := userData.GetSystem(); m != nil {
		metadata["system"] = m.AsMap()
	}
	return metadata
}

// UpdateSandboxUserData replaces a running sandbox's user data over
// its API socket, the same reconnect-by-socket pattern as
// StartSandbox/StopSandbox -- the guest's next read of its metadata
// endpoint reflects it immediately, no restart required.
func (r *Runtime) UpdateSandboxUserData(ctx context.Context, req *v1.UpdateSandboxUserDataRequest) (*v1.UpdateSandboxUserDataResponse, error) {
	sandboxID := req.GetSandboxId()
	if sandboxID == "" {
		return nil, status.Error(codes.InvalidArgument, "sandbox_id must be set")
	}
	log := logrus.WithField(obs.SandboxID, sandboxID)
	log.Info("updating sandbox user data")

	fc := r.cfg.Runtime.Firecracker
	if !fc.Jailer.Enabled {
		return nil, status.Error(codes.FailedPrecondition, "update sandbox user data requires jailer to be enabled")
	}
	socketPath := jailerSocketPath(fc, sandboxID)
	if _, err := os.Stat(socketPath); err != nil {
		if os.IsNotExist(err) {
			return nil, status.Errorf(codes.NotFound, "sandbox %q not found", sandboxID)
		}
		log.WithError(err).Error("update sandbox user data: failed to stat socket")
		return nil, status.Errorf(codes.Internal, "update sandbox user data: %v", err)
	}

	client := firecracker.NewClient(socketPath, logrus.NewEntry(logrus.StandardLogger()), false)
	if _, err := client.PutMmds(ctx, sandboxUserDataMetadata(req.GetUserData())); err != nil {
		log.WithError(err).Error("update sandbox user data: failed to set user data")
		return nil, status.Errorf(codes.Internal, "update sandbox user data: %v", err)
	}

	log.Info("sandbox user data updated")
	return &v1.UpdateSandboxUserDataResponse{}, nil
}

// ListSandbox considers that jailer is always enabled
func (r *Runtime) ListSandbox(ctx context.Context, req *v1.ListSandboxRequest) (*v1.ListSandboxResponse, error) {
	filter := req.GetFilter()

	var sandboxList []*v1.SandboxTrait
	log := logrus.WithField(obs.Operation, obs.OperationListSandbox)
	log.Info("listing sandboxes")
	// user wants to list all sandboxes
	fc := r.cfg.Runtime.Firecracker
	jailerRoot := filepath.Join(fc.Jailer.ChrootBaseDir, filepath.Base(fc.Bin))
	entries, err := os.ReadDir(jailerRoot)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list sandbox: failed to read sandboxes: %v", err)
	}
	for _, entry := range entries {
		sandboxID := entry.Name()
		if entry.IsDir() {
			sandboxDescriptor, err := readSandboxDescriptor(sandboxStateDir(r.cfg.State, sandboxID))
			if err != nil {
				log.WithField(obs.SandboxID, sandboxID).
					WithError(err).
					Error("failed to read sandbox descriptor. Sandboxes should always have descriptor - backbone is in an inconsistent state")
				continue
			}
			ipAddress, _, err := network.IPAddress(sandboxID)
			if err != nil {
				log.WithField(obs.SandboxID, sandboxID).
					WithError(err).
					Error("checkpoint sandbox: failed to look up sandbox ip")
			}
			socketPath := jailerSocketPath(fc, sandboxID)
			client := firecracker.NewClient(socketPath, logrus.NewEntry(logrus.StandardLogger()), false)
			info, err := client.GetInstanceInfo(ctx)
			if err != nil {
				log.WithError(err).Error("checkpoint sandbox: failed to get instance info")
				continue
			}
			// started Running Paused
			state := info.GetPayload().State
			var microvmState v1.SandboxStateValue
			switch *state {
			case models.InstanceInfoStatePaused:
				microvmState = v1.SandboxStateValue_STATE_STOPPED
			case models.InstanceInfoStateRunning:
				microvmState = v1.SandboxStateValue_STATE_RUNNING
			case models.InstanceInfoStateNotStarted:
				microvmState = v1.SandboxStateValue_STATE_PENDING
			default:
				microvmState = v1.SandboxStateValue_STATE_UNSPECIFIED
			}

			sandbox := &v1.SandboxTrait{
				Config: sandboxDescriptor.Config,
				User: &v1.UserGroup{
					Uid: uint64(sandboxDescriptor.UID),
					Gid: uint64(sandboxDescriptor.GID),
				},
				Network: &v1.SandboxNetwork{
					IpAddress: ipAddress,
				},
				State: microvmState,
			}
			sandboxList = append(sandboxList, sandbox)
		}
	}
	filteredList := make([]*v1.SandboxTrait, 0, len(sandboxList))
	for _, sandbox := range sandboxList {
		if matchesFilter(sandbox, filter) {
			filteredList = append(filteredList, sandbox)
		}
	}
	return &v1.ListSandboxResponse{Sandbox: filteredList}, nil
}
func matchesFilter(sandbox *v1.SandboxTrait, filter *v1.SandboxFilter) bool {
	if filter.Uid != "" && sandbox.Config.Metadata.Uid != filter.Uid {
		return false
	}

	if filter.State != v1.SandboxStateValue_STATE_UNSPECIFIED &&
		sandbox.State != filter.State {
		return false
	}

	for k, v := range filter.LabelSelector {
		if sandbox.Config.Labels[k] != v {
			return false
		}
	}

	return true
}

// StopSandbox pauses a running sandbox's vCPUs over its API socket,
// the same reconnect-by-socket pattern as StartSandbox.
func (r *Runtime) StopSandbox(ctx context.Context, req *v1.StopSandboxRequest) (*v1.StopSandboxResponse, error) {
	sandboxID := req.GetSandboxId()
	if sandboxID == "" {
		return nil, status.Error(codes.InvalidArgument, "sandbox_id must be set")
	}
	log := logrus.WithField(obs.SandboxID, sandboxID)
	log.Info("stopping sandbox")

	fc := r.cfg.Runtime.Firecracker
	socketPath := filepath.Join(r.cfg.State, sandboxID, firecrackerSocketName)
	if fc.Jailer.Enabled {
		socketPath = jailerSocketPath(fc, sandboxID)
	}
	if _, err := os.Stat(socketPath); err != nil {
		if os.IsNotExist(err) {
			return nil, status.Errorf(codes.NotFound, "sandbox %q not found", sandboxID)
		}
		log.WithError(err).Error("stop sandbox: failed to stat socket")
		return nil, status.Errorf(codes.Internal, "stop sandbox: %v", err)
	}

	client := firecracker.NewClient(socketPath, logrus.NewEntry(logrus.StandardLogger()), false)
	vm := &models.VM{State: firecracker.String(models.VMStatePaused)}
	if _, err := client.PatchVM(ctx, vm); err != nil {
		log.WithError(err).Error("stop sandbox: failed to pause vm")
		return nil, status.Errorf(codes.Internal, "stop sandbox: %v", err)
	}

	log.Info("sandbox stopped")
	return &v1.StopSandboxResponse{}, nil
}

// RemoveSandbox asks sandboxID's guest to shut down gracefully
// (SendCtrlAltDel over its firecracker API socket), waits up to
// req.TimeoutSeconds for its firecracker/jailer process to exit on
// its own, and releases what CreateSandbox allocated for it, except
// its network namespace/tap device.
func (r *Runtime) RemoveSandbox(ctx context.Context, req *v1.RemoveSandboxRequest) (*v1.RemoveSandboxResponse, error) {
	sandboxID := req.GetSandboxId()
	if sandboxID == "" {
		return nil, status.Error(codes.InvalidArgument, "sandbox_id must be set")
	}
	log := logrus.WithField(obs.SandboxID, sandboxID)
	log.Info("removing sandbox")

	sandboxDir := sandboxStateDir(r.cfg.State, sandboxID)
	if _, err := os.Stat(sandboxDir); err != nil {
		if os.IsNotExist(err) {
			return nil, status.Errorf(codes.NotFound, "sandbox %q not found", sandboxID)
		}
		log.WithError(err).Error("remove sandbox: failed to stat sandbox dir")
		return nil, status.Errorf(codes.Internal, "remove sandbox: %v", err)
	}

	fc := r.cfg.Runtime.Firecracker
	socketPath := filepath.Join(sandboxDir, firecrackerSocketName)
	if fc.Jailer.Enabled {
		socketPath = jailerSocketPath(fc, sandboxID)
	}
	if _, err := os.Stat(socketPath); err == nil {
		client := firecracker.NewClient(socketPath, logrus.NewEntry(logrus.StandardLogger()), false)
		if _, err := client.CreateSyncAction(ctx, &models.InstanceActionInfo{ActionType: new(models.InstanceActionInfoActionTypeSendCtrlAltDel)}); err != nil {
			log.WithError(err).Warn("remove sandbox: failed to send ctrl-alt-del, falling back to timeout/force")
		}
	}

	pids, err := sandboxProcessPids(sandboxID)
	if err != nil {
		log.WithError(err).Error("remove sandbox: failed to list sandbox processes")
		return nil, status.Errorf(codes.Internal, "remove sandbox: %v", err)
	}
	deadline := time.Now().Add(time.Duration(req.GetTimeoutSeconds()) * time.Second)
	for len(pids) > 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		if pids, err = sandboxProcessPids(sandboxID); err != nil {
			log.WithError(err).Error("remove sandbox: failed to list sandbox processes")
			return nil, status.Errorf(codes.Internal, "remove sandbox: %v", err)
		}
	}
	if len(pids) > 0 {
		if !req.GetForce() {
			return nil, status.Errorf(codes.DeadlineExceeded,
				"remove sandbox: still running after %ds, retry with force to kill it", req.GetTimeoutSeconds())
		}
		if err := killPids(pids); err != nil {
			log.WithError(err).Error("remove sandbox: failed to kill process")
			return nil, status.Errorf(codes.Internal, "remove sandbox: %v", err)
		}
	}

	if fc.Jailer.Enabled {
		chrootDir := filepath.Join(fc.Jailer.ChrootBaseDir, filepath.Base(fc.Bin), sandboxID)
		if err := os.RemoveAll(chrootDir); err != nil {
			log.WithError(err).Error("remove sandbox: failed to remove jailer chroot dir")
			return nil, status.Errorf(codes.Internal, "remove sandbox: %v", err)
		}
		if fc.Jailer.UIDGID.Mode == "on_demand" {
			if desc, err := readSandboxDescriptor(sandboxDir); err != nil {
				log.WithError(err).Warn("remove sandbox: failed to read descriptor, skipping jailer id release")
			} else if err := r.jailerIDs.Release(desc.UID); err != nil {
				log.WithError(err).Warn("remove sandbox: failed to release jailer id")
			}
		}
	}

	if err := os.RemoveAll(sandboxDir); err != nil {
		log.WithError(err).Error("remove sandbox: failed to remove sandbox dir")
		return nil, status.Errorf(codes.Internal, "remove sandbox: %v", err)
	}

	log.Info("sandbox removed")
	return &v1.RemoveSandboxResponse{}, nil
}

// sandboxProcessPids finds every running process (firecracker, and
// jailer if enabled) whose command line carries "--id sandboxID", via
// /proc rather than any in-memory state, since RuntimeService RPCs
// reconnect statelessly.
func sandboxProcessPids(sandboxID string) ([]int, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, fmt.Errorf("list /proc: %w", err)
	}
	var pids []int
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		cmdline, err := os.ReadFile(filepath.Join("/proc", e.Name(), "cmdline"))
		if err != nil {
			continue
		}
		args := strings.Split(strings.TrimRight(string(cmdline), "\x00"), "\x00")
		for i, a := range args {
			if a == "--id" && i+1 < len(args) && args[i+1] == sandboxID {
				pids = append(pids, pid)
				break
			}
		}
	}
	return pids, nil
}

// killPids SIGKILLs every pid, ignoring ones that are already gone.
func killPids(pids []int) error {
	for _, pid := range pids {
		if err := syscall.Kill(pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
			return fmt.Errorf("kill pid %d: %w", pid, err)
		}
	}
	return nil
}

// CheckpointSandbox captures a stopped sandbox's VM state (via
// Firecracker's own CreateSnapshot) and a compressed copy of its
// overlay into cfg.Checkpoint.Dir, alongside a Sidecar describing
// where it came from. Fails if the sandbox is still running, or if
// uid is already in use.
func (r *Runtime) CheckpointSandbox(ctx context.Context, req *v1.CheckpointSandboxRequest) (*v1.CheckpointSandboxResponse, error) {
	sandboxID := req.GetSandboxId()
	if sandboxID == "" {
		return nil, status.Error(codes.InvalidArgument, "sandbox_id must be set")
	}
	uid := req.GetUid()
	if uid == "" {
		uid = sandboxID
	}
	log := logrus.WithField(obs.SandboxID, sandboxID)
	log.Info("checkpointing sandbox")

	fc := r.cfg.Runtime.Firecracker
	sandboxDir := sandboxStateDir(r.cfg.State, sandboxID)
	socketPath := jailerSocketPath(fc, sandboxID)
	if _, err := os.Stat(socketPath); err != nil {
		if os.IsNotExist(err) {
			return nil, status.Errorf(codes.NotFound, "sandbox %q not found", sandboxID)
		}
		log.WithError(err).Error("checkpoint sandbox: failed to stat socket")
		return nil, status.Errorf(codes.Internal, "checkpoint sandbox: %v", err)
	}

	client := firecracker.NewClient(socketPath, logrus.NewEntry(logrus.StandardLogger()), false)
	info, err := client.GetInstanceInfo(ctx)
	if err != nil {
		log.WithError(err).Error("checkpoint sandbox: failed to get instance info")
		return nil, status.Errorf(codes.Internal, "checkpoint sandbox: %v", err)
	}
	state := info.GetPayload().State
	if state == nil || *state != models.InstanceInfoStatePaused {
		got := "unknown"
		if state != nil {
			got = *state
		}
		return nil, status.Errorf(codes.FailedPrecondition, "sandbox %q must be stopped before checkpointing (state: %s)", sandboxID, got)
	}

	dir, err := newCheckpointDir(r.cfg.Checkpoint.Dir, uid)
	if err != nil {
		if errors.Is(err, ErrCheckpointExists) {
			return nil, status.Errorf(codes.AlreadyExists, "checkpoint %q already exists", uid)
		}
		log.WithError(err).Error("checkpoint sandbox: failed to create checkpoint dir")
		return nil, status.Errorf(codes.Internal, "checkpoint sandbox: %v", err)
	}
	cu := cleanup.Make(func() {
		if err := os.RemoveAll(dir); err != nil {
			log.WithError(err).Warn("checkpoint sandbox: cleanup: failed to remove checkpoint dir")
		}
	})
	defer cu.Clean()

	// Firecracker writes these relative to its own chroot root, so
	// they land in the jailer chroot, not dir -- moved below.
	memPath := filepath.Join(dir, checkpointMemFile)
	snapshotPath := filepath.Join(dir, checkpointSnapshotFile)

	params := &models.SnapshotCreateParams{
		MemFilePath:  firecracker.String(checkpointMemFile),
		SnapshotPath: firecracker.String(checkpointSnapshotFile),
	}
	if _, err := client.CreateSnapshot(ctx, params); err != nil {
		log.WithError(err).Error("checkpoint sandbox: failed to create snapshot")
		return nil, status.Errorf(codes.Internal, "checkpoint sandbox: %v", err)
	}

	if err := moveFile(jailerRootPath(fc, sandboxID, checkpointMemFile), memPath); err != nil {
		log.WithError(err).Error("checkpoint sandbox: failed to move mem file out of chroot")
		return nil, status.Errorf(codes.Internal, "checkpoint sandbox: %v", err)
	}
	if err := moveFile(jailerRootPath(fc, sandboxID, checkpointSnapshotFile), snapshotPath); err != nil {
		log.WithError(err).Error("checkpoint sandbox: failed to move snapshot file out of chroot")
		return nil, status.Errorf(codes.Internal, "checkpoint sandbox: %v", err)
	}

	compCfg := r.cfg.Checkpoint.Compression.Filesystem
	overlayPath := filepath.Join(sandboxDir, "overlay.ext4")
	compressedOverlayPath := filepath.Join(dir, overlayFileName(compress.Type(compCfg.Format)))
	if err := r.fileSystemCheckpointCompression.Compress(ctx, overlayPath, compressedOverlayPath, compress.WithLevel(compCfg.Level)); err != nil {
		log.WithError(err).Error("checkpoint sandbox: failed to compress overlay")
		return nil, status.Errorf(codes.Internal, "checkpoint sandbox: %v", err)
	}

	desc, err := readSandboxDescriptor(sandboxDir)
	if err != nil {
		log.WithError(err).Error("checkpoint sandbox: failed to read sandbox descriptor")
		return nil, status.Errorf(codes.Internal, "checkpoint sandbox: %v", err)
	}

	ipAddress, _, err := network.IPAddress(sandboxID)
	if err != nil {
		log.WithError(err).Error("checkpoint sandbox: failed to look up sandbox ip")
		return nil, status.Errorf(codes.Internal, "checkpoint sandbox: %v", err)
	}

	if err := writeSidecar(dir, Sidecar{
		ID:        uid,
		Name:      req.GetName(),
		Config:    desc.Config,
		IPAddress: ipAddress,
		UID:       desc.UID,
		GID:       desc.GID,
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		log.WithError(err).Error("checkpoint sandbox: failed to write sidecar")
		return nil, status.Errorf(codes.Internal, "checkpoint sandbox: %v", err)
	}

	cu.Release()
	log.WithField(obs.CheckPointID, uid).Info("sandbox checkpointed")
	return &v1.CheckpointSandboxResponse{CheckpointRef: uid}, nil
}

// moveFile copies src to dst then removes src -- a cross-filesystem-
// safe rename, since a jailed checkpoint's mem/snapshot files start
// out inside the jailer chroot, which may not share a filesystem with
// cfg.Checkpoint.Dir.
func moveFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open %s: %w", src, err)
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("create %s: %w", dst, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return fmt.Errorf("copy %s -> %s: %w", src, dst, err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("close %s: %w", dst, err)
	}
	if err := in.Close(); err != nil {
		return fmt.Errorf("close %s: %w", src, err)
	}
	return os.Remove(src)
}
