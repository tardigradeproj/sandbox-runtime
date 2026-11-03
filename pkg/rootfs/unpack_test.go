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
)

func newLayer(t *testing.T, entries map[string]string) ggcrv1.Layer {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, content := range entries {
		// archive.Apply chowns extracted files to the header's
		// uid/gid; use this process's own so Apply doesn't need root.
		hdr := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(content)), Uid: os.Getuid(), Gid: os.Getgid()}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("WriteHeader(%q): %v", name, err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatalf("write %q: %v", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar writer: %v", err)
	}

	sum := sha256.Sum256(buf.Bytes())
	diffID := ggcrv1.Hash{Algorithm: "sha256", Hex: hex.EncodeToString(sum[:])}

	layer, err := partial.UncompressedToLayer(&uncompressedLayer{content: buf.Bytes(), diffID: diffID})
	if err != nil {
		t.Fatalf("UncompressedToLayer: %v", err)
	}
	return layer
}

// uncompressedLayer is a minimal partial.UncompressedLayer with explicit,
// controllable tar content (unlike random.Layer, which writes files with
// mode 0).
type uncompressedLayer struct {
	content []byte
	diffID  ggcrv1.Hash
}

func (l *uncompressedLayer) DiffID() (ggcrv1.Hash, error) {
	return l.diffID, nil
}

func (l *uncompressedLayer) Uncompressed() (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(l.content)), nil
}

func (l *uncompressedLayer) MediaType() (types.MediaType, error) {
	return types.DockerLayer, nil
}

func newImage(t *testing.T, layerEntries ...map[string]string) ggcrv1.Image {
	t.Helper()
	img := empty.Image
	for _, entries := range layerEntries {
		layer := newLayer(t, entries)
		var err error
		img, err = mutate.Append(img, mutate.Addendum{Layer: layer})
		if err != nil {
			t.Fatalf("mutate.Append: %v", err)
		}
	}
	return img
}

func TestUnpack(t *testing.T) {
	img := newImage(t,
		map[string]string{"etc/hostname": "sandbox\n"},
		map[string]string{"etc/hosts": "127.0.0.1 localhost\n"},
	)

	root := t.TempDir()
	if err := Unpack(context.Background(), img, root); err != nil {
		t.Fatalf("Unpack: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(root, "etc/hostname"))
	if err != nil {
		t.Fatalf("read etc/hostname: %v", err)
	}
	if string(got) != "sandbox\n" {
		t.Errorf("etc/hostname = %q, want %q", got, "sandbox\n")
	}

	got, err = os.ReadFile(filepath.Join(root, "etc/hosts"))
	if err != nil {
		t.Fatalf("read etc/hosts: %v", err)
	}
	if string(got) != "127.0.0.1 localhost\n" {
		t.Errorf("etc/hosts = %q, want %q", got, "127.0.0.1 localhost\n")
	}
}

func TestUnpackLayerCountMismatch(t *testing.T) {
	img := newImage(t, map[string]string{"a": "a"})

	cfg, err := img.ConfigFile()
	if err != nil {
		t.Fatalf("ConfigFile: %v", err)
	}
	cfg.RootFS.DiffIDs = nil // force a mismatch against img.Layers()
	img, err = mutate.ConfigFile(img, cfg)
	if err != nil {
		t.Fatalf("mutate.ConfigFile: %v", err)
	}

	if err := Unpack(context.Background(), img, t.TempDir()); err == nil {
		t.Fatal("Unpack: expected layer/DiffID count mismatch error, got nil")
	}
}
