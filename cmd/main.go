package main

import (
	"os"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/tardigradeproj/sandbox-runtime/internal/cfg"
	"github.com/tardigradeproj/sandbox-runtime/pkg/observability"
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
			cfg, err := cfg.Load(configPath)
			if err != nil {
				return err
			}

			if err := observability.ConfigureLogging(cfg.Logs.Level, cfg.Logs.Format); err != nil {
				return err
			}

			logrus.Infof("loaded config from %s", configPath)
			logrus.WithFields(logrus.Fields{
				observability.ConfigRootfs:      cfg.Rootfs.Dir,
				observability.ConfigVersion:     cfg.Version,
				observability.GRPCListenAddress: cfg.GRPC.ListenAddress,
				observability.FirecrackerBin:    cfg.Runtime.Firecracker.Bin,
			}).Info("runtime configuration")
			return nil
		},
	}

	cmd.Flags().StringVar(&configPath, "config", "/etc/sandbox-runtime/config.toml", "path to the configuration file")

	// TEMPORARY: manual-testing subcommands, remove before merging.
	cmd.AddCommand(newValidatePullCmd())
	cmd.AddCommand(newValidateCreateSandboxCmd())
	cmd.AddCommand(newValidateStartSandboxCmd())
	cmd.AddCommand(newValidateStopSandboxCmd())
	cmd.AddCommand(newValidateRunSandboxCmd())

	return cmd
}
