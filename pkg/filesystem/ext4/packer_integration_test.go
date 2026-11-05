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

//go:build integration

// This file exercises the real mkfs.ext4/e2fsprogs toolchain and only
// runs on Linux with e2fsprogs installed, e.g. via `make test-integration`.

package ext4

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMkfsExt4PackerIntegration(t *testing.T) {
	srcDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "hello.txt"), []byte("hello from tardigrade\n"), 0o644))
	require.NoError(t, os.Mkdir(filepath.Join(srcDir, "etc"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "etc", "hostname"), []byte("sandbox\n"), 0o644))

	totalBytes, entryCount, err := MeasureTree(srcDir)
	require.NoError(t, err)
	sizeBytes := EstimateImageSize(totalBytes, DefaultSizeOptions)
	inodeCount := EstimateInodeCount(entryCount, DefaultSizeOptions)

	imagePath := filepath.Join(t.TempDir(), "rootfs.ext4")
	packer := MkfsExt4Packer{}
	require.NoError(t, packer.Pack(context.Background(), srcDir, imagePath, sizeBytes, inodeCount))

	out, err := exec.Command("dumpe2fs", "-h", imagePath).CombinedOutput()
	require.NoError(t, err, "dumpe2fs -h: %s", out)
	// dumpe2fs -h never prints the literal word "ext4"; the extent
	// feature is what distinguishes ext4 from ext2/ext3.
	assert.Contains(t, string(out), "extent", "dumpe2fs -h output doesn't show the ext4 extent feature")

	out, err = exec.Command("debugfs", "-R", "ls -l /", imagePath).CombinedOutput()
	require.NoError(t, err, "debugfs ls /: %s", out)
	for _, want := range []string{"hello.txt", "etc"} {
		assert.Contains(t, string(out), want)
	}
}
