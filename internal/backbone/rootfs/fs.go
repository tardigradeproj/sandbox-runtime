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

package rootfs

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"github.com/sirupsen/logrus"
	"github.com/tardigradeproj/sandbox-runtime/internal/cfg"
	"github.com/tardigradeproj/sandbox-runtime/pkg/filesystem/store"
	obs "github.com/tardigradeproj/sandbox-runtime/pkg/observability"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	v1 "github.com/tardigradeproj/sandbox-runtime/pkg/api/runtime/v1"
	"github.com/tardigradeproj/sandbox-runtime/pkg/filesystem/oci"
	"github.com/tardigradeproj/sandbox-runtime/pkg/filesystem/packer"
	"github.com/tardigradeproj/sandbox-runtime/pkg/filesystem/rootfs"
)

// RootFS implements v1.RootFSServiceServer.
type RootFS struct {
	v1.UnimplementedRootFSServiceServer
	cfg    *cfg.Settings
	store  *store.Store
	packer packer.Packer
}

// NewRootFS constructs a RootFS backed by store, using the packer
// format selected by cfg.Rootfs.Packer.Type.
func NewRootFS(cfg *cfg.Settings, store *store.Store) (*RootFS, error) {
	pk, err := packer.New(packer.Type(cfg.Rootfs.Packer.Type))
	if err != nil {
		return nil, fmt.Errorf("rootfs: %w", err)
	}
	return &RootFS{cfg: cfg, store: store, packer: pk}, nil
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
	log := logrus.WithField(obs.RootfsImage, image)
	log.Info("pulling rootfs")

	img, err := oci.PullImage(ctx, image)
	if err != nil {
		if errors.Is(err, oci.ErrInvalidReference) {
			return nil, status.Errorf(codes.InvalidArgument, "%v", err)
		}
		log.WithError(err).Error("failed to pull image")
		return nil, pullError(err)
	}

	digest, err := img.Digest()
	if err != nil {
		log.WithError(err).Error("failed to resolve digest")
		return nil, status.Errorf(codes.Internal, "%v", err)
	}
	digestStr := digest.String()
	log = log.WithField(obs.RootfsDigest, digestStr)

	repoTags, repoDigest := repoRefs(image, digestStr)

	if _, ok, err := r.store.Lookup(digestStr); err != nil {
		log.WithError(err).Error("failed to look up store entry")
		return nil, status.Errorf(codes.Internal, "%v", err)
	} else if ok {
		if err := r.store.AddRef(digestStr, repoTags, repoDigest); err != nil {
			log.WithError(err).Error("failed to add ref to existing entry")
			return nil, status.Errorf(codes.Internal, "%v", err)
		}
		log.Info("rootfs already present, added ref")
		return &v1.PullRootFSResponse{RootfsRef: digestStr}, nil
	}

	extractDir, tmpImage, cleanup, err := r.store.Stage()
	if err != nil {
		log.WithError(err).Error("failed to stage store entry")
		return nil, status.Errorf(codes.Internal, "%v", err)
	}
	defer cleanup()

	if err := rootfs.Unpack(ctx, img, extractDir); err != nil {
		log.WithError(err).Error("failed to unpack layers")
		return nil, status.Errorf(codes.Internal, "%v", err)
	}
	log.Debug("layers unpacked")

	if err := r.packer.Pack(ctx, extractDir, tmpImage); err != nil {
		log.WithError(err).Error("failed to pack rootfs image")
		return nil, status.Errorf(codes.Internal, "%v", err)
	}

	fi, err := os.Stat(tmpImage)
	if err != nil {
		log.WithError(err).Error("failed to stat packed image")
		return nil, status.Errorf(codes.Internal, "%v", err)
	}

	entry := store.Entry{
		Digest:          digestStr,
		Size:            uint64(fi.Size()),
		SpecImage:       image,
		SpecAnnotations: spec.GetAnnotations(),
		RepoTags:        repoTags,
	}
	if repoDigest != "" {
		entry.RepoDigests = []string{repoDigest}
	}
	if err := r.store.Commit(entry, tmpImage); err != nil {
		log.WithError(err).Error("failed to commit store entry")
		return nil, status.Errorf(codes.Internal, "%v", err)
	}

	imagePath, _ := r.store.ImagePath(digestStr)
	log.WithFields(logrus.Fields{
		obs.RootfsPath: imagePath,
		obs.RootfsSize: entry.Size,
	}).Info("rootfs pulled")
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
//
// repoTags includes both the canonicalized tag (e.g. "alpine" becomes
// "index.docker.io/library/alpine:latest") and the original, as-typed
// image string, so a later lookup using whatever shorthand the caller
// used to pull still resolves.
func repoRefs(image, digestStr string) (repoTags []string, repoDigest string) {
	ref, err := name.ParseReference(image)
	if err != nil {
		return nil, ""
	}
	if t, ok := ref.(name.Tag); ok {
		canonical := t.Name()
		repoTags = append(repoTags, canonical)
		if image != canonical {
			repoTags = append(repoTags, image)
		}
	}
	return repoTags, ref.Context().Digest(digestStr).Name()
}

// pullError maps a registry-side pull failure to an appropriate gRPC
// status.
func pullError(err error) error {
	var terr *transport.Error
	if errors.As(err, &terr) {
		switch terr.StatusCode {
		case http.StatusUnauthorized:
			return status.Errorf(codes.Unauthenticated, "%v", err)
		case http.StatusForbidden:
			return status.Errorf(codes.PermissionDenied, "%v", err)
		case http.StatusNotFound:
			return status.Errorf(codes.NotFound, "%v", err)
		}
	}
	return status.Errorf(codes.Internal, "%v", err)
}
