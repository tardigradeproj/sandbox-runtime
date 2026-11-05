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
	"net/http"
	"os"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"github.com/tardigradeproj/sandbox-runtime/pkg/filesystem/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	v1 "github.com/tardigradeproj/sandbox-runtime/pkg/api/runtime/v1"
	"github.com/tardigradeproj/sandbox-runtime/pkg/filesystem/ext4"
	"github.com/tardigradeproj/sandbox-runtime/pkg/filesystem/oci"
	"github.com/tardigradeproj/sandbox-runtime/pkg/filesystem/rootfs"
)

// RootFS implements v1.RootFSServiceServer.
type RootFS struct {
	v1.UnimplementedRootFSServiceServer

	store  *store.Store
	packer ext4.Packer
}

// NewRootFS opens (creating if necessary) a rootfs cache rooted at
// rootDir.
func NewRootFS(rootDir string) (*RootFS, error) {
	st, err := store.Open(rootDir)
	if err != nil {
		return nil, err
	}
	return &RootFS{store: st, packer: ext4.MkfsExt4Packer{}}, nil
}

// ListRootFS lists the rootfs images known to the runtime, optionally
// narrowed by filter.
func (r *RootFS) ListRootFS(_ context.Context, req *v1.ListRootFSRequest) (*v1.ListRootFSResponse, error) {
	if ref := req.GetFilter().GetSpec().GetImage(); ref != "" {
		entry, ok, err := r.store.Resolve(ref)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "list rootfs: %v", err)
		}
		if !ok {
			return &v1.ListRootFSResponse{}, nil
		}
		return &v1.ListRootFSResponse{Rootfs: []*v1.RootFS{entryToProto(entry)}}, nil
	}

	entries, err := r.store.List()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list rootfs: %v", err)
	}
	resp := &v1.ListRootFSResponse{Rootfs: make([]*v1.RootFS, 0, len(entries))}
	for _, e := range entries {
		resp.Rootfs = append(resp.Rootfs, entryToProto(e))
	}
	return resp, nil
}

// RootFSStatus returns the status of the rootfs identified by spec, if
// it exists locally.
func (r *RootFS) RootFSStatus(_ context.Context, req *v1.RootFSStatusRequest) (*v1.RootFSStatusResponse, error) {
	ref := req.GetSpec().GetImage()
	if ref == "" {
		return nil, status.Error(codes.InvalidArgument, "spec.image must be set")
	}

	entry, ok, err := r.store.Resolve(ref)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "rootfs status: %v", err)
	}
	if !ok {
		return nil, status.Errorf(codes.NotFound, "rootfs %q not found", ref)
	}

	resp := &v1.RootFSStatusResponse{Rootfs: entryToProto(entry)}
	if req.GetVerbose() {
		path, err := r.store.ImagePath(entry.Digest)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "rootfs status: %v", err)
		}
		resp.Info = map[string]string{"path": path}
	}
	return resp, nil
}

// PullRootFS fetches a rootfs (e.g. from an OCI registry or a template
// catalog) and stores it locally so it can be used to launch a sandbox.
func (r *RootFS) PullRootFS(ctx context.Context, req *v1.PullRootFSRequest) (*v1.PullRootFSResponse, error) {
	spec := req.GetSpec()
	image := spec.GetImage()
	if image == "" {
		return nil, status.Error(codes.InvalidArgument, "spec.image must be set")
	}

	img, err := oci.PullImage(ctx, image, authOption(req.GetAuth()))
	if err != nil {
		if errors.Is(err, oci.ErrInvalidReference) {
			return nil, status.Errorf(codes.InvalidArgument, "pull rootfs: %v", err)
		}
		return nil, pullError(err)
	}

	digest, err := img.Digest()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "pull rootfs: %v", err)
	}
	digestStr := digest.String()

	repoTag, repoDigest := repoRefs(image, digestStr)

	if _, ok, err := r.store.Lookup(digestStr); err != nil {
		return nil, status.Errorf(codes.Internal, "pull rootfs: %v", err)
	} else if ok {
		if err := r.store.AddRef(digestStr, repoTag, repoDigest); err != nil {
			return nil, status.Errorf(codes.Internal, "pull rootfs: %v", err)
		}
		return &v1.PullRootFSResponse{RootfsRef: digestStr}, nil
	}

	extractDir, tmpImage, cleanup, err := r.store.Stage()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "pull rootfs: %v", err)
	}
	defer cleanup()

	if err := rootfs.Unpack(ctx, img, extractDir); err != nil {
		return nil, status.Errorf(codes.Internal, "pull rootfs: %v", err)
	}

	totalBytes, entryCount, err := ext4.MeasureTree(extractDir)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "pull rootfs: %v", err)
	}
	sizeBytes := ext4.EstimateImageSize(totalBytes, ext4.DefaultSizeOptions)
	inodeCount := ext4.EstimateInodeCount(entryCount, ext4.DefaultSizeOptions)

	if err := r.packer.Pack(ctx, extractDir, tmpImage, sizeBytes, inodeCount); err != nil {
		return nil, status.Errorf(codes.Internal, "pull rootfs: %v", err)
	}

	fi, err := os.Stat(tmpImage)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "pull rootfs: %v", err)
	}

	entry := store.Entry{
		Digest:          digestStr,
		Size:            uint64(fi.Size()),
		SpecImage:       image,
		SpecAnnotations: spec.GetAnnotations(),
	}
	if repoTag != "" {
		entry.RepoTags = []string{repoTag}
	}
	if repoDigest != "" {
		entry.RepoDigests = []string{repoDigest}
	}
	if err := r.store.Commit(entry, tmpImage); err != nil {
		return nil, status.Errorf(codes.Internal, "pull rootfs: %v", err)
	}

	return &v1.PullRootFSResponse{RootfsRef: digestStr}, nil
}

