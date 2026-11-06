package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/tardigradeproj/sandbox-runtime/internal/config"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "sandbox-runtime",
		Short: "sandbox-runtime runs the Firecracker-backed sandbox runtime daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(configPath)
			if err != nil {
				return err
			}

			fmt.Printf("loaded config from %s\n", configPath)
			fmt.Printf("  rootfs:          %s\n", cfg.Rootfs)
			fmt.Printf("  version:         %s\n", cfg.Version)
			fmt.Printf("  grpc listen:     %s\n", cfg.GRPC.ListenAddress)
			fmt.Printf("  firecracker bin: %s\n", cfg.Runtime.Firecracker.Bin)
			return nil
		},
	}

	cmd.Flags().StringVar(&configPath, "config", "/etc/sandbox-runtime/config.toml", "path to the configuration file")

	return cmd
}
