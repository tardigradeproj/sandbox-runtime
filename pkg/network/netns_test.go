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

package network

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateDeleteNetns(t *testing.T) {
	requireSupport(t)

	const name = "test-netns"
	path, err := createNetns(name)
	require.NoError(t, err)

	assert.Equal(t, filepath.Join(netnsDir, name), path)
	_, err = os.Stat(path)
	assert.NoError(t, err, "expected the namespace's bind-mounted path to exist")

	require.NoError(t, deleteNetns(name))
	_, err = os.Stat(path)
	assert.True(t, os.IsNotExist(err), "expected the namespace's path to be gone after delete")
}

func TestCreateNetnsDuplicateFails(t *testing.T) {
	requireSupport(t)

	const name = "test-netns-dup"
	_, err := createNetns(name)
	require.NoError(t, err)
	defer deleteNetns(name)

	_, err = createNetns(name)
	assert.Error(t, err)
}

func TestDeleteNetnsUnknownFails(t *testing.T) {
	requireSupport(t)
	assert.Error(t, deleteNetns("does-not-exist"))
}
