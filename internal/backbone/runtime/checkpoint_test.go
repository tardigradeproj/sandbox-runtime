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

package runtime

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	v1 "github.com/tardigradeproj/sandbox-runtime/pkg/api/runtime/v1"
)

func TestNewCheckpointDirCreatesDirectory(t *testing.T) {
	dir := t.TempDir()
	path, err := newCheckpointDir(dir, "chk-1")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "chk-1"), path)
	assert.DirExists(t, path)
}

func TestNewCheckpointDirFailsIfAlreadyExists(t *testing.T) {
	dir := t.TempDir()
	_, err := newCheckpointDir(dir, "chk-1")
	require.NoError(t, err)

	_, err = newCheckpointDir(dir, "chk-1")
	require.ErrorIs(t, err, ErrCheckpointExists)
}

func TestSidecarRoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := Sidecar{
		ID:   "chk-1",
		Name: "nightly build",
		Config: &v1.SandboxConfig{
			Metadata: &v1.SandboxMetadata{Uid: "sandbox-1", Name: "sandbox"},
			Rootfs:   &v1.RootFSMount{Spec: &v1.RootFSSpec{Image: "alpine:3.19"}, IsRootDevice: true},
			Resources: &v1.SandboxResources{
				Cpu:          1,
				MemoryBytes:  512 << 20,
				StorageBytes: 256 << 20,
			},
			Labels: map[string]string{"env": "test"},
		},
		IPAddress: "10.200.5.14/24",
		UID:       100001,
		GID:       100001,
		CreatedAt: time.Now().UTC().Truncate(time.Second),
	}

	require.NoError(t, writeSidecar(dir, want))

	got, err := readSidecar(dir)
	require.NoError(t, err)

	assert.Equal(t, want.ID, got.ID)
	assert.Equal(t, want.Name, got.Name)
	assert.Equal(t, want.IPAddress, got.IPAddress)
	assert.Equal(t, want.UID, got.UID)
	assert.Equal(t, want.GID, got.GID)
	assert.True(t, want.CreatedAt.Equal(got.CreatedAt))
	require.NotNil(t, got.Config)
	assert.Equal(t, "sandbox-1", got.Config.GetMetadata().GetUid())
	assert.Equal(t, "alpine:3.19", got.Config.GetRootfs().GetSpec().GetImage())
	assert.True(t, got.Config.GetRootfs().GetIsRootDevice())
	assert.Equal(t, int64(512<<20), got.Config.GetResources().GetMemoryBytes())
	assert.Equal(t, "test", got.Config.GetLabels()["env"])
}

func TestReadSidecarMissingFile(t *testing.T) {
	_, err := readSidecar(t.TempDir())
	assert.Error(t, err)
}
