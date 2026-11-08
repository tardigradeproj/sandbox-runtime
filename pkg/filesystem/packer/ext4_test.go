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

package packer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fakeMkfsExt4(t *testing.T, argsFile, failMsg string) string {
	t.Helper()
	script := filepath.Join(t.TempDir(), "fake-mkfs.ext4")
	body := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + shQuote(argsFile) + "\n"
	if failMsg != "" {
		body += "echo " + shQuote(failMsg) + " >&2\nexit 1\n"
	} else {
		body += "exit 0\n"
	}
	require.NoError(t, os.WriteFile(script, []byte(body), 0o755))
	return script
}

func writeTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	}
}

func TestMkfsExt4PackerArgs(t *testing.T) {
	argsFile := filepath.Join(t.TempDir(), "args.txt")
	bin := fakeMkfsExt4(t, argsFile, "")

	srcDir := t.TempDir()
	writeTree(t, srcDir, map[string]string{"hello.txt": "hi\n"})
	imagePath := filepath.Join(t.TempDir(), "rootfs.ext4")

	packer := MkfsExt4Packer{Bin: bin}
	require.NoError(t, packer.Pack(context.Background(), srcDir, imagePath))

	got, err := os.ReadFile(argsFile)
	require.NoError(t, err)
	args := strings.Fields(string(got))

	require.Len(t, args, 7)
	assert.Equal(t, []string{"-F", "-d", srcDir, "-N"}, args[:4])
	assert.Equal(t, imagePath, args[5])
	assert.Regexp(t, `^\d+K$`, args[6])
}

func TestMkfsExt4PackerExtraArgs(t *testing.T) {
	argsFile := filepath.Join(t.TempDir(), "args.txt")
	bin := fakeMkfsExt4(t, argsFile, "")

	srcDir := t.TempDir()
	imagePath := filepath.Join(t.TempDir(), "rootfs.ext4")

	packer := MkfsExt4Packer{Bin: bin, Options: Ext4PackOptions{ExtraMkfsArgs: []string{"-O", "^has_journal"}}}
	require.NoError(t, packer.Pack(context.Background(), srcDir, imagePath))

	got, err := os.ReadFile(argsFile)
	require.NoError(t, err)
	args := strings.Fields(string(got))

	// -F -d <srcDir> -N <count> -O ^has_journal <imagePath> <size>K
	require.Len(t, args, 9)
	assert.Equal(t, []string{"-O", "^has_journal"}, args[5:7])
}

func TestMkfsExt4PackerFailureIncludesStderr(t *testing.T) {
	argsFile := filepath.Join(t.TempDir(), "args.txt")
	bin := fakeMkfsExt4(t, argsFile, "mkfs.ext4: no space left on device")

	packer := MkfsExt4Packer{Bin: bin}
	err := packer.Pack(context.Background(), t.TempDir(), filepath.Join(t.TempDir(), "rootfs.ext4"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no space left on device")
}

func TestMeasureExt4ImageSizeAndInodes(t *testing.T) {
	srcDir := t.TempDir()
	writeTree(t, srcDir, map[string]string{
		"a.txt":        strings.Repeat("x", 1000),
		"dir/b.txt":    strings.Repeat("y", 2000),
		"dir/sub/c.go": "package x\n",
	})

	size, inodes, err := measureExt4Image(srcDir, Ext4PackOptions{})
	require.NoError(t, err)

	// Small tree: floored at the default minimum size.
	assert.Equal(t, int64(defaultMinSizeBytes), size)
	// srcDir itself + dir + dir/sub + 3 files = 6 entries, plus headroom.
	assert.Equal(t, int64(6+inodeHeadroom), inodes)
}

func TestMeasureExt4ImageSizeAboveMinimum(t *testing.T) {
	srcDir := t.TempDir()
	fileSize := int64(defaultMinSizeBytes)
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "big.bin"), make([]byte, fileSize), 0o644))

	size, _, err := measureExt4Image(srcDir, Ext4PackOptions{})
	require.NoError(t, err)

	want := int64(float64(fileSize) * (1 + defaultSlackFraction))
	assert.Equal(t, want, size)
}

func TestMeasureExt4ImageCustomOptions(t *testing.T) {
	srcDir := t.TempDir()
	const fileSize = 10 << 20
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "f.bin"), make([]byte, fileSize), 0o644))

	opts := Ext4PackOptions{SlackFraction: 0.5, MinSizeBytes: 1 << 20}
	size, _, err := measureExt4Image(srcDir, opts)
	require.NoError(t, err)

	want := int64(fileSize * 1.5)
	assert.Equal(t, want, size)
}

func TestMeasureExt4ImageMissingDir(t *testing.T) {
	_, _, err := measureExt4Image(filepath.Join(t.TempDir(), "does-not-exist"), Ext4PackOptions{})
	assert.Error(t, err)
}
