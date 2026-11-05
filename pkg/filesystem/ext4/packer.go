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
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
)

// Packer builds an ext4 filesystem image from a populated directory.

type Packer interface {
	Pack(ctx context.Context, srcDir, imagePath string, sizeBytes, inodeCount int64) error
}

// PackOptions carries escape-hatch tuning for MkfsExt4Packer.
type PackOptions struct {
	// ExtraMkfsArgs are appended after the required flags, e.g.
	// []string{"-O", "^has_journal"} to shrink images that don't need a
	// journal. Empty by default: the journal stays enabled.
	ExtraMkfsArgs []string
}

// MkfsExt4Packer packs images by shelling out to mkfs.ext4's "-d" flag,
// which populates the filesystem directly from a directory at creation
// time.
type MkfsExt4Packer struct {
	// Bin is the mkfs.ext4 binary to invoke.
	Bin     string
	Options PackOptions
}

func (m MkfsExt4Packer) Pack(ctx context.Context, srcDir, imagePath string, sizeBytes, inodeCount int64) error {
	bin := m.Bin
	if bin == "" {
		bin = "mkfs.ext4"
	}

	args := []string{
		"-F",
		"-d", srcDir,
		"-N", strconv.FormatInt(inodeCount, 10),
	}
	args = append(args, m.Options.ExtraMkfsArgs...)
	args = append(args, imagePath, strconv.FormatInt(sizeBytes/1024, 10)+"K")

	cmd := exec.CommandContext(ctx, bin, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ext4: %s %v: %w: %s", bin, args, err, stderr.String())
	}
	return nil
}
