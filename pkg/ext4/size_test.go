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
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMeasureTree(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a"), []byte("12345"), 0o644))
	require.NoError(t, os.Mkdir(filepath.Join(dir, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sub", "b"), []byte("1234567890"), 0o644))
	require.NoError(t, os.Symlink("a", filepath.Join(dir, "link")))

	totalBytes, entryCount, err := MeasureTree(dir)
	require.NoError(t, err)
	assert.EqualValues(t, 15, totalBytes)
	assert.EqualValues(t, 4, entryCount)
}

func TestEstimateImageSizeFloor(t *testing.T) {
	opts := DefaultSizeOptions
	assert.Equal(t, opts.MinImageBytes, EstimateImageSize(1024, opts))
}

func TestEstimateImageSizeSlackAndRounding(t *testing.T) {
	opts := SizeOptions{SlackFraction: 0.20, MinImageBytes: 0}
	got := EstimateImageSize(1_000_000, opts)
	assert.Equal(t, roundUpBlock(1_200_000), got)
	assert.Zero(t, got%blockSize, "result %d is not block-aligned", got)
}

func TestEstimateInodeCountManySmallFiles(t *testing.T) {
	got := EstimateInodeCount(100_000, DefaultSizeOptions)
	assert.GreaterOrEqual(t, got, int64(100_000))
}

func TestEstimateInodeCountFloor(t *testing.T) {
	opts := DefaultSizeOptions
	got := EstimateInodeCount(0, opts)
	assert.GreaterOrEqual(t, got, opts.MinInodeHeadroom)
}
