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

// Package oci pulls OCI images from a registry using
// github.com/google/go-containerregistry.
package oci

import (
	"context"
	"errors"
	"fmt"
	"runtime"

	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/name"
	ggcrv1 "github.com/google/go-containerregistry/pkg/v1"
)

// ErrInvalidReference distinguishes a bad image reference (caller
// mistake) from a registry-side failure.
var ErrInvalidReference = errors.New("oci: invalid image reference")

func PullImage(ctx context.Context, image string, opts ...crane.Option) (ggcrv1.Image, error) {
	if _, err := name.ParseReference(image); err != nil {
		return nil, fmt.Errorf("%w: %q: %v", ErrInvalidReference, image, err)
	}

	platform := hostPlatform()
	allOpts := append([]crane.Option{crane.WithContext(ctx), crane.WithPlatform(&platform)}, opts...)
	img, err := crane.Pull(image, allOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to pull manifest for %q: %w", image, err)
	}
	return img, nil
}

func hostPlatform() ggcrv1.Platform {
	return ggcrv1.Platform{OS: "linux", Architecture: runtime.GOARCH}
}
