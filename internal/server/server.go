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
// NetworkService over gRPC, on the address configured by
// cfg.Settings.GRPC.ListenAddress.
package server

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"os"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"github.com/tardigradeproj/sandbox-runtime/internal/backbone/network"
	"github.com/tardigradeproj/sandbox-runtime/internal/backbone/rootfs"
	"github.com/tardigradeproj/sandbox-runtime/internal/backbone/runtime"
	"github.com/tardigradeproj/sandbox-runtime/internal/cfg"
	v1 "github.com/tardigradeproj/sandbox-runtime/pkg/api/runtime/v1"
	"github.com/tardigradeproj/sandbox-runtime/pkg/filesystem/store"
)

// New builds the gRPC server and the listener for
// cfg.GRPC.ListenAddress.
func New(c *cfg.Settings) (*grpc.Server, net.Listener, error) {
	st, err := store.New(c.Rootfs.Dir)
	if err != nil {
		return nil, nil, fmt.Errorf("server: %w", err)
	}

	rt, err := runtime.NewRuntime(c, st)
	if err != nil {
		return nil, nil, fmt.Errorf("server: %w", err)
	}
	rfs, err := rootfs.NewRootFS(c, st)
	if err != nil {
		return nil, nil, fmt.Errorf("server: %w", err)
	}
	nsvc, err := network.New(c)
	if err != nil {
		return nil, nil, fmt.Errorf("server: %w", err)
	}

	isTCP, lis, err := listen(c.GRPC.ListenAddress)
	if err != nil {
		return nil, nil, fmt.Errorf("server: %w", err)
	}

	var opts []grpc.ServerOption
	if c.GRPC.TLS.Enabled {
		if !isTCP {
			return nil, nil, fmt.Errorf("server: grpc.tls.enabled requires a TCP grpc.listen_address, not %q", c.GRPC.ListenAddress)
		}
		creds, err := serverTLSCredentials(c.GRPC.TLS)
		if err != nil {
			return nil, nil, fmt.Errorf("server: %w", err)
		}
		opts = append(opts, grpc.Creds(creds))
	}

	s := grpc.NewServer(opts...)
	v1.RegisterRuntimeServiceServer(s, rt)
	v1.RegisterRootFSServiceServer(s, rfs)
	v1.RegisterNetworkServiceServer(s, nsvc)

	return s, lis, nil
}

// listen parses addr as "unix://<path>" or "<host>:<port>" (also
// accepting a bare "tcp://" prefix) and opens it, reporting whether
// it opened a TCP listener (as opposed to unix) so New can refuse
// TLS config on a unix socket.
func listen(addr string) (isTCP bool, _ net.Listener, _ error) {
	if path, ok := strings.CutPrefix(addr, "unix://"); ok {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return false, nil, fmt.Errorf("remove stale socket %q: %w", path, err)
		}
		lis, err := net.Listen("unix", path)
		if err != nil {
			return false, nil, fmt.Errorf("listen on %q: %w", addr, err)
		}
		return false, lis, nil
	}

	tcpAddr := strings.TrimPrefix(addr, "tcp://")
	lis, err := net.Listen("tcp", tcpAddr)
	if err != nil {
		return false, nil, fmt.Errorf("listen on %q: %w", addr, err)
	}
	return true, lis, nil
}

// serverTLSCredentials builds mTLS server credentials from t: the
// server's own cert/key, and a client CA pool such that any client
// certificate chaining to it is authorized.
func serverTLSCredentials(t cfg.TLS) (credentials.TransportCredentials, error) {
	if t.CertFile == "" || t.KeyFile == "" || t.ClientCAFile == "" {
		return nil, fmt.Errorf("tls: cert_file, key_file, and client_ca_file must all be set")
	}

	cert, err := tls.LoadX509KeyPair(t.CertFile, t.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("tls: load server cert/key: %w", err)
	}

	caPEM, err := os.ReadFile(t.ClientCAFile)
	if err != nil {
		return nil, fmt.Errorf("tls: read client ca: %w", err)
	}
	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("tls: client_ca_file %q contains no usable certificates", t.ClientCAFile)
	}

	return credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    caPool,
		MinVersion:   tls.VersionTLS12,
	}), nil
}
