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

//go:build linux

package rootfs

import (
	"archive/tar"
	"strings"

	"golang.org/x/sys/unix"
)

func mknodEntry(target string, hdr *tar.Header) error {
	mode := uint32(hdr.FileInfo().Mode().Perm())
	switch hdr.Typeflag {
	case tar.TypeChar:
		mode |= unix.S_IFCHR
	case tar.TypeBlock:
		mode |= unix.S_IFBLK
	case tar.TypeFifo:
		mode |= unix.S_IFIFO
	}
	dev := unix.Mkdev(uint32(hdr.Devmajor), uint32(hdr.Devminor))
	return unix.Mknod(target, mode, int(dev))
}

// applyPlatformMetadata restores ownership and xattrs. Both are
// best-effort: this runtime needs to run as root in production anyway
// (for KVM/TAP access), so a failure here shouldn't abort an
// otherwise-successful pull during local, non-root development.
func applyPlatformMetadata(target string, hdr *tar.Header) {
	_ = unix.Lchown(target, hdr.Uid, hdr.Gid)

	const xattrPrefix = "SCHILY.xattr."
	for k, v := range hdr.PAXRecords {
		if !strings.HasPrefix(k, xattrPrefix) {
			continue
		}
		name := strings.TrimPrefix(k, xattrPrefix)
		_ = unix.Lsetxattr(target, name, []byte(v), 0)
	}
}
