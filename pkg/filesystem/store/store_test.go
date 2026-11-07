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

package store

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testDigest = "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b85"

func commitFake(t *testing.T, s *Store, entry Entry) {
	t.Helper()
	_, tmpImage, cleanup, err := s.Stage()
	require.NoError(t, err)
	defer cleanup()
	require.NoError(t, os.WriteFile(tmpImage, []byte("image"), 0o644))
	require.NoError(t, s.Commit(entry, tmpImage))
}

func TestStageCommitLookup(t *testing.T) {
	s, err := Open(t.TempDir())
	require.NoError(t, err)

	extractDir, tmpImage, cleanup, err := s.Stage()
	require.NoError(t, err)
	defer cleanup()
	_, err = os.Stat(extractDir)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(tmpImage, []byte("fake erofs image"), 0o644))

	entry := Entry{Digest: testDigest, RepoTags: []string{"example.com/widget:v1"}, Size: 15}
	require.NoError(t, s.Commit(entry, tmpImage))

	got, ok, err := s.Lookup(testDigest)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, testDigest, got.Digest)
	assert.EqualValues(t, 15, got.Size)
	assert.Equal(t, []string{"example.com/widget:v1"}, got.RepoTags)
	assert.False(t, got.CreatedAt.IsZero(), "CreatedAt was not populated by Commit")

	imgPath, err := s.ImagePath(testDigest)
	require.NoError(t, err)
	data, err := os.ReadFile(imgPath)
	require.NoError(t, err)
	assert.Equal(t, "fake erofs image", string(data))
}

func TestCommitMergesRepoTagsForExistingDigest(t *testing.T) {
	s, err := Open(t.TempDir())
	require.NoError(t, err)

	commitFake(t, s, Entry{Digest: testDigest, RepoTags: []string{"example.com/widget:v1"}})
	commitFake(t, s, Entry{Digest: testDigest, RepoTags: []string{"example.com/widget:latest"}})

	got, ok, err := s.Lookup(testDigest)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Len(t, got.RepoTags, 2)
}

func TestAddRefRequiresExistingEntry(t *testing.T) {
	s, err := Open(t.TempDir())
	require.NoError(t, err)
	assert.Error(t, s.AddRef(testDigest, []string{"example.com/widget:v1"}, ""))
}

func TestAddRefMergesIntoExistingEntry(t *testing.T) {
	s, err := Open(t.TempDir())
	require.NoError(t, err)
	commitFake(t, s, Entry{Digest: testDigest})

	require.NoError(t, s.AddRef(testDigest, []string{"example.com/widget:v2"}, "example.com/widget@"+testDigest))

	got, ok, err := s.LookupByRef("example.com/widget:v2")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, testDigest, got.Digest)

	_, ok, err = s.LookupByRef("example.com/widget@" + testDigest)
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestAddRefAcceptsMultipleTags(t *testing.T) {
	s, err := Open(t.TempDir())
	require.NoError(t, err)
	commitFake(t, s, Entry{Digest: testDigest})

	require.NoError(t, s.AddRef(testDigest, []string{"example.com/widget:v2", "widget"}, ""))

	for _, ref := range []string{"example.com/widget:v2", "widget"} {
		got, ok, err := s.LookupByRef(ref)
		require.NoError(t, err)
		require.True(t, ok, "expected %q to resolve", ref)
		assert.Equal(t, testDigest, got.Digest)
	}
}

func TestResolveByBareDigestOrRef(t *testing.T) {
	s, err := Open(t.TempDir())
	require.NoError(t, err)
	commitFake(t, s, Entry{Digest: testDigest, RepoTags: []string{"example.com/widget:v1"}})

	_, ok, err := s.Resolve(testDigest)
	require.NoError(t, err)
	assert.True(t, ok)

	_, ok, err = s.Resolve("example.com/widget:v1")
	require.NoError(t, err)
	assert.True(t, ok)

	_, ok, err = s.Resolve("example.com/widget:missing")
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestRemove(t *testing.T) {
	s, err := Open(t.TempDir())
	require.NoError(t, err)
	commitFake(t, s, Entry{Digest: testDigest})

	require.NoError(t, s.Remove(testDigest))
	_, ok, err := s.Lookup(testDigest)
	require.NoError(t, err)
	assert.False(t, ok)

	assert.NoError(t, s.Remove(testDigest), "Remove (second time)")
}

func TestList(t *testing.T) {
	s, err := Open(t.TempDir())
	require.NoError(t, err)
	entries, err := s.List()
	require.NoError(t, err)
	assert.Empty(t, entries)

	digests := []string{
		"sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b85",
		"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	for _, d := range digests {
		commitFake(t, s, Entry{Digest: d})
	}

	entries, err = s.List()
	require.NoError(t, err)
	assert.Len(t, entries, 2)
}

func TestOpenSweepsStaleTmp(t *testing.T) {
	root := t.TempDir()
	s, err := Open(root)
	require.NoError(t, err)
	_, _, cleanup, err := s.Stage()
	require.NoError(t, err)
	defer cleanup()

	tmpEntries, err := os.ReadDir(filepath.Join(root, "tmp"))
	require.NoError(t, err)
	require.Len(t, tmpEntries, 1)
	staleDir := filepath.Join(root, "tmp", tmpEntries[0].Name())
	oldTime := time.Now().Add(-2 * time.Hour)
	require.NoError(t, os.Chtimes(staleDir, oldTime, oldTime))

	_, err = Open(root)
	require.NoError(t, err)
	assert.NoDirExists(t, staleDir)
}

func TestCommitConcurrentSameDigestIsIdempotent(t *testing.T) {
	s, err := Open(t.TempDir())
	require.NoError(t, err)

	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, tmpImage, cleanup, err := s.Stage()
			if err != nil {
				errs[i] = err
				return
			}
			defer cleanup()
			if err := os.WriteFile(tmpImage, []byte("image"), 0o644); err != nil {
				errs[i] = err
				return
			}
			errs[i] = s.Commit(Entry{Digest: testDigest}, tmpImage)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		assert.NoError(t, err, "Commit %d", i)
	}

	entries, err := s.List()
	require.NoError(t, err)
	assert.Len(t, entries, 1)
}

func TestDigestFileBaseRejectsGarbage(t *testing.T) {
	_, err := digestFileBase("not-a-digest")
	assert.Error(t, err)
	_, err = digestFileBase("../../etc/passwd")
	assert.Error(t, err)
}
