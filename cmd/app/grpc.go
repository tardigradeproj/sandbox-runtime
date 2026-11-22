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

package app

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/tardigradeproj/sandbox-runtime/internal/cfg"
	"github.com/tardigradeproj/sandbox-runtime/internal/server"
	"github.com/tardigradeproj/sandbox-runtime/pkg/observability"
)

// NewGRPCCmd is the backbone daemon itself: RuntimeService,
// RootFSService, and NetworkService over plain gRPC, on
// cfg.GRPC.ListenAddress. See NewConsoleCmd for the separate,
// browser-facing process that proxies to this one.
func NewGRPCCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "grpc",
		Short: "serve RuntimeService, RootFSService, and NetworkService over gRPC",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := cfg.Load(configPath)
			if err != nil {
				return err
			}

			if err := observability.ConfigureLogging(c.Logs.Level, c.Logs.Format); err != nil {
				return err
			}

			logrus.Infof("loaded config from %s", configPath)
			logrus.WithFields(logrus.Fields{
				observability.ConfigRootfs:      c.Rootfs.Dir,
				observability.ConfigVersion:     c.Version,
				observability.GRPCListenAddress: c.GRPC.ListenAddress,
				observability.FirecrackerBin:    c.Runtime.Firecracker.Bin,
			}).Info("runtime configuration")

			rt, rfs, nsvc, err := server.NewServices(c)
			if err != nil {
				return err
			}

			grpcSrv, grpcLis, err := server.New(c, rt, rfs, nsvc)
			if err != nil {
				return err
			}

			sigCtx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			go func() {
				<-sigCtx.Done()
				logrus.Info("shutting down")
				grpcSrv.GracefulStop()
			}()

			logrus.WithField(observability.GRPCListenAddress, c.GRPC.ListenAddress).Info("serving grpc")
			return grpcSrv.Serve(grpcLis)
		},
	}

	cmd.Flags().StringVar(&configPath, "config", "/etc/sandbox-runtime/config.toml", "path to the configuration file")

	return cmd
}
