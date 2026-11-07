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
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	ggcrv1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/partial"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	v1 "github.com/tardigradeproj/sandbox-runtime/pkg/api/runtime/v1"
	"github.com/tardigradeproj/sandbox-runtime/pkg/filesystem/erofs"
	"github.com/tardigradeproj/sandbox-runtime/pkg/filesystem/store"
)

// fakePacker stands in for mkfs.erofs: on success it writes a
// placeholder file at imagePath so the real packed image's later
// os.Stat succeeds.
type fakePacker struct {
	err   error
	calls int
}

func (f *fakePacker) Pack(_ context.Context, _, imagePath string) error {
	f.calls++
	if f.err != nil {
		return f.err
	}
	return os.WriteFile(imagePath, []byte("fake erofs image"), 0o644)
}

// uncompressedLayer is a minimal partial.UncompressedLayer backed by
// explicit tar content, used instead of random.Image/random.Layer
// because those default to uid/gid 0 in their tar headers, which
// archive.Apply's ownership-preserving Lchown then refuses to fake
// without root.
type uncompressedLayer struct {
	content []byte
	diffID  ggcrv1.Hash
}

func (l *uncompressedLayer) DiffID() (ggcrv1.Hash, error) { return l.diffID, nil }
func (l *uncompressedLayer) Uncompressed() (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(l.content)), nil
}
func (l *uncompressedLayer) MediaType() (types.MediaType, error) { return types.DockerLayer, nil }

// buildTestImage returns a single-layer v1.Image whose one file is
// owned by this test process's own uid/gid.
func buildTestImage(t *testing.T) ggcrv1.Image {
	t.Helper()
	content := []byte("widget content\n")

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	hdr := &tar.Header{
		Name: "etc/widget.txt", Mode: 0o644, Size: int64(len(content)),
		Uid: os.Getuid(), Gid: os.Getgid(),
	}
	require.NoError(t, tw.WriteHeader(hdr))
	_, err := tw.Write(content)
	require.NoError(t, err)
	require.NoError(t, tw.Close())

	sum := sha256.Sum256(buf.Bytes())
	diffID := ggcrv1.Hash{Algorithm: "sha256", Hex: hex.EncodeToString(sum[:])}
	layer, err := partial.UncompressedToLayer(&uncompressedLayer{content: buf.Bytes(), diffID: diffID})
	require.NoError(t, err)
	img, err := mutate.Append(empty.Image, mutate.Addendum{Layer: layer})
	require.NoError(t, err)
	return img
}

// newTestRegistry starts an in-process registry and pushes a small
// test image to it under repoTag.
func newTestRegistry(t *testing.T, repoTag string) string {
	t.Helper()
	srv := httptest.NewServer(registry.New())
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")

	ref, err := name.ParseReference(host+"/"+repoTag, name.WeakValidation, name.Insecure)
	require.NoError(t, err)
	require.NoError(t, remote.Write(ref, buildTestImage(t)))
	return host
}

func newTestRootFS(t *testing.T, pk erofs.Packer) *RootFS {
	t.Helper()
	st, err := store.Open(t.TempDir())
	require.NoError(t, err)
	return &RootFS{store: st, packer: pk}
}

