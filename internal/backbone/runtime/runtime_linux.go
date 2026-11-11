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
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	firecracker "github.com/firecracker-microvm/firecracker-go-sdk"
	"github.com/firecracker-microvm/firecracker-go-sdk/client/models"
	"github.com/sirupsen/logrus"
	"github.com/tardigradeproj/sandbox-runtime/internal/cfg"
	v1 "github.com/tardigradeproj/sandbox-runtime/pkg/api/runtime/v1"
	"github.com/tardigradeproj/sandbox-runtime/pkg/cleanup"
	"github.com/tardigradeproj/sandbox-runtime/pkg/network"
	obs "github.com/tardigradeproj/sandbox-runtime/pkg/observability"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const firecrackerSocketName = "firecracker.sock"

// jailerSocketPath mirrors the SDK's own jail() path computation, so
// StartSandbox can find a jailed sandbox's socket independently.
func jailerSocketPath(fc cfg.Firecracker, sandboxID string) string {
	return filepath.Join(fc.Jailer.ChrootBaseDir, filepath.Base(fc.Bin), sandboxID, "root", firecrackerSocketName)
}

// CreateSandbox prepares a sandbox's on-disk state (writable overlay)
// and builds the Firecracker microVM configuration for it, without
// booting the guest. The microVM is actually started by StartSandbox.
func (r *Runtime) CreateSandbox(ctx context.Context, req *v1.CreateSandboxRequest) (*v1.CreateSandboxResponse, error) {
	config := req.GetConfig()

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

	sandboxDir := filepath.Join(r.cfg.State, sandboxID)
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
	if fc.Jailer.Enabled {
		uid, gid, err := r.resolveJailerUIDGID(fc.Jailer, sandboxID)
		if err != nil {
			log.WithError(err).Error("create sandbox: failed to obtain jailer uid/gid")
			return nil, status.Errorf(codes.Internal, "create sandbox: %v", err)
		}

		// Hard-linked into the chroot as-is, so the jailed uid/gid
		// needs write access to the shared inode.
		if err := os.Chown(overlayPath, uid, gid); err != nil {
			log.WithError(err).Error("create sandbox: failed to chown overlay image for jailer")
			return nil, status.Errorf(codes.Internal, "create sandbox: %v", err)
		}

		// The tap device (created earlier by AttachNetwork) is owned
		// by root; without CAP_NET_ADMIN, the jailed uid can only
		// open it if it's also the device's recorded owner.
		if err := network.SetTapOwner(sandboxID, uid, gid); err != nil {
			log.WithError(err).Error("create sandbox: failed to chown tap device for jailer")
			return nil, status.Errorf(codes.Internal, "create sandbox: %v", err)
		}

		if fc.Jailer.UIDGID.Mode == "on_demand" {
			cu.Add(func() {
				if err := r.jailerIDs.Release(uid); err != nil {
					log.WithError(err).Warn("create sandbox: cleanup: failed to release jailer id")
				}
			})
		}
		cu.Add(func() {
			chrootDir := filepath.Join(fc.Jailer.ChrootBaseDir, filepath.Base(fc.Bin), sandboxID)
			if err := os.RemoveAll(chrootDir); err != nil {
				log.WithError(err).Warn("create sandbox: cleanup: failed to remove jailer chroot dir")
			}
		})

		// Namespace + tap device are provisioned independently, by
		// NetworkService (see pkg/network), not by CreateSandbox --
		// RuntimeService and NetworkService don't call each other.
		// Both the namespace path and the tap device name are fixed
		// conventions, not values CreateSandbox needs to look up.
		machineCfg.NetNS = network.NetnsPath(sandboxID)
		machineCfg.NetworkInterfaces = firecracker.NetworkInterfaces{{
			StaticConfiguration: &firecracker.StaticNetworkConfiguration{
				HostDevName: network.TapDeviceName,
			},
		}}

		guest := network.GuestNetwork(r.cfg.Network.GuestNetwork)
		machineCfg.KernelArgs = strings.TrimSpace(machineCfg.KernelArgs + " " + network.GuestIPKernelParam(guest))

		// LogFifo (not LogPath) gets hard-linked into the chroot by the SDK.
		logPath = filepath.Join(sandboxDir, "firecracker.fifo")
		machineCfg.LogFifo = logPath
		machineCfg.JailerCfg = buildJailerConfig(fc, uid, gid, sandboxID, kernelImagePath)

		// Chroot-relative: the SDK's jail() rewrites this to the real
		// host path (see jailerSocketPath).
		machineCfg.SocketPath = firecrackerSocketName
		socketPath = jailerSocketPath(fc, sandboxID)

		machine, err = firecracker.NewMachine(ctx, machineCfg)
		if err != nil {
			log.WithError(err).Error("create sandbox: failed to build firecracker machine config")
			return nil, status.Errorf(codes.Internal, "create sandbox: %v", err)
		}
	} else {
		machineCfg.LogPath = logPath

		// Wire the guest's console to our own stdio; a nil Stdin
		// defaults to /dev/null, an immediate EOF.
		cmd := firecracker.VMCommandBuilder{}.
			WithBin(fc.Bin).
			WithSocketPath(socketPath).
			WithStdin(os.Stdin).
			WithStdout(os.Stdout).
			WithStderr(os.Stderr).
			AddArgs("--id", sandboxID).
			Build(ctx)

		var err error
		machine, err = firecracker.NewMachine(ctx, machineCfg, firecracker.WithProcessRunner(cmd))
		if err != nil {
			log.WithError(err).Error("create sandbox: failed to build firecracker machine config")
			return nil, status.Errorf(codes.Internal, "create sandbox: %v", err)
		}
	}
	cu.Add(func() {
		if err := machine.StopVMM(); err != nil {
			log.WithError(err).Warn("create sandbox: cleanup: failed to stop vmm")
		}
	})

	// Same as machine.Start, minus the final InstanceStart action.
	log.WithField(obs.SocketPath, socketPath).Debug("create sandbox: spawning firecracker process")
	if err := machine.Handlers.Run(ctx, machine); err != nil {
		log.WithError(err).Error("create sandbox: failed to spawn/configure firecracker")
		return nil, status.Errorf(codes.Internal, "create sandbox: %v", err)
	}

	cu.Release()
	log.WithFields(logrus.Fields{
		obs.RootfsPath:  rootfsPath,
		obs.OverlayPath: overlayPath,
		obs.SocketPath:  socketPath,
		obs.LogPath:     logPath,
	}).Info("sandbox created")
	return &v1.CreateSandboxResponse{SandboxId: sandboxID}, nil
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
// TODO.md).
func buildJailerConfig(fc cfg.Firecracker, uid, gid int, sandboxID, kernelImagePath string) *firecracker.JailerConfig {
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
		Stdin:          os.Stdin,
		Stdout:         os.Stdout,
		Stderr:         os.Stderr,
	}
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
	socketPath := filepath.Join(r.cfg.State, sandboxID, firecrackerSocketName)
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

	client := firecracker.NewClient(socketPath, logrus.NewEntry(logrus.StandardLogger()), false)
	action := models.InstanceActionInfoActionTypeInstanceStart
	if _, err := client.CreateSyncAction(ctx, &models.InstanceActionInfo{ActionType: &action}); err != nil {
		log.WithError(err).Error("start sandbox: failed to start instance")
		return nil, status.Errorf(codes.Internal, "start sandbox: %v", err)
	}

	log.Info("sandbox started")
	return &v1.StartSandboxResponse{}, nil
}
