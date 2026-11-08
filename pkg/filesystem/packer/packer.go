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

// Package packer builds filesystem images for pulled rootfs content.
// It supports multiple on-disk formats (see Type) behind a single
// Packer interface, so callers can select a format via configuration
// without caring how any one of them is actually produced.
package packer

import (
	"context"
	"fmt"
)

// Packer builds a filesystem image from a populated directory.
type Packer interface {
	Pack(ctx context.Context, srcDir, imagePath string) error
}

// Type identifies which on-disk filesystem format a Packer produces.
type Type string

const (
	// TypeErofs packs a read-only EROFS image via mkfs.erofs.
	TypeErofs Type = "erofs"
	// TypeExt4 packs a read-write ext4 image via mkfs.ext4.
	TypeExt4 Type = "ext4"
)

// New constructs a Packer for typ, using that format's default binary
// name and options. An empty typ defaults to TypeErofs.
func New(typ Type) (Packer, error) {
	switch typ {
	case "", TypeErofs:
		return MkfsErofsPacker{}, nil
	case TypeExt4:
		return MkfsExt4Packer{}, nil
	default:
		return nil, fmt.Errorf("packer: unknown type %q", typ)
	}
}
