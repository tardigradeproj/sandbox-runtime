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

package idalloc

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenRejectsInvalidRange(t *testing.T) {
	_, err := Open(t.TempDir(), 5, 1)
	assert.Error(t, err)
}

func TestOpenCreatesDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "ids")
	_, err := Open(dir, 1, 10)
	require.NoError(t, err)
	info, err := os.Stat(dir)
	require.NoError(t, err)
	assert.True(t, info.IsDir())
}

func TestAllocateReleaseRoundTrip(t *testing.T) {
	a, err := Open(t.TempDir(), 100, 102)
	require.NoError(t, err)

	id, err := a.Allocate("sandbox-1")
	require.NoError(t, err)
	assert.Equal(t, 100, id)

	require.NoError(t, a.Release(id))

	// Released, so the next allocation can reuse it.
	id2, err := a.Allocate("sandbox-2")
	require.NoError(t, err)
	assert.Equal(t, 100, id2)
}

func TestAllocateSkipsHeldIds(t *testing.T) {
	a, err := Open(t.TempDir(), 1, 3)
	require.NoError(t, err)

	first, err := a.Allocate("a")
	require.NoError(t, err)
	second, err := a.Allocate("b")
	require.NoError(t, err)
	assert.NotEqual(t, first, second)
}

func TestAllocateExhaustion(t *testing.T) {
	a, err := Open(t.TempDir(), 1, 2)
	require.NoError(t, err)

	_, err = a.Allocate("a")
	require.NoError(t, err)
	_, err = a.Allocate("b")
	require.NoError(t, err)

	_, err = a.Allocate("c")
	assert.Error(t, err)
}

func TestReleaseUnallocatedIsNotAnError(t *testing.T) {
	a, err := Open(t.TempDir(), 1, 10)
	require.NoError(t, err)
	assert.NoError(t, a.Release(7))
}

func TestAllocationSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	a, err := Open(dir, 1, 2)
	require.NoError(t, err)

	id, err := a.Allocate("sandbox-1")
	require.NoError(t, err)
	require.Equal(t, 1, id)

	// Simulate a daemon restart: a fresh Allocator over the same dir
	// must still see id 1 as held.
	reopened, err := Open(dir, 1, 2)
	require.NoError(t, err)

	id2, err := reopened.Allocate("sandbox-2")
	require.NoError(t, err)
	assert.Equal(t, 2, id2)

	_, err = reopened.Allocate("sandbox-3")
	assert.Error(t, err)
}

func TestConcurrentAllocateNeverDuplicates(t *testing.T) {
	const n = 50
	a, err := Open(t.TempDir(), 1, n)
	require.NoError(t, err)

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		seen     = map[int]bool{}
		dupFound bool
	)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id, err := a.Allocate(fmt.Sprintf("owner-%d", i))
			if err != nil {
				return
			}
			mu.Lock()
			if seen[id] {
				dupFound = true
			}
			seen[id] = true
			mu.Unlock()
		}(i)
	}
	wg.Wait()

	assert.False(t, dupFound, "allocate handed out a duplicate id")
	assert.Len(t, seen, n)
}
