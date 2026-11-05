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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestRegistry(t *testing.T, repoTag string) (host string, digest ggcrv1.Hash) {
	t.Helper()
	srv := httptest.NewServer(registry.New())
	t.Cleanup(srv.Close)
	host = strings.TrimPrefix(srv.URL, "http://")

	img, err := random.Image(256, 2)
	require.NoError(t, err)
	d, err := img.Digest()
	require.NoError(t, err)

	ref, err := name.ParseReference(host+"/"+repoTag, name.WeakValidation, name.Insecure)
	require.NoError(t, err)
	require.NoError(t, remote.Write(ref, img))
	return host, d
}

func TestPullImageByTag(t *testing.T) {
	host, wantDigest := newTestRegistry(t, "library/widget:v1")

	img, err := PullImage(context.Background(), host+"/library/widget:v1")
	require.NoError(t, err)
	gotDigest, err := img.Digest()
	require.NoError(t, err)
	assert.Equal(t, wantDigest, gotDigest)
}

func TestPullImageByDigest(t *testing.T) {
	host, wantDigest := newTestRegistry(t, "library/widget:v1")

	img, err := PullImage(context.Background(), host+"/library/widget@"+wantDigest.String())
	require.NoError(t, err)
	gotDigest, err := img.Digest()
	require.NoError(t, err)
	assert.Equal(t, wantDigest, gotDigest)
}

func TestPullImageInvalidReference(t *testing.T) {
	_, err := PullImage(context.Background(), "!!! not a reference !!!")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidReference)
}

func TestPullImageNotFound(t *testing.T) {
	host, _ := newTestRegistry(t, "library/widget:v1")

	_, err := PullImage(context.Background(), host+"/library/does-not-exist:v1")
	assert.Error(t, err)
}

func TestPullImageConfigAndLayers(t *testing.T) {
	host, _ := newTestRegistry(t, "library/widget:v1") // pushes a 2-layer image

	img, err := PullImage(context.Background(), host+"/library/widget:v1")
	require.NoError(t, err)
	cfg, err := img.ConfigFile()
	require.NoError(t, err)
	layers, err := img.Layers()
	require.NoError(t, err)
	require.Len(t, layers, len(cfg.RootFS.DiffIDs))

	for i, layer := range layers {
		diffID, err := layer.DiffID()
		require.NoError(t, err)
		assert.Equal(t, cfg.RootFS.DiffIDs[i], diffID)

		rc, err := layer.Uncompressed()
		require.NoError(t, err)
		n, err := io.Copy(io.Discard, rc)
		rc.Close()
		require.NoError(t, err)
		assert.NotZero(t, n, "layers[%d] uncompressed content is empty", i)
	}
}

func TestPullImageResolvesIndexToHostArchitecture(t *testing.T) {
	srv := httptest.NewServer(registry.New())
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")

	amd64Img, err := random.Image(128, 1)
	require.NoError(t, err)
	arm64Img, err := random.Image(128, 1)
	require.NoError(t, err)
	amd64Digest, err := amd64Img.Digest()
	require.NoError(t, err)
	arm64Digest, err := arm64Img.Digest()
	require.NoError(t, err)

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
	require.NoError(t, err)
	require.NoError(t, remote.WriteIndex(ref, idx))

	img, err := PullImage(context.Background(), host+"/library/multiarch:v1")
	require.NoError(t, err)
	gotDigest, err := img.Digest()
	require.NoError(t, err)

	wantDigest := amd64Digest
	if runtime.GOARCH == "arm64" {
		wantDigest = arm64Digest
	}
	assert.Equal(t, wantDigest, gotDigest)
}
