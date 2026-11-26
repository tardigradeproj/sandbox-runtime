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

package server

import (
	"context"

	connect "connectrpc.com/connect"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"

	v1 "github.com/tardigradeproj/sandbox-runtime/pkg/api/runtime/v1"
	"github.com/tardigradeproj/sandbox-runtime/pkg/api/runtime/v1/v1connect"
)

// unary adapts call (one of the backbone gRPC client's own methods,
// each shaped (ctx, *Req, ...grpc.CallOption) (*Resp, error)) into the
// connect.Request/connect.Response-wrapped shape v1connect's generated
// *ServiceHandler interfaces require -- the one bit of glue those
// interfaces need beyond what a plain grpc client call already gives.
func unary[Req, Resp any](ctx context.Context, req *connect.Request[Req], call func(context.Context, *Req, ...grpc.CallOption) (*Resp, error)) (*connect.Response[Resp], error) {
	resp, err := call(ctx, req.Msg)
	if err != nil {
		return nil, connectError(err)
	}
	return connect.NewResponse(resp), nil
}

// connectError translates a google.golang.org/grpc/status error (what
// every call to the backbone's gRPC server returns on failure) into a
// connect.Error carrying the equivalent code -- connect.Code's values
// are defined to match gRPC's status codes exactly, but connect-go
// doesn't understand grpc-go's status.Error type on its own, so
// without this translation every error would surface to the browser
// as CodeUnknown regardless of what codes.Code the backbone actually
// set.
func connectError(err error) error {
	if err == nil {
		return nil
	}
	s, ok := status.FromError(err)
	if !ok {
		return err
	}
	return connect.NewError(connect.Code(s.Code()), s.Err())
}

// runtimeHandler adapts a plain gRPC RuntimeServiceClient (dialed
// straight at the backbone daemon's own GRPC.ListenAddress -- see
// cmd/app/console.go) to v1connect.RuntimeServiceHandler. The console
// is a separate process from the backbone daemon, so unlike an
// earlier version of this file, it never touches *runtime.Runtime (or
// rootfs.RootFS/network.Network below) directly: doing so would mean
// constructing a second, independent copy of that stateful machinery
// (e.g. the jailer id allocator) rather than talking to the one the
// backbone process already owns.
type runtimeHandler struct {
	client v1.RuntimeServiceClient
}

func (h runtimeHandler) CreateSandbox(ctx context.Context, req *connect.Request[v1.CreateSandboxRequest]) (*connect.Response[v1.CreateSandboxResponse], error) {
	return unary(ctx, req, h.client.CreateSandbox)
}

func (h runtimeHandler) StartSandbox(ctx context.Context, req *connect.Request[v1.StartSandboxRequest]) (*connect.Response[v1.StartSandboxResponse], error) {
	return unary(ctx, req, h.client.StartSandbox)
}

func (h runtimeHandler) ListSandbox(ctx context.Context, req *connect.Request[v1.ListSandboxRequest]) (*connect.Response[v1.ListSandboxResponse], error) {
	return unary(ctx, req, h.client.ListSandbox)
}

func (h runtimeHandler) UpdateSandboxUserData(ctx context.Context, req *connect.Request[v1.UpdateSandboxUserDataRequest]) (*connect.Response[v1.UpdateSandboxUserDataResponse], error) {
	return unary(ctx, req, h.client.UpdateSandboxUserData)
}

func (h runtimeHandler) StopSandbox(ctx context.Context, req *connect.Request[v1.StopSandboxRequest]) (*connect.Response[v1.StopSandboxResponse], error) {
	return unary(ctx, req, h.client.StopSandbox)
}

func (h runtimeHandler) RemoveSandbox(ctx context.Context, req *connect.Request[v1.RemoveSandboxRequest]) (*connect.Response[v1.RemoveSandboxResponse], error) {
	return unary(ctx, req, h.client.RemoveSandbox)
}

func (h runtimeHandler) CheckpointSandbox(ctx context.Context, req *connect.Request[v1.CheckpointSandboxRequest]) (*connect.Response[v1.CheckpointSandboxResponse], error) {
	return unary(ctx, req, h.client.CheckpointSandbox)
}

func (h runtimeHandler) ListCheckpoint(ctx context.Context, req *connect.Request[v1.ListCheckpointRequest]) (*connect.Response[v1.ListCheckpointResponse], error) {
	return unary(ctx, req, h.client.ListCheckpoint)
}

var _ v1connect.RuntimeServiceHandler = runtimeHandler{}

// rootFSHandler adapts a plain gRPC RootFSServiceClient to
// v1connect.RootFSServiceHandler -- see runtimeHandler.
type rootFSHandler struct {
	client v1.RootFSServiceClient
}

func (h rootFSHandler) ListRootFS(ctx context.Context, req *connect.Request[v1.ListRootFSRequest]) (*connect.Response[v1.ListRootFSResponse], error) {
	return unary(ctx, req, h.client.ListRootFS)
}

func (h rootFSHandler) RootFSStatus(ctx context.Context, req *connect.Request[v1.RootFSStatusRequest]) (*connect.Response[v1.RootFSStatusResponse], error) {
	return unary(ctx, req, h.client.RootFSStatus)
}

func (h rootFSHandler) PullRootFS(ctx context.Context, req *connect.Request[v1.PullRootFSRequest]) (*connect.Response[v1.PullRootFSResponse], error) {
	return unary(ctx, req, h.client.PullRootFS)
}

func (h rootFSHandler) RemoveRootFS(ctx context.Context, req *connect.Request[v1.RemoveRootFSRequest]) (*connect.Response[v1.RemoveRootFSResponse], error) {
	return unary(ctx, req, h.client.RemoveRootFS)
}

var _ v1connect.RootFSServiceHandler = rootFSHandler{}

// networkHandler adapts a plain gRPC NetworkServiceClient to
// v1connect.NetworkServiceHandler -- see runtimeHandler.
type networkHandler struct {
	client v1.NetworkServiceClient
}

func (h networkHandler) AttachNetwork(ctx context.Context, req *connect.Request[v1.AttachNetworkRequest]) (*connect.Response[v1.AttachNetworkResponse], error) {
	return unary(ctx, req, h.client.AttachNetwork)
}

func (h networkHandler) DetachNetwork(ctx context.Context, req *connect.Request[v1.DetachNetworkRequest]) (*connect.Response[v1.DetachNetworkResponse], error) {
	return unary(ctx, req, h.client.DetachNetwork)
}

var _ v1connect.NetworkServiceHandler = networkHandler{}
