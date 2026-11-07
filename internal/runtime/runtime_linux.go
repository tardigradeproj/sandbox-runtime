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
	"math"
	"os"
	"path/filepath"

	firecracker "github.com/firecracker-microvm/firecracker-go-sdk"
	"github.com/firecracker-microvm/firecracker-go-sdk/client/models"
	"github.com/sirupsen/logrus"
	v1 "github.com/tardigradeproj/sandbox-runtime/pkg/api/runtime/v1"
	"github.com/tardigradeproj/sandbox-runtime/pkg/cleanup"
	obs "github.com/tardigradeproj/sandbox-runtime/pkg/observability"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// CreateSandbox prepares a sandbox's on-disk state (scratch overlay) and
// builds the Firecracker microVM configuration for it, without booting
// the guest. The microVM is actually started by StartSandbox.
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

	scratchPath := filepath.Join(sandboxDir, "rootfs.ext4")
	if err := createScratchImage(ctx, scratchPath, storageBytes); err != nil {
		log.WithError(err).Error("create sandbox: failed to create scratch image")
		return nil, status.Errorf(codes.Internal, "create sandbox: %v", err)
	}
	log.WithField(obs.ScratchPath, scratchPath).Debug("create sandbox: scratch overlay created")

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
		AddDrive(scratchPath, false).
		Build()

	vcpuCount := int64(math.Ceil(config.GetResources().GetCpu()))
	if vcpuCount < 1 {
		vcpuCount = 1
	}
	memSizeMib := memoryBytes / (1024 * 1024)
	if memSizeMib < 1 {
		memSizeMib = 1
	}

	socketPath := filepath.Join(sandboxDir, "firecracker.sock")

	machineCfg := firecracker.Config{
		VMID:            sandboxID,
		SocketPath:      socketPath,
		LogPath:         fc.Log.LogPath,
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

	cmd := firecracker.VMCommandBuilder{}.
		WithBin(fc.Bin).
		WithSocketPath(socketPath).
		AddArgs("--id", sandboxID).
		Build(ctx)

	machine, err := firecracker.NewMachine(ctx, machineCfg, firecracker.WithProcessRunner(cmd))
	if err != nil {
		log.WithError(err).Error("create sandbox: failed to build firecracker machine config")
		return nil, status.Errorf(codes.Internal, "create sandbox: %v", err)
	}
	cu.Add(func() {
		if err := machine.StopVMM(); err != nil {
			log.WithError(err).Warn("create sandbox: cleanup: failed to stop vmm")
		}
	})

	// Run the same handler chain machine.Start would, which spawns the
	// firecracker process (equivalent to `firecracker --api-sock
	// <socketPath>`) and configures it over the API, but stop short of
	// Start's final step, which sends the InstanceStart action.
	log.WithField(obs.SocketPath, socketPath).Debug("create sandbox: spawning firecracker process")
	if err := machine.Handlers.Run(ctx, machine); err != nil {
		log.WithError(err).Error("create sandbox: failed to spawn/configure firecracker")
		return nil, status.Errorf(codes.Internal, "create sandbox: %v", err)
	}

	cu.Release()
	log.WithFields(logrus.Fields{
		obs.RootfsPath:  rootfsPath,
		obs.ScratchPath: scratchPath,
		obs.SocketPath:  socketPath,
	}).Info("sandbox created")
	return &v1.CreateSandboxResponse{SandboxId: sandboxID}, nil
}

func (r *Runtime) StartSandbox(context.Context, *v1.StartSandboxRequest) (*v1.StartSandboxResponse, error) {
	panic("implement me!")
}
