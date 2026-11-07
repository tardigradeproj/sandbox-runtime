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

// Package store manages the runtime's local, content-addressed cache of
// pulled rootfs images.
//
// Layout under root:
//
//	<root>/images/sha256-<hex>.erofs   the packed image
//	<root>/images/sha256-<hex>.json   its metadata sidecar
//	<root>/tmp/<random>/               scratch space for in-progress pulls
package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
)

var digestPattern = regexp.MustCompile(`^[a-z0-9]+(?:[._+-][a-z0-9]+)*:[0-9A-Fa-f]+$`)

// Entry is the metadata the store keeps about one pulled rootfs image.
type Entry struct {
	Digest          string            `json:"digest"`
	RepoTags        []string          `json:"repo_tags,omitempty"`
	RepoDigests     []string          `json:"repo_digests,omitempty"`
	Size            uint64            `json:"size"`
	SpecImage       string            `json:"spec_image,omitempty"`
	SpecAnnotations map[string]string `json:"spec_annotations,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`
}

// Store manages the on-disk rootfs cache rooted at a configurable
// directory.
type Store struct {
	root string
	mu   sync.Mutex
}

// Open opens (creating if necessary) a Store rooted at root, and sweeps
// any scratch directories left behind by a crashed or killed pull.
func Open(root string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(root, "images"), 0o755); err != nil {
		return nil, fmt.Errorf("store: create images dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "tmp"), 0o755); err != nil {
		return nil, fmt.Errorf("store: create tmp dir: %w", err)
	}
	s := &Store{root: root}
	s.sweepStaleTmp()
	return s, nil
}

func (s *Store) sweepStaleTmp() {
	dir := filepath.Join(s.root, "tmp")
	des, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-1 * time.Hour)
	for _, d := range des {
		info, err := d.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		_ = os.RemoveAll(filepath.Join(dir, d.Name()))
	}
}

// ImagePath returns the path at which digest's packed image is (or
// would be) stored.
func (s *Store) ImagePath(digest string) (string, error) {
	base, err := digestFileBase(digest)
	if err != nil {
		return "", err
	}
	return filepath.Join(s.root, "images", base+".erofs"), nil
}

// Lookup returns the entry for digest, if present.
func (s *Store) Lookup(digest string) (Entry, bool, error) {
	base, err := digestFileBase(digest)
	if err != nil {
		return Entry{}, false, err
	}
	return s.readSidecar(s.sidecarPath(base))
}

// LookupByRef returns the entry whose digest, repo tags, or repo
// digests match ref.
func (s *Store) LookupByRef(ref string) (Entry, bool, error) {
	entries, err := s.List()
	if err != nil {
		return Entry{}, false, err
	}
	for _, e := range entries {
		if e.Digest == ref || slices.Contains(e.RepoTags, ref) || slices.Contains(e.RepoDigests, ref) {
			return e, true, nil
		}
	}
	return Entry{}, false, nil
}

// Resolve looks ref up as a bare digest first ("sha256:<hex>"), falling
// back to matching it against stored repo tags/digests. This lets
// callers pass either form, as RootFSSpec.image allows.
func (s *Store) Resolve(ref string) (Entry, bool, error) {
	if digestPattern.MatchString(ref) {
		if e, ok, err := s.Lookup(ref); err != nil || ok {
			return e, ok, err
		}
	}
	return s.LookupByRef(ref)
}

// Stage allocates a fresh scratch directory under root/tmp and returns
// an extraction directory, a path for the in-progress image, and a
// cleanup func the caller must defer.
func (s *Store) Stage() (extractDir, tmpImagePath string, cleanup func(), err error) {
	tmpDir, err := os.MkdirTemp(filepath.Join(s.root, "tmp"), "pull-")
	if err != nil {
		return "", "", nil, fmt.Errorf("store: stage: %w", err)
	}
	cleanup = func() { _ = os.RemoveAll(tmpDir) }

	extractDir = filepath.Join(tmpDir, "extract")
	if err := os.Mkdir(extractDir, 0o755); err != nil {
		cleanup()
		return "", "", nil, fmt.Errorf("store: stage: %w", err)
	}
	tmpImagePath = filepath.Join(tmpDir, "image.erofs")
	return extractDir, tmpImagePath, cleanup, nil
}

