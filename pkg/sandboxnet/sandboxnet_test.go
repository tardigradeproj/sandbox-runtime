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

//go:build linux

package sandboxnet

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// requireSupport skips the test if the environment can't create
// network namespaces (needs CAP_SYS_ADMIN).
func requireSupport(t *testing.T) {
	t.Helper()
	const probe = "sandboxnet-probe"
	if _, err := Create(probe); err != nil {
		t.Skipf("network namespaces unavailable: %v", err)
	}
	require.NoError(t, Delete(probe))
}

func TestCreateAndDelete(t *testing.T) {
	requireSupport(t)

	const name = "test-sandboxnet"
	path, err := Create(name)
	require.NoError(t, err)

	assert.Equal(t, filepath.Join(Dir, name), path)
	_, err = os.Stat(path)
	assert.NoError(t, err, "expected the namespace's bind-mounted path to exist")

	require.NoError(t, Delete(name))
	_, err = os.Stat(path)
	assert.True(t, os.IsNotExist(err), "expected the namespace's path to be gone after Delete")
}

func TestCreateDuplicateFails(t *testing.T) {
	requireSupport(t)

	const name = "test-sandboxnet-dup"
	_, err := Create(name)
	require.NoError(t, err)
	defer Delete(name)

	_, err = Create(name)
	assert.Error(t, err)
}

func TestDeleteUnknownFails(t *testing.T) {
	requireSupport(t)

	err := Delete("does-not-exist")
	assert.Error(t, err)
}
