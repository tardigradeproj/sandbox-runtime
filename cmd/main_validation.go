package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/tardigradeproj/sandbox-runtime/internal/backbone"
	"github.com/tardigradeproj/sandbox-runtime/internal/cfg"
	"github.com/tardigradeproj/sandbox-runtime/internal/runtime"
	v1 "github.com/tardigradeproj/sandbox-runtime/pkg/api/runtime/v1"
	"github.com/tardigradeproj/sandbox-runtime/pkg/filesystem/store"
)

// newValidatePullCmd is a temporary manual-testing command for
// exercising PullRootFS directly from the CLI with a real image
// reference, e.g.:
//
//	go run ./cmd validate-pull docker.io/library/alpine:3.19
func newValidatePullCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "validate-pull <image>",
		Short: "temporary: pull a rootfs image and print the result",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			image := args[0]

			c, err := cfg.Load(configPath)
			if err != nil {
				return err
			}

			st, err := store.Open(c.Rootfs)
			if err != nil {
				return err
			}

			rfs := backbone.NewRootFS(c, st)

			resp, err := rfs.PullRootFS(context.Background(), &v1.PullRootFSRequest{
				Spec: &v1.RootFSSpec{Image: image},
			})
			if err != nil {
				return err
			}

			fmt.Printf("pulled %s\n  digest: %s\n", image, resp.GetRootfsRef())
			return nil
		},
	}

	cmd.Flags().StringVar(&configPath, "config", "/etc/sandbox-runtime/config.toml", "path to the configuration file")

	return cmd
}

// newValidateCreateSandboxCmd is a temporary manual-testing command for
// exercising CreateSandbox directly from the CLI. image must already
// be present locally (see validate-pull), e.g.:
//
//	go run ./cmd validate-create-sandbox docker.io/library/alpine:3.19 --uid test-1
func newValidateCreateSandboxCmd() *cobra.Command {
	var (
		configPath string
		uid        string
		cpu        float64
		memoryMib  int64
		storageMib int64
	)

	cmd := &cobra.Command{
		Use:   "validate-create-sandbox <image>",
		Short: "temporary: create a sandbox and print the result",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			image := args[0]

			c, err := cfg.Load(configPath)
			if err != nil {
				return err
			}

			st, err := store.Open(c.Rootfs)
			if err != nil {
				return err
			}

			rt := runtime.NewRuntime(c, st)

			resp, err := rt.CreateSandbox(context.Background(), &v1.CreateSandboxRequest{
				Config: &v1.SandboxConfig{
					Metadata: &v1.SandboxMetadata{Uid: uid},
					Rootfs:   &v1.RootFSMount{Spec: &v1.RootFSSpec{Image: image}, IsRootDevice: true},
					Resources: &v1.SandboxResources{
						Cpu:          cpu,
						MemoryBytes:  memoryMib << 20,
						StorageBytes: storageMib << 20,
					},
				},
			})
			if err != nil {
				return err
			}

			fmt.Printf("created sandbox %s\n", resp.GetSandboxId())
			return nil
		},
	}

	cmd.Flags().StringVar(&configPath, "config", "/etc/sandbox-runtime/config.toml", "path to the configuration file")
	cmd.Flags().StringVar(&uid, "uid", "validate-sandbox", "sandbox uid")
	cmd.Flags().Float64Var(&cpu, "cpu", 1, "vCPU count")
	cmd.Flags().Int64Var(&memoryMib, "memory-mib", 128, "memory size, in MiB")
	cmd.Flags().Int64Var(&storageMib, "storage-mib", 256, "scratch overlay size, in MiB")

	return cmd
}
