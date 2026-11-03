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

package backbone

import (
	"context"

	v1 "github.com/tardigradeproj/sandbox-runtime/pkg/api/runtime/v1"
)

// RootFS implements v1.RootFSServiceServer.
type RootFS struct {
	v1.UnimplementedRootFSServiceServer
}

// ListRootFS lists the rootfs images known to the runtime, optionally
// narrowed by filter.
func (*RootFS) ListRootFS(context.Context, *v1.ListRootFSRequest) (*v1.ListRootFSResponse, error) {
	panic("not implemented")
}

// RootFSStatus returns the status of the rootfs identified by spec, if it
// exists locally.
func (*RootFS) RootFSStatus(context.Context, *v1.RootFSStatusRequest) (*v1.RootFSStatusResponse, error) {
	panic("not implemented")
}

// PullRootFS fetches a rootfs (e.g. from an OCI registry or a template
// catalog) and stores it locally so it can be used to launch a sandbox.
func (*RootFS) PullRootFS(context.Context, *v1.PullRootFSRequest) (*v1.PullRootFSResponse, error) {
	panic("not implemented")
}

// RemoveRootFS removes a rootfs from local storage.
func (*RootFS) RemoveRootFS(context.Context, *v1.RemoveRootFSRequest) (*v1.RemoveRootFSResponse, error) {
	panic("not implemented")
}
