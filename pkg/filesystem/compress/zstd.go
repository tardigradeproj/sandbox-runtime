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

package compress

import (
	"context"
	"fmt"
	"io"

	"github.com/klauspost/compress/zstd"
)

// ZstdCompressor compresses/decompresses using klauspost/compress's
// native Go zstd implementation.
type ZstdCompressor struct{}

func (z ZstdCompressor) Compress(ctx context.Context, src, dst string, opts ...Option) error {
	o := resolveOptions(opts)

	in, out, err := openPair(src, dst)
	if err != nil {
		return err
	}
	defer in.Close()
	defer out.Close()

	var eopts []zstd.EOption
	if o.level != nil {
		eopts = append(eopts, zstd.WithEncoderLevel(zstd.EncoderLevelFromZstd(*o.level)))
	}
	w, err := zstd.NewWriter(out, eopts...)
	if err != nil {
		return fmt.Errorf("compress: zstd writer: %w", err)
	}
	if _, err := io.Copy(w, in); err != nil {
		w.Close()
		return fmt.Errorf("compress: zstd compress %s: %w", src, err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("compress: zstd compress %s: %w", src, err)
	}
	return out.Close()
}

func (z ZstdCompressor) Decompress(ctx context.Context, src, dst string, _ ...Option) error {
	in, out, err := openPair(src, dst)
	if err != nil {
		return err
	}
	defer in.Close()
	defer out.Close()

	r, err := zstd.NewReader(in)
	if err != nil {
		return fmt.Errorf("compress: zstd decompress %s: %w", src, err)
	}
	defer r.Close()

	if _, err := io.Copy(out, r); err != nil {
		return fmt.Errorf("compress: zstd decompress %s: %w", src, err)
	}
	return out.Close()
}
