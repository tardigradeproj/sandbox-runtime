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
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	v1 "github.com/tardigradeproj/sandbox-runtime/pkg/api/runtime/v1"
	"github.com/tardigradeproj/sandbox-runtime/pkg/filesystem/compress"
)

// descriptorFile holds a sandbox's config and jailer identity --
// neither is part of any single RPC call's own request, so
// CreateSandbox persists them here for CheckpointSandbox to read
// back later.
const descriptorFile = "descriptor.json"

// The overlay has no fixed extension here -- it gets the compression
// format's own (see overlayFileName).
const (
	checkpointMemFile      = "mem_file"
	checkpointSnapshotFile = "snapshot_file"
	checkpointSidecarFile  = "sidecar.json"
)

var ErrCheckpointExists = errors.New("checkpoint already exists")

// Sidecar is a checkpoint's own metadata.
type Sidecar struct {
	// ID defaults to sandbox_id when the caller left it empty.
	ID        string
	Name      string
	Config    *v1.SandboxConfig
	IPAddress string
	// UID/GID are the jailer identity the sandbox ran under -- not
	// ID, that's the checkpoint's own.
	UID       int
	GID       int
	CreatedAt time.Time
}

// checkpointDir does not create anything -- see newCheckpointDir.
func checkpointDir(dir, uid string) string {
	return filepath.Join(dir, uid)
}

// newCheckpointDir mirrors pkg/network's ErrSandboxExists pattern:
// os.Mkdir itself arbitrates "does uid already exist".
func newCheckpointDir(dir, uid string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("checkpoint: mkdir %s: %w", dir, err)
	}
	path := checkpointDir(dir, uid)
	if err := os.Mkdir(path, 0o755); err != nil {
		if os.IsExist(err) {
			return "", ErrCheckpointExists
		}
		return "", fmt.Errorf("checkpoint: mkdir %s: %w", path, err)
	}
	return path, nil
}

// writeSidecar writes atomically: temp file, then rename.
func writeSidecar(dir string, s Sidecar) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("checkpoint: marshal sidecar: %w", err)
	}
	path := filepath.Join(dir, checkpointSidecarFile)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("checkpoint: write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("checkpoint: rename %s -> %s: %w", tmp, path, err)
	}
	return nil
}

func readSidecar(dir string) (Sidecar, error) {
	path := filepath.Join(dir, checkpointSidecarFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return Sidecar{}, fmt.Errorf("checkpoint: read %s: %w", path, err)
	}
	var s Sidecar
	if err := json.Unmarshal(data, &s); err != nil {
		return Sidecar{}, fmt.Errorf("checkpoint: parse %s: %w", path, err)
	}
	return s, nil
}

type sandboxDescriptor struct {
	Config *v1.SandboxConfig
	UID    int
	GID    int
}

func writeSandboxDescriptor(sandboxDir string, d sandboxDescriptor) error {
	data, err := json.Marshal(d)
	if err != nil {
		return fmt.Errorf("checkpoint: marshal descriptor: %w", err)
	}
	path := filepath.Join(sandboxDir, descriptorFile)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("checkpoint: write %s: %w", path, err)
	}
	return nil
}

func readSandboxDescriptor(sandboxDir string) (sandboxDescriptor, error) {
	path := filepath.Join(sandboxDir, descriptorFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return sandboxDescriptor{}, fmt.Errorf("checkpoint: read %s: %w", path, err)
	}
	var d sandboxDescriptor
	if err := json.Unmarshal(data, &d); err != nil {
		return sandboxDescriptor{}, fmt.Errorf("checkpoint: parse %s: %w", path, err)
	}
	return d, nil
}

func overlayFileName(format compress.Type) string {
	switch format {
	case compress.TypeZstd:
		return "overlay.ext4.zst"
	case compress.TypeGzip:
		return "overlay.ext4.gz"
	default:
		return "overlay.ext4"
	}
}
