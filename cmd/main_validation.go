package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

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

			st, err := store.Open(c.Rootfs.Dir)
			if err != nil {
				return err
			}

			rfs, err := backbone.NewRootFS(c, st)
			if err != nil {
				return err
			}

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

			st, err := store.Open(c.Rootfs.Dir)
			if err != nil {
				return err
			}

			rt := runtime.NewRuntime(c, st)

			resp, err := rt.CreateSandbox(context.Background(), &v1.CreateSandboxRequest{
				Config: &v1.SandboxConfig{
					Metadata: &v1.SandboxMetadata{Uid: uid},
					Rootfs:   &v1.RootFSMount{Spec: &v1.RootFSSpec{Image: image}, IsRootDevice: true, IsReadOnly: true},
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
	cmd.Flags().StringVar(&uid, "uid", "validate-sandbox-4", "sandbox uid")
	cmd.Flags().Float64Var(&cpu, "cpu", 1, "vCPU count")
	cmd.Flags().Int64Var(&memoryMib, "memory-mib", 128, "memory size, in MiB")
	cmd.Flags().Int64Var(&storageMib, "storage-mib", 256, "overlay size, in MiB")

	return cmd
}

// newValidateStartSandboxCmd is a temporary manual-testing command for
// exercising StartSandbox directly from the CLI. The sandbox must
// already exist (see validate-create-sandbox), e.g.:
//
//	go run ./cmd validate-start-sandbox test-1
func newValidateStartSandboxCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "validate-start-sandbox <sandbox-id>",
		Short: "temporary: start a sandbox and print the result",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sandboxID := args[0]

			c, err := cfg.Load(configPath)
			if err != nil {
				return err
			}

			st, err := store.Open(c.Rootfs.Dir)
			if err != nil {
				return err
			}

			rt := runtime.NewRuntime(c, st)

			if _, err := rt.StartSandbox(context.Background(), &v1.StartSandboxRequest{SandboxId: sandboxID}); err != nil {
				return err
			}

			fmt.Printf("started sandbox %s\n", sandboxID)
			return nil
		},
	}

	cmd.Flags().StringVar(&configPath, "config", "/etc/sandbox-runtime/config.toml", "path to the configuration file")

	return cmd
}

// newValidateRunSandboxCmd is a temporary manual-testing command that
// creates and starts a sandbox, then blocks until interrupted, all
// within a single process. validate-create-sandbox and
// validate-start-sandbox run as two separate CLI invocations, so the
// firecracker child spawned by the first one outlives its parent and
// ends up in an orphaned process group by the time the second
// invocation starts it -- per POSIX terminal semantics, writes from an
// orphaned background process group to the controlling terminal fail
// with EIO, so the guest's console output (wired to our own stdio)
// never reaches the screen. Keeping create+start+run in one long-lived
// process avoids that: the firecracker child stays in a
// non-orphaned process group for as long as this command keeps running,
// e.g.:
//
//	go run ./cmd validate-run-sandbox docker.io/library/alpine:3.19
func newValidateRunSandboxCmd() *cobra.Command {
	var (
		configPath string
		uid        string
		cpu        float64
		memoryMib  int64
		storageMib int64
	)

	cmd := &cobra.Command{
		Use:   "validate-run-sandbox <image>",
		Short: "temporary: create, start, and hold open a sandbox until interrupted",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			image := args[0]

			c, err := cfg.Load(configPath)
			if err != nil {
				return err
			}

			st, err := store.Open(c.Rootfs.Dir)
			if err != nil {
				return err
			}

			rt := runtime.NewRuntime(c, st)

			ctx := context.Background()
			if _, err := rt.CreateSandbox(ctx, &v1.CreateSandboxRequest{
				Config: &v1.SandboxConfig{
					Metadata: &v1.SandboxMetadata{Uid: uid},
					Rootfs:   &v1.RootFSMount{Spec: &v1.RootFSSpec{Image: image}, IsRootDevice: true, IsReadOnly: true},
					Resources: &v1.SandboxResources{
						Cpu:          cpu,
						MemoryBytes:  memoryMib << 20,
						StorageBytes: storageMib << 20,
					},
				},
			}); err != nil {
				return err
			}

			if _, err := rt.StartSandbox(ctx, &v1.StartSandboxRequest{SandboxId: uid}); err != nil {
				return err
			}

			fmt.Printf("sandbox %s running, console attached -- press Ctrl+C to exit\n", uid)

			sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			<-sigCtx.Done()

			fmt.Printf("\nstopping -- sandbox %s is left running in the background\n", uid)
			return nil
		},
	}

	cmd.Flags().StringVar(&configPath, "config", "/etc/sandbox-runtime/config.toml", "path to the configuration file")
	cmd.Flags().StringVar(&uid, "uid", "validate-sandbox-run-4", "sandbox uid")
	cmd.Flags().Float64Var(&cpu, "cpu", 1, "vCPU count")
	cmd.Flags().Int64Var(&memoryMib, "memory-mib", 128, "memory size, in MiB")
	cmd.Flags().Int64Var(&storageMib, "storage-mib", 256, "overlay size, in MiB")

	return cmd
}
