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
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/tardigradeproj/sandbox-runtime/internal/cfg"
	internalnetwork "github.com/tardigradeproj/sandbox-runtime/internal/network"
	v1 "github.com/tardigradeproj/sandbox-runtime/pkg/api/runtime/v1"
	"github.com/tardigradeproj/sandbox-runtime/pkg/filesystem/store"
	"github.com/tardigradeproj/sandbox-runtime/pkg/idalloc"
)

// mkfsExt4Bin is the mkfs.ext4 binary to invoke; overridable in tests.
var mkfsExt4Bin = "mkfs.ext4"

// Runtime implements v1.RuntimeServiceServer. CreateSandbox/StartSandbox
// are only implemented on linux (see runtime_linux.go), since firecracker
// itself only runs there.
type Runtime struct {
	v1.UnimplementedRuntimeServiceServer
	cfg   *cfg.Settings
	store *store.Store
	// jailerIDs is set when jailer's uid_gid.mode is "on_demand".
	jailerIDs *idalloc.Allocator
	// network is set when jailer is enabled -- networking is only
	// namespace-isolated per sandbox in that case (see runtime_linux.go).
	// CreateSandbox calls it directly, same as r.store, rather than
	// over a loopback gRPC connection -- see internal/network.
	network *internalnetwork.Network
}

// NewRuntime constructs a Runtime.
func NewRuntime(cfg *cfg.Settings, store *store.Store) (*Runtime, error) {
	r := &Runtime{cfg: cfg, store: store}

	j := cfg.Runtime.Firecracker.Jailer
	if j.Enabled {
		// jailer requires --chroot-base-dir to already exist.
		if err := os.MkdirAll(j.ChrootBaseDir, 0o755); err != nil {
			return nil, fmt.Errorf("failed to create jailer chroot base dir: %w", err)
		}
		if j.UIDGID.Mode == "on_demand" {
			a, err := idalloc.Open(filepath.Join(cfg.State, "jailer-ids"), *j.UIDGID.RangeStart, *j.UIDGID.RangeEnd)
			if err != nil {
				return nil, fmt.Errorf("failed to open jailer id allocator: %w", err)
			}
			r.jailerIDs = a
		}

		n, err := internalnetwork.New(cfg)
		if err != nil {
			return nil, fmt.Errorf("failed to set up network: %w", err)
		}
		r.network = n
	}
	return r, nil
}

// createOverlayImage creates a sparse file at path and formats it
// ext4, for use as the microVM's writable overlay.
func createOverlayImage(ctx context.Context, path string, sizeBytes int64) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("failed to create %s: %w", path, err)
	}
	if err := f.Truncate(sizeBytes); err != nil {
		f.Close()
		return fmt.Errorf("failed to truncate %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("failed to close %s: %w", path, err)
	}

	cmd := exec.CommandContext(ctx, mkfsExt4Bin, "-q", "-m", "0", path)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("mkfs.ext4 %s: %w: %s", path, err, stderr.String())
	}
	return nil
}
