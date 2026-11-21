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

package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListenUnixCreatesSocket(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "test.sock")

	isTCP, lis, err := listen("unix://" + sockPath)
	require.NoError(t, err)
	defer lis.Close()
	assert.False(t, isTCP)

	info, err := os.Stat(sockPath)
	require.NoError(t, err)
	assert.NotZero(t, info.Mode()&os.ModeSocket, "expected a unix socket at %s", sockPath)
}

func TestListenUnixRemovesStaleSocket(t *testing.T) {
	// A short, dedicated dir rather than t.TempDir(): unix socket paths
	// are capped at ~104 bytes (sockaddr_un.sun_path), which
	// t.TempDir()'s long, test-name-qualified path can exceed.
	dir, err := os.MkdirTemp("", "srv")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(dir) })

	sockPath := filepath.Join(dir, "stale.sock")
	require.NoError(t, os.WriteFile(sockPath, []byte("not a socket"), 0o644))

	isTCP, lis, err := listen("unix://" + sockPath)
	require.NoError(t, err)
	defer lis.Close()
	assert.False(t, isTCP)

	info, err := os.Stat(sockPath)
	require.NoError(t, err)
	assert.NotZero(t, info.Mode()&os.ModeSocket, "expected the stale file to be replaced with a real socket")
}

func TestListenTCP(t *testing.T) {
	isTCP, lis, err := listen("127.0.0.1:0")
	require.NoError(t, err)
	defer lis.Close()
	assert.True(t, isTCP)
	assert.Contains(t, lis.Addr().String(), "127.0.0.1")
}
