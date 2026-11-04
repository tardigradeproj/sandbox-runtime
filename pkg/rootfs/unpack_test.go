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
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"testing"

	ggcrv1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/partial"
	"github.com/google/go-containerregistry/pkg/v1/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type tarEntry struct {
	name     string
	typeflag byte
	body     string
	linkname string
}

type uncompressedLayer struct {
	content []byte
	diffID  ggcrv1.Hash
}

func (l *uncompressedLayer) DiffID() (ggcrv1.Hash, error) { return l.diffID, nil }
func (l *uncompressedLayer) Uncompressed() (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(l.content)), nil
}
func (l *uncompressedLayer) MediaType() (types.MediaType, error) { return types.DockerLayer, nil }

func buildLayer(t *testing.T, entries []tarEntry) ggcrv1.Layer {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range entries {
		typeflag := e.typeflag
		if typeflag == 0 {
			typeflag = tar.TypeReg
		}
		hdr := &tar.Header{
			Name: e.name, Typeflag: typeflag, Linkname: e.linkname,
			Size: int64(len(e.body)), Mode: 0o644,
			// archive consumers chown to the header's uid/gid; use this
			// process's own so extraction doesn't need root.
			Uid: os.Getuid(), Gid: os.Getgid(),
		}
		if typeflag == tar.TypeDir {
			hdr.Mode = 0o755
			hdr.Size = 0
		}
		require.NoError(t, tw.WriteHeader(hdr))
		if typeflag == tar.TypeReg && len(e.body) > 0 {
			_, err := tw.Write([]byte(e.body))
			require.NoError(t, err)
		}
	}
	require.NoError(t, tw.Close())

	sum := sha256.Sum256(buf.Bytes())
	diffID := ggcrv1.Hash{Algorithm: "sha256", Hex: hex.EncodeToString(sum[:])}
	layer, err := partial.UncompressedToLayer(&uncompressedLayer{content: buf.Bytes(), diffID: diffID})
	require.NoError(t, err)
	return layer
}

func buildImage(t *testing.T, layers ...[]tarEntry) ggcrv1.Image {
	t.Helper()
	img := empty.Image
	for _, entries := range layers {
		var err error
		img, err = mutate.Append(img, mutate.Addendum{Layer: buildLayer(t, entries)})
		require.NoError(t, err)
	}
	return img
}

func TestUnpackMergesLayers(t *testing.T) {
	img := buildImage(t,
		[]tarEntry{{name: "etc/hostname", body: "sandbox\n"}},
		[]tarEntry{{name: "etc/hosts", body: "127.0.0.1 localhost\n"}},
	)
	root := t.TempDir()
	require.NoError(t, Unpack(context.Background(), img, root))

	got, err := os.ReadFile(filepath.Join(root, "etc/hostname"))
	require.NoError(t, err)
	assert.Equal(t, "sandbox\n", string(got))

	got, err = os.ReadFile(filepath.Join(root, "etc/hosts"))
	require.NoError(t, err)
	assert.Equal(t, "127.0.0.1 localhost\n", string(got))
}

func TestUnpackWhiteoutRemovesLowerFile(t *testing.T) {
	img := buildImage(t,
		[]tarEntry{
			{name: "var/log/a.log", body: "lower"},
			{name: "var/log/b.log", body: "keep"},
		},
		[]tarEntry{{name: "var/log/.wh.a.log"}},
	)
	root := t.TempDir()
	require.NoError(t, Unpack(context.Background(), img, root))
	assert.NoFileExists(t, filepath.Join(root, "var/log/a.log"))
	assert.FileExists(t, filepath.Join(root, "var/log/b.log"))
}

func TestUnpackOpaqueDirHidesLowerSiblings(t *testing.T) {
	img := buildImage(t,
		[]tarEntry{
			{name: "data/old1", body: "old1"},
			{name: "data/old2", body: "old2"},
		},
		[]tarEntry{
			{name: "data/.wh..wh..opq"},
			{name: "data/new", body: "new"},
		},
	)
	root := t.TempDir()
	require.NoError(t, Unpack(context.Background(), img, root))
	assert.NoFileExists(t, filepath.Join(root, "data/old1"))
	assert.NoFileExists(t, filepath.Join(root, "data/old2"))
	got, err := os.ReadFile(filepath.Join(root, "data/new"))
	require.NoError(t, err)
	assert.Equal(t, "new", string(got))
}

func TestUnpackSymlink(t *testing.T) {
	img := buildImage(t, []tarEntry{
		{name: "bin/sh", typeflag: tar.TypeSymlink, linkname: "busybox"},
	})
	root := t.TempDir()
	require.NoError(t, Unpack(context.Background(), img, root))
	link, err := os.Readlink(filepath.Join(root, "bin/sh"))
	require.NoError(t, err)
	assert.Equal(t, "busybox", link)
}

func TestUnpackHardlinkSameLayer(t *testing.T) {
	img := buildImage(t, []tarEntry{
		{name: "bin/busybox", body: "#!binary"},
		{name: "bin/sh", typeflag: tar.TypeLink, linkname: "bin/busybox"},
	})
	root := t.TempDir()
	require.NoError(t, Unpack(context.Background(), img, root))
	shFI, err := os.Stat(filepath.Join(root, "bin/sh"))
	require.NoError(t, err)
	busyboxFI, err := os.Stat(filepath.Join(root, "bin/busybox"))
	require.NoError(t, err)
	assert.True(t, os.SameFile(shFI, busyboxFI), "bin/sh and bin/busybox are not the same inode")
}

func TestUnpackCrossLayerHardlinkResolvesAfterAllLayers(t *testing.T) {
	// mutate.Extract emits layers newest-first, so the layer with the
	// hardlink (written first below, but logically newer) is emitted
	// before the layer with its target - exercising the pendingLink
	// retry path.
	img := buildImage(t,
		[]tarEntry{{name: "bin/busybox", body: "#!binary"}},
		[]tarEntry{{name: "bin/sh", typeflag: tar.TypeLink, linkname: "bin/busybox"}},
	)
	root := t.TempDir()
	require.NoError(t, Unpack(context.Background(), img, root))
	shFI, err := os.Stat(filepath.Join(root, "bin/sh"))
	require.NoError(t, err)
	busyboxFI, err := os.Stat(filepath.Join(root, "bin/busybox"))
	require.NoError(t, err)
	assert.True(t, os.SameFile(shFI, busyboxFI), "bin/sh and bin/busybox are not the same inode")
}

func TestCleanTarPath(t *testing.T) {
	cases := map[string]string{
		"foo/bar":       "foo/bar",
		"/foo/bar":      "foo/bar",
		"./foo/bar":     "foo/bar",
		"foo/../bar":    "bar",
		".":             "",
		"/":             "",
		"../../etc":     "../../etc",
		"foo/../../bar": "../bar",
	}
	for in, want := range cases {
		assert.Equal(t, want, cleanTarPath(in), "cleanTarPath(%q)", in)
	}
}

func TestSecureJoinRejectsEscape(t *testing.T) {
	for _, in := range []string{"../../etc/passwd", "../escape"} {
		_, err := secureJoin("/rootfs", in)
		assert.Error(t, err, "secureJoin(%q)", in)
	}
	got, err := secureJoin("/rootfs", "foo/bar")
	require.NoError(t, err)
	assert.Equal(t, "/rootfs/foo/bar", got)
}