// RemoveRootFS removes a rootfs from local storage.
func (r *RootFS) RemoveRootFS(_ context.Context, req *v1.RemoveRootFSRequest) (*v1.RemoveRootFSResponse, error) {
	ref := req.GetSpec().GetImage()
	if ref == "" {
		return nil, status.Error(codes.InvalidArgument, "spec.image must be set")
	}

	entry, ok, err := r.store.Resolve(ref)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "remove rootfs: %v", err)
	}
	if !ok {
		return &v1.RemoveRootFSResponse{}, nil
	}
	if err := r.store.Remove(entry.Digest); err != nil {
		return nil, status.Errorf(codes.Internal, "remove rootfs: %v", err)
	}
	return &v1.RemoveRootFSResponse{}, nil
}

func entryToProto(e store.Entry) *v1.RootFS {
	return &v1.RootFS{
		Id:          e.Digest,
		RepoTags:    e.RepoTags,
		RepoDigests: e.RepoDigests,
		Size:        e.Size,
		Spec:        &v1.RootFSSpec{Image: e.SpecImage, Annotations: e.SpecAnnotations},
	}
}

// repoRefs derives the repo-qualified tag and digest references for
// image, given the digest it resolved to. image is re-parsed locally
// (no network call) since oci.PullImage already validated it.
func repoRefs(image, digestStr string) (repoTag, repoDigest string) {
	ref, err := name.ParseReference(image)
	if err != nil {
		return "", ""
	}
	if t, ok := ref.(name.Tag); ok {
		repoTag = t.Name()
	}
	return repoTag, ref.Context().Digest(digestStr).Name()
}

// authOption maps a PullRootFSRequest's AuthConfig onto a crane.Option,
// falling back to the default keychain when auth carries no
// credentials (which is also crane's own default).
func authOption(auth *v1.AuthConfig) crane.Option {
	if auth == nil || (auth.GetUsername() == "" && auth.GetPassword() == "" && auth.GetAuth() == "" &&
		auth.GetIdentityToken() == "" && auth.GetRegistryToken() == "") {
		return crane.WithAuthFromKeychain(authn.DefaultKeychain)
	}
	return crane.WithAuth(authn.FromConfig(authn.AuthConfig{
		Username:      auth.GetUsername(),
		Password:      auth.GetPassword(),
		Auth:          auth.GetAuth(),
		IdentityToken: auth.GetIdentityToken(),
		RegistryToken: auth.GetRegistryToken(),
	}))
}

// pullError maps a registry-side pull failure to an appropriate gRPC
// status.
func pullError(err error) error {
	var terr *transport.Error
	if errors.As(err, &terr) {
		switch terr.StatusCode {
		case http.StatusUnauthorized:
			return status.Errorf(codes.Unauthenticated, "pull rootfs: %v", err)
		case http.StatusForbidden:
			return status.Errorf(codes.PermissionDenied, "pull rootfs: %v", err)
		case http.StatusNotFound:
			return status.Errorf(codes.NotFound, "pull rootfs: %v", err)
		}
	}
	return status.Errorf(codes.Internal, "pull rootfs: %v", err)
}