// Commit atomically moves tmpImagePath into place under entry.Digest
// and writes its metadata sidecar, merging RepoTags/RepoDigests into an
// existing entry for the same digest rather than clobbering it.
func (s *Store) Commit(entry Entry, tmpImagePath string) error {
	base, err := digestFileBase(entry.Digest)
	if err != nil {
		return err
	}
	imagePath := filepath.Join(s.root, "images", base+".erofs")
	if err := os.Rename(tmpImagePath, imagePath); err != nil {
		return fmt.Errorf("store: commit image: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	sidecar := s.sidecarPath(base)
	existing, ok, err := s.readSidecar(sidecar)
	if err != nil {
		return err
	}
	if ok {
		entry.RepoTags = mergeUnique(existing.RepoTags, entry.RepoTags)
		entry.RepoDigests = mergeUnique(existing.RepoDigests, entry.RepoDigests)
		if entry.CreatedAt.IsZero() {
			entry.CreatedAt = existing.CreatedAt
		}
	}
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now().UTC()
	}
	return s.writeSidecarAtomic(sidecar, entry)
}

// AddRef records that repoTags and/or repoDigest resolve to digest,
// merging into an existing entry without touching its image. digest
// must already have an entry.
func (s *Store) AddRef(digest string, repoTags []string, repoDigest string) error {
	base, err := digestFileBase(digest)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	sidecar := s.sidecarPath(base)
	entry, ok, err := s.readSidecar(sidecar)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("store: no entry for digest %q", digest)
	}
	if len(repoTags) > 0 {
		entry.RepoTags = mergeUnique(entry.RepoTags, repoTags)
	}
	if repoDigest != "" {
		entry.RepoDigests = mergeUnique(entry.RepoDigests, []string{repoDigest})
	}
	return s.writeSidecarAtomic(sidecar, entry)
}

// Remove deletes digest's image and sidecar, if present.
func (s *Store) Remove(digest string) error {
	base, err := digestFileBase(digest)
	if err != nil {
		return err
	}
	dir := filepath.Join(s.root, "images")
	if err := removeIfExists(filepath.Join(dir, base+".erofs")); err != nil {
		return err
	}
	return removeIfExists(filepath.Join(dir, base+".json"))
}

// List returns every entry currently in the store.
func (s *Store) List() ([]Entry, error) {
	dir := filepath.Join(s.root, "images")
	des, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var entries []Entry
	for _, d := range des {
		if !strings.HasSuffix(d.Name(), ".json") {
			continue
		}
		e, ok, err := s.readSidecar(filepath.Join(dir, d.Name()))
		if err != nil {
			return nil, err
		}
		if ok {
			entries = append(entries, e)
		}
	}
	return entries, nil
}

func (s *Store) sidecarPath(base string) string {
	return filepath.Join(s.root, "images", base+".json")
}

func (s *Store) readSidecar(path string) (Entry, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Entry{}, false, nil
		}
		return Entry{}, false, fmt.Errorf("store: read %s: %w", path, err)
	}
	var e Entry
	if err := json.Unmarshal(data, &e); err != nil {
		return Entry{}, false, fmt.Errorf("store: parse %s: %w", path, err)
	}
	return e, true, nil
}

func (s *Store) writeSidecarAtomic(path string, entry Entry) error {
	data, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return fmt.Errorf("store: marshal entry: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("store: write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("store: rename %s -> %s: %w", tmp, path, err)
	}
	return nil
}

// digestFileBase validates digest and returns its filesystem-safe
// filename base, e.g. "sha256:deadbeef" -> "sha256-deadbeef".
func digestFileBase(digest string) (string, error) {
	if !digestPattern.MatchString(digest) {
		return "", fmt.Errorf("store: invalid digest %q", digest)
	}
	return strings.Replace(digest, ":", "-", 1), nil
}

func removeIfExists(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func mergeUnique(a, b []string) []string {
	seen := make(map[string]bool, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, lists := range [][]string{a, b} {
		for _, v := range lists {
			if v == "" || seen[v] {
				continue
			}
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}
