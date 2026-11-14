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

package compress

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewSelectsCompressor(t *testing.T) {
	c, err := New(TypeZstd)
	require.NoError(t, err)
	assert.IsType(t, ZstdCompressor{}, c)

	c, err = New(TypeGzip)
	require.NoError(t, err)
	assert.IsType(t, GzipCompressor{}, c)

	c, err = New(TypeNone)
	require.NoError(t, err)
	assert.IsType(t, NoneCompressor{}, c)

	c, err = New("")
	require.NoError(t, err, "empty type should default to zstd")
	assert.IsType(t, ZstdCompressor{}, c)

	_, err = New("bogus")
	assert.Error(t, err)
}

func TestZstdCompressorAcceptsLevel(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	require.NoError(t, os.WriteFile(src, []byte("content"), 0o644))

	z := ZstdCompressor{}
	require.NoError(t, z.Compress(context.Background(), src, dst, WithLevel(7)))
	assert.FileExists(t, dst)
}

func TestGzipCompressorAcceptsLevel(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	require.NoError(t, os.WriteFile(src, []byte("content"), 0o644))

	g := GzipCompressor{}
	require.NoError(t, g.Compress(context.Background(), src, dst, WithLevel(9)))
	assert.FileExists(t, dst)
}

func TestNoneCompressorCopiesContent(t *testing.T) {
	requireSparseCp(t)

	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	require.NoError(t, os.WriteFile(src, []byte("sandbox overlay content"), 0o644))

	n := NoneCompressor{}
	require.NoError(t, n.Compress(context.Background(), src, dst))

	got, err := os.ReadFile(dst)
	require.NoError(t, err)
	assert.Equal(t, "sandbox overlay content", string(got))
}

func TestGzipCompressorRoundTrip(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	compressed := filepath.Join(dir, "src.gz")
	roundTripped := filepath.Join(dir, "roundtrip")
	const content = "sandbox overlay content, repeated for compression to matter. " +
		"sandbox overlay content, repeated for compression to matter."
	require.NoError(t, os.WriteFile(src, []byte(content), 0o644))

	g := GzipCompressor{}
	require.NoError(t, g.Compress(context.Background(), src, compressed, WithLevel(6)))
	require.NoError(t, g.Decompress(context.Background(), compressed, roundTripped))

	got, err := os.ReadFile(roundTripped)
	require.NoError(t, err)
	assert.Equal(t, content, string(got))
}

func TestZstdCompressorRoundTrip(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	compressed := filepath.Join(dir, "src.zst")
	roundTripped := filepath.Join(dir, "roundtrip")
	const content = "sandbox overlay content, repeated for compression to matter. " +
		"sandbox overlay content, repeated for compression to matter."
	require.NoError(t, os.WriteFile(src, []byte(content), 0o644))

	z := ZstdCompressor{}
	require.NoError(t, z.Compress(context.Background(), src, compressed, WithLevel(3)))
	require.NoError(t, z.Decompress(context.Background(), compressed, roundTripped))

	got, err := os.ReadFile(roundTripped)
	require.NoError(t, err)
	assert.Equal(t, content, string(got))
}

// requireBin skips the test if bin isn't on PATH, same spirit as the
// CAP_SYS_ADMIN skip-guards in pkg/network's tests.
func requireBin(t *testing.T, bin string) {
	t.Helper()
	if _, err := exec.LookPath(bin); err != nil {
		t.Skipf("%s not available: %v", bin, err)
	}
}

// requireSparseCp skips the test if cp doesn't understand
// --sparse=always (a GNU coreutils flag -- macOS's built-in BSD cp
// doesn't have it).
func requireSparseCp(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "probe-src")
	require.NoError(t, os.WriteFile(src, []byte("x"), 0o644))
	if err := exec.Command("cp", "--sparse=always", src, filepath.Join(dir, "probe-dst")).Run(); err != nil {
		t.Skipf("cp --sparse=always unavailable: %v", err)
	}
}
