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
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/tardigradeproj/sandbox-runtime/pkg/observability"
	uiserver "github.com/tardigradeproj/sandbox-runtime/ui/server"
)

// NewConsoleCmd serves the Backbone Console on its own listener, proxying RuntimeService/RootFSService/
// NetworkService calls to a backbone daemon (NewGRPCCmd) running
// separately, possibly on a different host.
func NewConsoleCmd() *cobra.Command {
	var listenAddress string
	var backboneAddress string

	cmd := &cobra.Command{
		Use:   "console",
		Short: "serve the Backbone Console browser UI, proxying to a backbone gRPC server",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := observability.ConfigureLogging("info", "text"); err != nil {
				return err
			}

			conn, err := grpc.NewClient(dialTarget(backboneAddress), grpc.WithTransportCredentials(insecure.NewCredentials()))
			if err != nil {
				return err
			}
			defer conn.Close()

			srv, lis, err := uiserver.New(listenAddress, conn)
			if err != nil {
				return err
			}

			sigCtx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			go func() {
				<-sigCtx.Done()
				logrus.Info("shutting down")
				if err := srv.Shutdown(context.Background()); err != nil {
					logrus.WithError(err).Warn("console: shutdown")
				}
			}()

			logrus.WithFields(logrus.Fields{
				observability.ConsoleListenAddress: listenAddress,
				"backbone.address":                 backboneAddress,
			}).Info("serving console")
			if err := srv.Serve(lis); err != nil && !errors.Is(err, http.ErrServerClosed) {
				return err
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&listenAddress, "listen", "127.0.0.1:9090", "address the console's own Connect/HTTP server binds to")
	cmd.Flags().StringVar(&backboneAddress, "backbone-addr", "unix:///var/run/sandbox-runtime.sock", "address of the backbone gRPC server ('sandbox-runtime grpc') to proxy to")

	return cmd
}

// dialTarget normalizes addr the same way internal/server's listen()
// does on the serving side: a bare "tcp://" prefix is stripped to a
// plain "host:port" (grpc-go's default "dns" resolver handles an
// IP-literal host:port target directly, no scheme needed), while
// "unix://" is passed through unchanged -- grpc-go's built-in "unix"
// resolver expects exactly that form.
func dialTarget(addr string) string {
	return strings.TrimPrefix(addr, "tcp://")
}
