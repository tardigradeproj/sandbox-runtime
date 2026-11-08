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

func fakeMkfs(t *testing.T, argsFile, failMsg string) string {
	t.Helper()
	script := filepath.Join(t.TempDir(), "fake-mkfs.erofs")
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

func TestMkfsErofsPackerArgs(t *testing.T) {
	argsFile := filepath.Join(t.TempDir(), "args.txt")
	bin := fakeMkfs(t, argsFile, "")

	packer := MkfsErofsPacker{Bin: bin}
	srcDir := t.TempDir()
	imagePath := filepath.Join(t.TempDir(), "rootfs.erofs")

	require.NoError(t, packer.Pack(context.Background(), srcDir, imagePath))

	got, err := os.ReadFile(argsFile)
	require.NoError(t, err)
	args := strings.Fields(string(got))
	want := []string{imagePath, srcDir}
	assert.Equal(t, want, args)
}

func TestMkfsErofsPackerExtraArgs(t *testing.T) {
	argsFile := filepath.Join(t.TempDir(), "args.txt")
	bin := fakeMkfs(t, argsFile, "")

	packer := MkfsErofsPacker{Bin: bin, Options: PackOptions{ExtraMkfsArgs: []string{"-zlz4hc,9"}}}
	srcDir := t.TempDir()
	imagePath := filepath.Join(t.TempDir(), "rootfs.erofs")

	require.NoError(t, packer.Pack(context.Background(), srcDir, imagePath))

	got, err := os.ReadFile(argsFile)
	require.NoError(t, err)
	args := strings.Fields(string(got))
	want := []string{"-zlz4hc,9", imagePath, srcDir}
	assert.Equal(t, want, args)
}

func TestMkfsErofsPackerFailureIncludesStderr(t *testing.T) {
	argsFile := filepath.Join(t.TempDir(), "args.txt")
	bin := fakeMkfs(t, argsFile, "mkfs.erofs: no space left on device")

	packer := MkfsErofsPacker{Bin: bin}
	err := packer.Pack(context.Background(), t.TempDir(), filepath.Join(t.TempDir(), "rootfs.erofs"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no space left on device")
}