func TestPullRootFSRequiresImage(t *testing.T) {
	rfs := newTestRootFS(t, &fakePacker{})
	_, err := rfs.PullRootFS(context.Background(), &v1.PullRootFSRequest{Spec: &v1.RootFSSpec{}})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestPullRootFSInvalidReference(t *testing.T) {
	rfs := newTestRootFS(t, &fakePacker{})
	_, err := rfs.PullRootFS(context.Background(), &v1.PullRootFSRequest{
		Spec: &v1.RootFSSpec{Image: "!!! not a reference !!!"},
	})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestPullRootFSSuccess(t *testing.T) {
	host := newTestRegistry(t, "library/widget:v1")
	pk := &fakePacker{}
	rfs := newTestRootFS(t, pk)

	resp, err := rfs.PullRootFS(context.Background(), &v1.PullRootFSRequest{
		Spec: &v1.RootFSSpec{Image: host + "/library/widget:v1"},
	})
	require.NoError(t, err)
	require.NotEmpty(t, resp.GetRootfsRef())
	assert.Equal(t, 1, pk.calls)

	entry, ok, err := rfs.store.Lookup(resp.GetRootfsRef())
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, host+"/library/widget:v1", entry.SpecImage)
	assert.Equal(t, []string{host + "/library/widget:v1"}, entry.RepoTags)
	assert.NotZero(t, entry.Size)
}

func TestPullRootFSCacheHitSkipsPack(t *testing.T) {
	host := newTestRegistry(t, "library/widget:v1")
	pk := &fakePacker{}
	rfs := newTestRootFS(t, pk)

	first, err := rfs.PullRootFS(context.Background(), &v1.PullRootFSRequest{
		Spec: &v1.RootFSSpec{Image: host + "/library/widget:v1"},
	})
	require.NoError(t, err)
	require.Equal(t, 1, pk.calls)

	// Re-tag the same content and pull it under the new tag too.
	ref, err := name.ParseReference(host+"/library/widget:v1", name.WeakValidation, name.Insecure)
	require.NoError(t, err)
	img, err := remote.Image(ref)
	require.NoError(t, err)
	ref2, err := name.ParseReference(host+"/library/widget:latest", name.WeakValidation, name.Insecure)
	require.NoError(t, err)
	require.NoError(t, remote.Write(ref2, img))

	second, err := rfs.PullRootFS(context.Background(), &v1.PullRootFSRequest{
		Spec: &v1.RootFSSpec{Image: host + "/library/widget:latest"},
	})
	require.NoError(t, err)
	assert.Equal(t, first.GetRootfsRef(), second.GetRootfsRef())
	assert.Equal(t, 1, pk.calls)

	entry, ok, err := rfs.store.Lookup(first.GetRootfsRef())
	require.NoError(t, err)
	require.True(t, ok)
	assert.Len(t, entry.RepoTags, 2)
}

func TestPullRootFSPackerFailureLeavesStoreUntouched(t *testing.T) {
	host := newTestRegistry(t, "library/widget:v1")
	pk := &fakePacker{err: errors.New("no space left on device")}
	rfs := newTestRootFS(t, pk)

	_, err := rfs.PullRootFS(context.Background(), &v1.PullRootFSRequest{
		Spec: &v1.RootFSSpec{Image: host + "/library/widget:v1"},
	})
	require.Equal(t, codes.Internal, status.Code(err))

	entries, err := rfs.store.List()
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestListRootFSEmpty(t *testing.T) {
	rfs := newTestRootFS(t, &fakePacker{})
	resp, err := rfs.ListRootFS(context.Background(), &v1.ListRootFSRequest{})
	require.NoError(t, err)
	assert.Empty(t, resp.GetRootfs())
}

func TestListRootFSAfterPullAndFilter(t *testing.T) {
	host := newTestRegistry(t, "library/widget:v1")
	rfs := newTestRootFS(t, &fakePacker{})

	pullResp, err := rfs.PullRootFS(context.Background(), &v1.PullRootFSRequest{
		Spec: &v1.RootFSSpec{Image: host + "/library/widget:v1"},
	})
	require.NoError(t, err)

	all, err := rfs.ListRootFS(context.Background(), &v1.ListRootFSRequest{})
	require.NoError(t, err)
	require.Len(t, all.GetRootfs(), 1)
	assert.Equal(t, pullResp.GetRootfsRef(), all.GetRootfs()[0].GetId())

	filtered, err := rfs.ListRootFS(context.Background(), &v1.ListRootFSRequest{
		Filter: &v1.RootFSFilter{Spec: &v1.RootFSSpec{Image: host + "/library/widget:v1"}},
	})
	require.NoError(t, err)
	assert.Len(t, filtered.GetRootfs(), 1)

	none, err := rfs.ListRootFS(context.Background(), &v1.ListRootFSRequest{
		Filter: &v1.RootFSFilter{Spec: &v1.RootFSSpec{Image: host + "/library/does-not-exist:v1"}},
	})
	require.NoError(t, err)
	assert.Empty(t, none.GetRootfs())
}

func TestRootFSStatusNotFound(t *testing.T) {
	rfs := newTestRootFS(t, &fakePacker{})
	_, err := rfs.RootFSStatus(context.Background(), &v1.RootFSStatusRequest{
		Spec: &v1.RootFSSpec{Image: "sha256:" + strings.Repeat("a", 64)},
	})
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestRootFSStatusVerbose(t *testing.T) {
	host := newTestRegistry(t, "library/widget:v1")
	rfs := newTestRootFS(t, &fakePacker{})

	pullResp, err := rfs.PullRootFS(context.Background(), &v1.PullRootFSRequest{
		Spec: &v1.RootFSSpec{Image: host + "/library/widget:v1"},
	})
	require.NoError(t, err)

	resp, err := rfs.RootFSStatus(context.Background(), &v1.RootFSStatusRequest{
		Spec:    &v1.RootFSSpec{Image: pullResp.GetRootfsRef()},
		Verbose: true,
	})
	require.NoError(t, err)
	assert.Equal(t, pullResp.GetRootfsRef(), resp.GetRootfs().GetId())
	path := resp.GetInfo()["path"]
	require.NotEmpty(t, path)
	assert.FileExists(t, path)
}

func TestRemoveRootFS(t *testing.T) {
	host := newTestRegistry(t, "library/widget:v1")
	rfs := newTestRootFS(t, &fakePacker{})

	pullResp, err := rfs.PullRootFS(context.Background(), &v1.PullRootFSRequest{
		Spec: &v1.RootFSSpec{Image: host + "/library/widget:v1"},
	})
	require.NoError(t, err)

	_, err = rfs.RemoveRootFS(context.Background(), &v1.RemoveRootFSRequest{
		Spec: &v1.RootFSSpec{Image: pullResp.GetRootfsRef()},
	})
	require.NoError(t, err)

	_, ok, err := rfs.store.Lookup(pullResp.GetRootfsRef())
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestRemoveRootFSUnknownIsNoop(t *testing.T) {
	rfs := newTestRootFS(t, &fakePacker{})
	_, err := rfs.RemoveRootFS(context.Background(), &v1.RemoveRootFSRequest{
		Spec: &v1.RootFSSpec{Image: "sha256:" + strings.Repeat("a", 64)},
	})
	assert.NoError(t, err)
}
