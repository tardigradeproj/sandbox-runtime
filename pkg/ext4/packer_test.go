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

package ext4

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fakeMkfs(t *testing.T, argsFile, failMsg string) string {
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

func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func TestMkfsExt4PackerArgs(t *testing.T) {
	argsFile := filepath.Join(t.TempDir(), "args.txt")
	bin := fakeMkfs(t, argsFile, "")

	packer := MkfsExt4Packer{Bin: bin}
	srcDir := t.TempDir()
	imagePath := filepath.Join(t.TempDir(), "rootfs.ext4")

	require.NoError(t, packer.Pack(context.Background(), srcDir, imagePath, 10*1024*1024, 2048))

	got, err := os.ReadFile(argsFile)
	require.NoError(t, err)
	args := strings.Fields(string(got))
	want := []string{"-F", "-d", srcDir, "-N", "2048", imagePath, "10240K"}
	assert.Equal(t, want, args)
}

func TestMkfsExt4PackerExtraArgs(t *testing.T) {
	argsFile := filepath.Join(t.TempDir(), "args.txt")
	bin := fakeMkfs(t, argsFile, "")

	packer := MkfsExt4Packer{Bin: bin, Options: PackOptions{ExtraMkfsArgs: []string{"-O", "^has_journal"}}}
	srcDir := t.TempDir()
	imagePath := filepath.Join(t.TempDir(), "rootfs.ext4")

	require.NoError(t, packer.Pack(context.Background(), srcDir, imagePath, 4096, 10))

	got, err := os.ReadFile(argsFile)
	require.NoError(t, err)
	args := strings.Fields(string(got))
	want := []string{"-F", "-d", srcDir, "-N", "10", "-O", "^has_journal", imagePath, "4K"}
	assert.Equal(t, want, args)
}

func TestMkfsExt4PackerFailureIncludesStderr(t *testing.T) {
	argsFile := filepath.Join(t.TempDir(), "args.txt")
	bin := fakeMkfs(t, argsFile, "mkfs.ext4: no space left on device")

	packer := MkfsExt4Packer{Bin: bin}
	err := packer.Pack(context.Background(), t.TempDir(), filepath.Join(t.TempDir(), "rootfs.ext4"), 4096, 10)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no space left on device")
}
