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
	"errors"
	"path/filepath"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	v1 "github.com/tardigradeproj/sandbox-runtime/pkg/api/runtime/v1"
	"github.com/tardigradeproj/sandbox-runtime/pkg/oci"
	"github.com/tardigradeproj/sandbox-runtime/pkg/rootfs"
)

// RootFS implements v1.RootFSServiceServer.
type RootFS struct {
	v1.UnimplementedRootFSServiceServer

	// Root is the directory under which pulled rootfs trees are
	// unpacked, one subdirectory per image digest.
	Root string
}

// NewRootFS returns a RootFS that unpacks pulled images under root.
func NewRootFS(root string) *RootFS {
	return &RootFS{Root: root}
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
func (r *RootFS) PullRootFS(ctx context.Context, req *v1.PullRootFSRequest) (*v1.PullRootFSResponse, error) {
	image := req.GetSpec().GetImage()
	if image == "" {
		return nil, status.Error(codes.InvalidArgument, "spec.image must be set")
	}

	img, err := oci.PullImage(ctx, image)
	if err != nil {
		if errors.Is(err, oci.ErrInvalidReference) {
			return nil, status.Errorf(codes.InvalidArgument, "pull rootfs: %v", err)
		}
		return nil, status.Errorf(codes.Internal, "pull rootfs: %v", err)
	}

	digest, err := img.Digest()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "pull rootfs: %v", err)
	}

	dir := filepath.Join(r.Root, strings.ReplaceAll(digest.String(), ":", "-"))
	if err := rootfs.Unpack(ctx, img, dir); err != nil {
		return nil, status.Errorf(codes.Internal, "pull rootfs: %v", err)
	}

	// TODO: pack the unpacked rootfs directory into an ext4 image; for
	// now PullRootFS leaves it as a plain directory and reports the
	// resolved manifest digest.
	return &v1.PullRootFSResponse{RootfsRef: digest.String()}, nil
}

// RemoveRootFS removes a rootfs from local storage.
func (*RootFS) RemoveRootFS(context.Context, *v1.RemoveRootFSRequest) (*v1.RemoveRootFSResponse, error) {
	panic("not implemented")
}
