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

// Package network implements v1.NetworkServiceServer, a thin gRPC
// wrapper around pkg/network's node-local bridge/namespace/tap/IP
// plumbing. See net-design.md.
package network

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/tardigradeproj/sandbox-runtime/internal/cfg"
	v1 "github.com/tardigradeproj/sandbox-runtime/pkg/api/runtime/v1"
	netcore "github.com/tardigradeproj/sandbox-runtime/pkg/network"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Network implements v1.NetworkServiceServer.
type Network struct {
	v1.UnimplementedNetworkServiceServer
	core *netcore.Network
}

// New sets up this node's bridge and NAT rule (see netcore.Open) and
// returns a Network ready to serve AttachNetwork/DetachNetwork.
func New(cfg *cfg.Settings) (*Network, error) {
	core, err := netcore.Open(context.Background(), cfg.Network.Cidr, cfg.Network.BridgeName, filepath.Join(cfg.State, "network"))
	if err != nil {
		return nil, fmt.Errorf("network: %w", err)
	}
	return &Network{core: core}, nil
}

// AttachNetwork provisions networking for a sandbox: a namespace, a
// tap device inside it for the guest NIC, and a veth pair out to this
// node's bridge for egress.
func (n *Network) AttachNetwork(ctx context.Context, req *v1.AttachNetworkRequest) (*v1.AttachNetworkResponse, error) {
	sandboxID := req.GetSandboxId()
	if sandboxID == "" {
		return nil, status.Error(codes.InvalidArgument, "sandbox_id must be set")
	}

	att, err := n.core.Attach(ctx, sandboxID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "attach network: %v", err)
	}
	return &v1.AttachNetworkResponse{
		NetnsPath: att.NetnsPath,
		TapDevice: att.TapDevice,
		IpAddress: att.IPAddress,
		GatewayIp: att.GatewayIP,
	}, nil
}

// DetachNetwork reverses AttachNetwork and releases the sandbox's IP.
func (n *Network) DetachNetwork(ctx context.Context, req *v1.DetachNetworkRequest) (*v1.DetachNetworkResponse, error) {
	sandboxID := req.GetSandboxId()
	if sandboxID == "" {
		return nil, status.Error(codes.InvalidArgument, "sandbox_id must be set")
	}

	if err := n.core.Detach(ctx, sandboxID); err != nil {
		return nil, status.Errorf(codes.Internal, "detach network: %v", err)
	}
	return &v1.DetachNetworkResponse{}, nil
}
