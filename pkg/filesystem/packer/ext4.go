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
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
)

const (
	// defaultSlackFraction is the fractional overhead added on top of a
	// measured tree's size to leave room for ext4's own metadata and
	// journal.
	defaultSlackFraction = 0.2
	// defaultMinSizeBytes is a floor under the estimated image size, so
	// small trees still get enough room for ext4's fixed overhead.
	defaultMinSizeBytes = 64 << 20 // 64 MiB
	// inodeHeadroom is added on top of the measured entry count to
	// cover ext4's own reserved inodes (root, lost+found, journal,
	// etc.).
	inodeHeadroom = 16
)

// Ext4PackOptions carries escape-hatch tuning for MkfsExt4Packer.
type Ext4PackOptions struct {
	// ExtraMkfsArgs are appended before the required positional
	// arguments, e.g. []string{"-O", "^has_journal"}.
	ExtraMkfsArgs []string
	// SlackFraction is the fractional overhead added on top of the
	// measured tree size, e.g. 0.2 for 20%. Zero uses the default.
	SlackFraction float64
	// MinSizeBytes is a floor under the estimated image size. Zero
	// uses the default.
	MinSizeBytes int64
}

// MkfsExt4Packer packs images by measuring srcDir to estimate a
// generously-sized image and inode count, then shelling out to
// mkfs.ext4 -d to format a new image file of that size directly from
// the source directory. Unlike erofs, ext4 needs the image pre-sized
// (and its inode count pre-estimated): mkfs.ext4 doesn't size the
// image automatically from -d's contents the way mkfs.erofs does.
type MkfsExt4Packer struct {
	// Bin is the mkfs.ext4 binary to invoke.
	Bin     string
	Options Ext4PackOptions
}

func (m MkfsExt4Packer) Pack(ctx context.Context, srcDir, imagePath string) error {
	bin := m.Bin
	if bin == "" {
		bin = "mkfs.ext4"
	}

	sizeBytes, inodeCount, err := measureExt4Image(srcDir, m.Options)
	if err != nil {
		return fmt.Errorf("ext4: measure %s: %w", srcDir, err)
	}

	if err := os.MkdirAll(filepath.Dir(imagePath), 0o755); err != nil {
		return fmt.Errorf("ext4: mkdir %s: %w", filepath.Dir(imagePath), err)
	}

	// mkfs.ext4 creates imagePath itself (as a sparse file) sized by
	// the trailing size argument, in KiB per the "K" suffix.
	args := []string{"-F", "-d", srcDir, "-N", strconv.FormatInt(inodeCount, 10)}
	args = append(args, m.Options.ExtraMkfsArgs...)
	args = append(args, imagePath, fmt.Sprintf("%dK", sizeBytes/1024))

	cmd := exec.CommandContext(ctx, bin, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ext4: %s %v: %w: %s", bin, args, err, stderr.String())
	}
	return nil
}

// measureExt4Image walks srcDir and returns an image size (in bytes)
// and inode count comfortably sized to hold it: size gets a slack
// fraction added on top of the measured content size (floored at a
// minimum) to cover ext4's own metadata/journal overhead, and inode
// count is sized directly from the number of filesystem entries found
// -- not guessed from a bytes-per-inode ratio -- so file-heavy trees
// don't exhaust inodes before they exhaust space.
func measureExt4Image(srcDir string, opts Ext4PackOptions) (sizeBytes, inodeCount int64, err error) {
	var totalSize, entries int64
	err = filepath.WalkDir(srcDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		entries++
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			totalSize += info.Size()
		}
		return nil
	})
	if err != nil {
		return 0, 0, err
	}

	slack := opts.SlackFraction
	if slack <= 0 {
		slack = defaultSlackFraction
	}
	minSize := opts.MinSizeBytes
	if minSize <= 0 {
		minSize = defaultMinSizeBytes
	}

	sizeBytes = max(int64(float64(totalSize)*(1+slack)), minSize)

	inodeCount = entries + inodeHeadroom
	return sizeBytes, inodeCount, nil
}
