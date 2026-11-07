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

// This file exercises the real mkfs.erofs/erofs-utils toolchain and
// only runs on Linux with erofs-utils installed, e.g. via
// `make test-integration`.

package erofs

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMkfsErofsPackerIntegration(t *testing.T) {
	srcDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "hello.txt"), []byte("hello from tardigrade\n"), 0o644))
	require.NoError(t, os.Mkdir(filepath.Join(srcDir, "etc"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "etc", "hostname"), []byte("sandbox\n"), 0o644))

	imagePath := filepath.Join(t.TempDir(), "rootfs.erofs")
	packer := MkfsErofsPacker{}
	require.NoError(t, packer.Pack(context.Background(), srcDir, imagePath))

	extractDir := t.TempDir()
	out, err := exec.Command("fsck.erofs", "--extract="+extractDir, imagePath).CombinedOutput()
	require.NoError(t, err, "fsck.erofs --extract: %s", out)

	got, err := os.ReadFile(filepath.Join(extractDir, "hello.txt"))
	require.NoError(t, err)
	assert.Equal(t, "hello from tardigrade\n", string(got))

	got, err = os.ReadFile(filepath.Join(extractDir, "etc", "hostname"))
	require.NoError(t, err)
	assert.Equal(t, "sandbox\n", string(got))
}
