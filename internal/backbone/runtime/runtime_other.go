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

package runtime

import (
	"context"

	v1 "github.com/tardigradeproj/sandbox-runtime/pkg/api/runtime/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (r *Runtime) CreateSandbox(context.Context, *v1.CreateSandboxRequest) (*v1.CreateSandboxResponse, error) {
	return nil, status.Error(codes.Unimplemented, "create sandbox: firecracker is only supported on linux")
}

func (r *Runtime) StartSandbox(context.Context, *v1.StartSandboxRequest) (*v1.StartSandboxResponse, error) {
	return nil, status.Error(codes.Unimplemented, "start sandbox: firecracker is only supported on linux")
}

func (r *Runtime) StopSandbox(context.Context, *v1.StopSandboxRequest) (*v1.StopSandboxResponse, error) {
	return nil, status.Error(codes.Unimplemented, "stop sandbox: firecracker is only supported on linux")
}
