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

package oci

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	ggcrv1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

func newTestRegistry(t *testing.T, repoTag string) (host string, digest ggcrv1.Hash) {
	t.Helper()
	srv := httptest.NewServer(registry.New())
	t.Cleanup(srv.Close)
	host = strings.TrimPrefix(srv.URL, "http://")

	img, err := random.Image(256, 2)
	if err != nil {
		t.Fatalf("random.Image: %v", err)
	}
	d, err := img.Digest()
	if err != nil {
		t.Fatalf("img.Digest: %v", err)
	}

	ref, err := name.ParseReference(host+"/"+repoTag, name.WeakValidation, name.Insecure)
	if err != nil {
		t.Fatalf("ParseReference: %v", err)
	}
	if err := remote.Write(ref, img); err != nil {
		t.Fatalf("remote.Write: %v", err)
	}
	return host, d
}

func TestPullImageByTag(t *testing.T) {
	host, wantDigest := newTestRegistry(t, "library/widget:v1")

	img, err := PullImage(context.Background(), host+"/library/widget:v1")
	if err != nil {
		t.Fatalf("PullImage: %v", err)
	}
	gotDigest, err := img.Digest()
	if err != nil {
		t.Fatalf("img.Digest: %v", err)
	}
	if gotDigest != wantDigest {
		t.Errorf("img.Digest() = %v, want %v", gotDigest, wantDigest)
	}
}

func TestPullImageByDigest(t *testing.T) {
	host, wantDigest := newTestRegistry(t, "library/widget:v1")

	img, err := PullImage(context.Background(), host+"/library/widget@"+wantDigest.String())
	if err != nil {
		t.Fatalf("PullImage: %v", err)
	}
	gotDigest, err := img.Digest()
	if err != nil {
		t.Fatalf("img.Digest: %v", err)
	}
	if gotDigest != wantDigest {
		t.Errorf("img.Digest() = %v, want %v", gotDigest, wantDigest)
	}
}

func TestPullImageInvalidReference(t *testing.T) {
	_, err := PullImage(context.Background(), "!!! not a reference !!!")
	if err == nil {
		t.Fatal("PullImage: expected error, got nil")
	}
	if !errors.Is(err, ErrInvalidReference) {
		t.Errorf("PullImage error = %v, want it to wrap ErrInvalidReference", err)
	}
}

func TestPullImageNotFound(t *testing.T) {
	host, _ := newTestRegistry(t, "library/widget:v1")

	if _, err := PullImage(context.Background(), host+"/library/does-not-exist:v1"); err == nil {
		t.Fatal("PullImage: expected error for missing image, got nil")
	}
}

func TestPullImageConfigAndLayers(t *testing.T) {
	host, _ := newTestRegistry(t, "library/widget:v1") // pushes a 2-layer image

	img, err := PullImage(context.Background(), host+"/library/widget:v1")
	if err != nil {
		t.Fatalf("PullImage: %v", err)
	}
	cfg, err := img.ConfigFile()
	if err != nil {
		t.Fatalf("img.ConfigFile: %v", err)
	}
	layers, err := img.Layers()
	if err != nil {
		t.Fatalf("img.Layers: %v", err)
	}
	if len(layers) != len(cfg.RootFS.DiffIDs) {
		t.Fatalf("len(layers) = %d, len(cfg.RootFS.DiffIDs) = %d, want equal", len(layers), len(cfg.RootFS.DiffIDs))
	}

	for i, layer := range layers {
		diffID, err := layer.DiffID()
		if err != nil {
			t.Fatalf("layers[%d].DiffID: %v", i, err)
		}
		if diffID != cfg.RootFS.DiffIDs[i] {
			t.Errorf("layers[%d].DiffID() = %v, want %v", i, diffID, cfg.RootFS.DiffIDs[i])
		}

		rc, err := layer.Uncompressed()
		if err != nil {
			t.Fatalf("layers[%d].Uncompressed: %v", i, err)
		}
		n, err := io.Copy(io.Discard, rc)
		rc.Close()
		if err != nil {
			t.Fatalf("layers[%d] read content: %v", i, err)
		}
		if n == 0 {
			t.Errorf("layers[%d] uncompressed content is empty", i)
		}
	}
}

func TestPullImageResolvesIndexToHostArchitecture(t *testing.T) {
	srv := httptest.NewServer(registry.New())
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")

	amd64Img, err := random.Image(128, 1)
	if err != nil {
		t.Fatalf("random.Image (amd64): %v", err)
	}
	arm64Img, err := random.Image(128, 1)
	if err != nil {
		t.Fatalf("random.Image (arm64): %v", err)
	}
	amd64Digest, err := amd64Img.Digest()
	if err != nil {
		t.Fatalf("amd64Img.Digest: %v", err)
	}
	arm64Digest, err := arm64Img.Digest()
	if err != nil {
		t.Fatalf("arm64Img.Digest: %v", err)
	}

	idx := mutate.AppendManifests(empty.Index,
		mutate.IndexAddendum{
			Add:        amd64Img,
			Descriptor: ggcrv1.Descriptor{Platform: &ggcrv1.Platform{OS: "linux", Architecture: "amd64"}},
		},
		mutate.IndexAddendum{
			Add:        arm64Img,
			Descriptor: ggcrv1.Descriptor{Platform: &ggcrv1.Platform{OS: "linux", Architecture: "arm64"}},
		},
	)

	ref, err := name.ParseReference(host+"/library/multiarch:v1", name.WeakValidation, name.Insecure)
	if err != nil {
		t.Fatalf("ParseReference: %v", err)
	}
	if err := remote.WriteIndex(ref, idx); err != nil {
		t.Fatalf("remote.WriteIndex: %v", err)
	}

	img, err := PullImage(context.Background(), host+"/library/multiarch:v1")
	if err != nil {
		t.Fatalf("PullImage: %v", err)
	}
	gotDigest, err := img.Digest()
	if err != nil {
		t.Fatalf("img.Digest: %v", err)
	}

	wantDigest := amd64Digest
	if runtime.GOARCH == "arm64" {
		wantDigest = arm64Digest
	}
	if gotDigest != wantDigest {
		t.Errorf("resolved image digest = %v, want %v (the %s child)", gotDigest, wantDigest, runtime.GOARCH)
	}
}
