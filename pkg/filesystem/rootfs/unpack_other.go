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

//go:build !linux

package rootfs

import (
	"archive/tar"
	"fmt"
	"runtime"
)

// mknodEntry is unsupported outside Linux: this runtime only targets
// Linux in production (it already needs KVM/TAP access there), so
// non-Linux builds exist only for local development/testing, where
// images containing device nodes are uncommon.
func mknodEntry(target string, hdr *tar.Header) error {
	return fmt.Errorf("rootfs: device node extraction is not supported on GOOS=%s", runtime.GOOS)
}

// applyPlatformMetadata is a no-op outside Linux; see unpack_linux.go.
func applyPlatformMetadata(target string, hdr *tar.Header) {}
