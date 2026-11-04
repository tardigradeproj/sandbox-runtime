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
	"io/fs"
	"path/filepath"
)

const blockSize = 4096

// SizeOptions controls how EstimateImageSize and EstimateInodeCount turn
// a measured tree into mkfs.ext4 parameters.
type SizeOptions struct {
	SlackFraction    float64
	MinImageBytes    int64
	InodeMultiplier  float64
	MinInodeHeadroom int64
}

// DefaultSizeOptions are reasonable defaults for a general-purpose Linux
// rootfs.
var DefaultSizeOptions = SizeOptions{
	SlackFraction:    0.20,
	MinImageBytes:    64 * 1024 * 1024,
	InodeMultiplier:  1.10,
	MinInodeHeadroom: 1024,
}

// MeasureTree walks dir and returns the total logical size of every
// regular file in it, and the total number of filesystem entries.
func MeasureTree(dir string) (totalBytes int64, entryCount int64, err error) {
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == dir {
			return nil
		}
		entryCount++
		if d.Type().IsRegular() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			totalBytes += info.Size()
		}
		return nil
	})
	return totalBytes, entryCount, err
}

// EstimateImageSize returns the mkfs.ext4 target size, in bytes, rounded
// up to a 4096-byte block boundary.
func EstimateImageSize(totalBytes int64, opts SizeOptions) int64 {
	size := int64(float64(totalBytes) * (1 + opts.SlackFraction))
	return roundUpBlock(max(size, opts.MinImageBytes))
}

// EstimateInodeCount returns the -N value to pass to mkfs.ext4, sized
// directly from the measured entry count rather than guessed via a
// bytes-per-inode ratio, so file-heavy trees don't run out of inodes
// before running out of space.
func EstimateInodeCount(entryCount int64, opts SizeOptions) int64 {
	n := int64(float64(entryCount)*opts.InodeMultiplier) + opts.MinInodeHeadroom
	return max(n, opts.MinInodeHeadroom)
}

func roundUpBlock(n int64) int64 {
	if rem := n % blockSize; rem != 0 {
		n += blockSize - rem
	}
	return n
}
