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

// Package compress compresses/decompresses a single file. It
// supports multiple formats (see Type) behind a single Compressor
// interface, so callers can select a format via configuration
// without caring how any one of them is actually produced.
package compress

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
)

// Compressor compresses src to dst, and reverses it.
type Compressor interface {
	// Compress reads src and writes its compressed form to dst.
	Compress(ctx context.Context, src, dst string, opts ...Option) error
	// Decompress reads src and writes its decompressed form to dst.
	Decompress(ctx context.Context, src, dst string, opts ...Option) error
}

// Type identifies which compression format a Compressor produces.
type Type string

const (
	// TypeZstd compresses with zstd.
	TypeZstd Type = "zstd"
	// TypeGzip compresses with gzip.
	TypeGzip Type = "gzip"
	// TypeNone copies src to dst unchanged -- no compression.
	TypeNone Type = "none"
)

// New constructs a Compressor for typ, using that format's default
// binary name. An empty typ defaults to TypeZstd.
func New(typ Type) (Compressor, error) {
	switch typ {
	case "", TypeZstd:
		return ZstdCompressor{}, nil
	case TypeGzip:
		return GzipCompressor{}, nil
	case TypeNone:
		return NoneCompressor{}, nil
	default:
		return nil, fmt.Errorf("compress: unknown type %q", typ)
	}
}

// options holds the resolved effect of a call's Option values.
type options struct {
	// level is the compression level, meaning depends on Type (zstd:
	// 1-19, gzip: 1-9). Nil means "let the tool pick its own default".
	level *int
}

// Option configures a single Compress call.
type Option func(*options)

// WithLevel sets the compression level. Ignored by Decompress and by
// NoneCompressor.
func WithLevel(level int) Option {
	return func(o *options) { o.level = &level }
}

func resolveOptions(opts []Option) options {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

// run executes a compression helper binary, capturing stderr for the
// error message.
func run(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("compress: %s %v: %w: %s", name, args, err, stderr.String())
	}
	return nil
}

// openPair opens src for reading and creates dst for writing.
func openPair(src, dst string) (*os.File, *os.File, error) {
	in, err := os.Open(src)
	if err != nil {
		return nil, nil, fmt.Errorf("compress: open %s: %w", src, err)
	}
	out, err := os.Create(dst)
	if err != nil {
		in.Close()
		return nil, nil, fmt.Errorf("compress: create %s: %w", dst, err)
	}
	return in, out, nil
}
