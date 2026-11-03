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

package rootfs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"

	"github.com/containerd/containerd/v2/pkg/archive"
	ggcrv1 "github.com/google/go-containerregistry/pkg/v1"
)

// Unpack applies img's layers, in order, onto root, verifying each
// layer's uncompressed content against its config-declared DiffID.
func Unpack(ctx context.Context, img ggcrv1.Image, root string) error {
	config, err := img.ConfigFile()
	if err != nil {
		return fmt.Errorf("reading image config: %w", err)
	}

	layers, err := img.Layers()
	if err != nil {
		return fmt.Errorf("reading image layers: %w", err)
	}

	if len(layers) != len(config.RootFS.DiffIDs) {
		return fmt.Errorf(
			"layer count (%d) does not match DiffID count (%d)",
			len(layers),
			len(config.RootFS.DiffIDs),
		)
	}

	if err := os.MkdirAll(root, 0755); err != nil {
		return fmt.Errorf("creating rootfs directory: %w", err)
	}

	for i, layer := range layers {
		if err := ctx.Err(); err != nil {
			return err
		}

		if err := applyLayer(ctx, root, layer, config.RootFS.DiffIDs[i]); err != nil {
			return fmt.Errorf("applying layer %d: %w", i, err)
		}
	}

	return nil
}

func applyLayer(ctx context.Context, root string, layer ggcrv1.Layer, expected ggcrv1.Hash) error {
	reader, err := layer.Uncompressed()
	if err != nil {
		return fmt.Errorf("decompressing layer: %w", err)
	}
	defer reader.Close()

	hasher := sha256.New()
	verifiedReader := io.TeeReader(reader, hasher)

	if _, err := archive.Apply(ctx, root, verifiedReader); err != nil {
		return fmt.Errorf("applying changeset: %w", err)
	}

	// archive.Apply may stop reading before EOF (e.g. trailing padding);
	// drain the rest so hasher sees every uncompressed byte.
	if _, err := io.Copy(io.Discard, verifiedReader); err != nil {
		return fmt.Errorf("reading remaining layer data: %w", err)
	}

	actual := ggcrv1.Hash{
		Algorithm: "sha256",
		Hex:       hex.EncodeToString(hasher.Sum(nil)),
	}

	if actual != expected {
		return fmt.Errorf("DiffID mismatch: expected %s, got %s", expected, actual)
	}

	return nil
}
