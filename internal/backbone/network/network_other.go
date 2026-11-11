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

//go:build !linux

package network

import (
	"context"

	"github.com/tardigradeproj/sandbox-runtime/internal/cfg"
	v1 "github.com/tardigradeproj/sandbox-runtime/pkg/api/runtime/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Network implements v1.NetworkServiceServer. pkg/network only builds
// on linux, so off linux every RPC is unimplemented.
type Network struct {
	v1.UnimplementedNetworkServiceServer
}

// New never actually sets anything up off linux; it only exists so
// callers can construct a Network on every platform.
func New(*cfg.Settings) (*Network, error) {
	return &Network{}, nil
}

func (n *Network) AttachNetwork(context.Context, *v1.AttachNetworkRequest) (*v1.AttachNetworkResponse, error) {
	return nil, status.Error(codes.Unimplemented, "attach network: only supported on linux")
}

func (n *Network) DetachNetwork(context.Context, *v1.DetachNetworkRequest) (*v1.DetachNetworkResponse, error) {
	return nil, status.Error(codes.Unimplemented, "detach network: only supported on linux")
}
