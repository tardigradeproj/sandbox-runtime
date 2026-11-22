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

// Package server exposes RuntimeService, RootFSService, and
// NetworkService to browsers: a Connect handler (gRPC, gRPC-Web, and
// Connect's own protocol, all on one port -- see connectrpc.com)
// proxying each call to a running backbone daemon's own plain gRPC
// server (see conn, below), plus the built React console
// (ui/frontend) served as a static-file fallback, plus ExecService
// (exec.go), which talks to each sandbox's own sandboxd directly and
// never touches the backbone at all.
//
// The console is deliberately its own process (cmd/app/console.go),
// not part of the backbone daemon (cmd/app/grpc.go) or its
// cfg.Settings -- it has no on-disk state of its own, so it has
// nothing to construct and nothing to lose by being restarted or run
// (or not) independently of the daemon it's pointed at.
package server

import (
	"fmt"
	"io/fs"
	"net"
	"net/http"

	"google.golang.org/grpc"

	v1 "github.com/tardigradeproj/sandbox-runtime/pkg/api/runtime/v1"
	"github.com/tardigradeproj/sandbox-runtime/pkg/api/runtime/v1/v1connect"
	"github.com/tardigradeproj/sandbox-runtime/ui/frontend"
	"github.com/tardigradeproj/sandbox-runtime/ui/proto/console/v1/consolev1connect"
)

// New builds the console's HTTP server and the listener for
// listenAddress, proxying RuntimeService/RootFSService/NetworkService
// calls over conn -- a connection to a backbone daemon's own gRPC
// server (see cmd/app/console.go for how conn is dialed).
func New(listenAddress string, conn *grpc.ClientConn) (*http.Server, net.Listener, error) {
	mux := http.NewServeMux()

	path, handler := v1connect.NewRuntimeServiceHandler(runtimeHandler{client: v1.NewRuntimeServiceClient(conn)})
	mux.Handle(path, handler)
	path, handler = v1connect.NewRootFSServiceHandler(rootFSHandler{client: v1.NewRootFSServiceClient(conn)})
	mux.Handle(path, handler)
	path, handler = v1connect.NewNetworkServiceHandler(networkHandler{client: v1.NewNetworkServiceClient(conn)})
	mux.Handle(path, handler)
	path, handler = consolev1connect.NewExecServiceHandler(newExecHandler())
	mux.Handle(path, handler)

	dist, err := fs.Sub(frontend.DistFS, "dist")
	if err != nil {
		return nil, nil, fmt.Errorf("ui/server: %w", err)
	}
	mux.Handle("/", http.FileServerFS(dist))

	lis, err := net.Listen("tcp", listenAddress)
	if err != nil {
		return nil, nil, fmt.Errorf("ui/server: listen on %q: %w", listenAddress, err)
	}

	// UnencryptedHTTP2: gRPC/gRPC-Web clients need real HTTP/2 framing
	// (for trailers), which this listener otherwise wouldn't offer
	// without TLS. HTTP1 stays on too, for the Connect protocol and
	// the static frontend, both of which are plain HTTP/1.1-friendly.
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)
	srv := &http.Server{Handler: mux, Protocols: protocols}
	return srv, lis, nil
}
