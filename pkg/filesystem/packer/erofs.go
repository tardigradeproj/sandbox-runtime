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
	"os/exec"
)

// PackOptions carries escape-hatch tuning for MkfsErofsPacker.
type PackOptions struct {
	// ExtraMkfsArgs are appended before the required positional
	// arguments, e.g. []string{"-zlz4hc,9"} to enable compression.
	ExtraMkfsArgs []string
}

// MkfsErofsPacker packs images by shelling out to mkfs.erofs, which
// builds a read-only EROFS image directly from a source directory and
// sizes it automatically, so unlike ext4 there's no need to pre-measure
// the tree or estimate inode counts.
type MkfsErofsPacker struct {
	// Bin is the mkfs.erofs binary to invoke.
	Bin     string
	Options PackOptions
}

func (m MkfsErofsPacker) Pack(ctx context.Context, srcDir, imagePath string) error {
	bin := m.Bin
	if bin == "" {
		bin = "mkfs.erofs"
	}

	args := append([]string{}, m.Options.ExtraMkfsArgs...)
	args = append(args, imagePath, srcDir)

	cmd := exec.CommandContext(ctx, bin, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("erofs: %s %v: %w: %s", bin, args, err, stderr.String())
	}
	return nil
}
