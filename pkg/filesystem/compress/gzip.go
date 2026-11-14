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
	"compress/gzip"
	"context"
	"fmt"
	"io"
)

// GzipCompressor compresses/decompresses using the standard library's
// gzip implementation.
type GzipCompressor struct{}

func (g GzipCompressor) Compress(_ context.Context, src, dst string, opts ...Option) error {
	o := resolveOptions(opts)
	level := gzip.DefaultCompression
	if o.level != nil {
		level = *o.level
	}

	in, out, err := openPair(src, dst)
	if err != nil {
		return err
	}
	defer in.Close()
	defer out.Close()

	w, err := gzip.NewWriterLevel(out, level)
	if err != nil {
		return fmt.Errorf("compress: gzip level %d: %w", level, err)
	}
	if _, err := io.Copy(w, in); err != nil {
		return fmt.Errorf("compress: gzip compress %s: %w", src, err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("compress: gzip compress %s: %w", src, err)
	}
	return out.Close()
}

func (g GzipCompressor) Decompress(ctx context.Context, src, dst string, _ ...Option) error {
	in, out, err := openPair(src, dst)
	if err != nil {
		return err
	}
	defer in.Close()
	defer out.Close()

	r, err := gzip.NewReader(in)
	if err != nil {
		return fmt.Errorf("compress: gzip decompress %s: %w", src, err)
	}
	defer r.Close()

	if _, err := io.Copy(out, r); err != nil {
		return fmt.Errorf("compress: gzip decompress %s: %w", src, err)
	}
	return out.Close()
}
