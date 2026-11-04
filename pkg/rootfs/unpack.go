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
	"archive/tar"
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	ggcrv1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
)

// pendingLink is a hardlink whose target wasn't on disk yet when
// encountered. mutate.Extract emits layers newest-first, so a hardlink
// added in a newer layer that points at a file only created in an older
// layer appears before its target in the merged stream; it's retried
// once the whole stream has been read.
type pendingLink struct {
	target     string
	linkTarget string
}

// Unpack flattens img's layers - resolving OCI whiteouts via
// mutate.Extract - and extracts the merged result onto root.
func Unpack(ctx context.Context, img ggcrv1.Image, root string) error {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return fmt.Errorf("creating rootfs directory: %w", err)
	}

	rc := mutate.Extract(img)
	defer rc.Close()

	var pending []pendingLink
	tr := tar.NewReader(rc)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("reading rootfs tar: %w", err)
		}

		name := cleanTarPath(hdr.Name)
		if name == "" {
			continue
		}
		target, err := secureJoin(root, name)
		if err != nil {
			return err
		}

		p, err := writeEntry(root, target, hdr, tr)
		if err != nil {
			return fmt.Errorf("extracting %q: %w", name, err)
		}
		if p != nil {
			pending = append(pending, *p)
		}
	}

	for _, p := range pending {
		if err := os.Link(p.linkTarget, p.target); err != nil {
			return fmt.Errorf("resolving hardlink %s -> %s: %w", p.target, p.linkTarget, err)
		}
	}
	return nil
}

func writeEntry(root, target string, hdr *tar.Header, tr *tar.Reader) (*pendingLink, error) {
	if err := os.MkdirAll(path.Dir(target), 0o755); err != nil {
		return nil, err
	}

	var pending *pendingLink

	switch hdr.Typeflag {
	case tar.TypeDir:
		if err := ensureDir(target); err != nil {
			return nil, err
		}
		if err := os.Chmod(target, hdr.FileInfo().Mode()); err != nil {
			return nil, err
		}

	case tar.TypeReg:
		if err := os.RemoveAll(target); err != nil {
			return nil, err
		}
		f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, hdr.FileInfo().Mode())
		if err != nil {
			return nil, err
		}
		_, copyErr := io.CopyN(f, tr, hdr.Size)
		closeErr := f.Close()
		if copyErr != nil {
			return nil, copyErr
		}
		if closeErr != nil {
			return nil, closeErr
		}

	case tar.TypeSymlink:
		if err := os.RemoveAll(target); err != nil {
			return nil, err
		}
		if err := os.Symlink(hdr.Linkname, target); err != nil {
			return nil, err
		}

	case tar.TypeLink:
		linkTarget, err := secureJoin(root, cleanTarPath(hdr.Linkname))
		if err != nil {
			return nil, err
		}
		if err := os.RemoveAll(target); err != nil {
			return nil, err
		}
		if err := os.Link(linkTarget, target); err != nil {
			if !os.IsNotExist(err) {
				return nil, err
			}
			pending = &pendingLink{target: target, linkTarget: linkTarget}
		}

	case tar.TypeChar, tar.TypeBlock, tar.TypeFifo:
		if err := os.RemoveAll(target); err != nil {
			return nil, err
		}
		if err := mknodEntry(target, hdr); err != nil {
			return nil, err
		}

	default:
		return nil, fmt.Errorf("unsupported tar entry type %v", hdr.Typeflag)
	}

	if pending == nil {
		applyPlatformMetadata(target, hdr)
	}
	return pending, nil
}

func ensureDir(target string) error {
	if fi, err := os.Lstat(target); err == nil {
		if fi.IsDir() {
			return nil
		}
		if err := os.RemoveAll(target); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.MkdirAll(target, 0o755)
}

// cleanTarPath normalizes a tar entry name: strips any leading "/" and
// resolves "." and ".." lexically, without clamping a ".." that climbs
// above the root (so secureJoin can detect and reject it).
func cleanTarPath(name string) string {
	cleaned := path.Clean(strings.TrimPrefix(name, "/"))
	if cleaned == "." {
		return ""
	}
	return cleaned
}

// secureJoin defends against tar-slip even though mutate.Extract
// already rejects unsafe paths before they reach this stream.
func secureJoin(root, name string) (string, error) {
	if name == "" {
		return root, nil
	}
	if name == ".." || strings.HasPrefix(name, "../") {
		return "", fmt.Errorf("rootfs: path %q escapes root", name)
	}
	return path.Join(root, name), nil
}
