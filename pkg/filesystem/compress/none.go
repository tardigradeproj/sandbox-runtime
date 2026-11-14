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

import "context"

// NoneCompressor copies src to dst unchanged -- the "no compression"
// option. Shells out to cp --sparse=always rather than a plain Go
// io.Copy, so copying a sparse file (e.g. a sandbox's overlay) doesn't
// re-materialize its holes as real zero bytes.
// TODO: remove bin need use unix.SEEK_DATA & unix.SEEK_HOLE instead
type NoneCompressor struct {
	// Bin is the cp binary to invoke.
	Bin string
}

func (n NoneCompressor) Compress(ctx context.Context, src, dst string, _ ...Option) error {
	return run(ctx, n.bin(), "--sparse=always", src, dst)
}

func (n NoneCompressor) Decompress(ctx context.Context, src, dst string, _ ...Option) error {
	return run(ctx, n.bin(), "--sparse=always", src, dst)
}

func (n NoneCompressor) bin() string {
	if n.Bin == "" {
		return "cp"
	}
	return n.Bin
}
