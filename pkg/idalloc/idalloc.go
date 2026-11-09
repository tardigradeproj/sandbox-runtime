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

// Package idalloc allocates unique integer ids from a fixed range,
// backed by marker files on disk.
package idalloc

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// Allocator hands out unique ids from [start, end], backed by a
// marker file per allocated id under dir so state survives a restart.
type Allocator struct {
	dir        string
	start, end int
}

// Open returns an Allocator backed by dir, which is created if it
// doesn't already exist. start and end are inclusive.
func Open(dir string, start, end int) (*Allocator, error) {
	if start > end {
		return nil, fmt.Errorf("idalloc: start %d must be <= end %d", start, end)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("idalloc: create %s: %w", dir, err)
	}
	return &Allocator{dir: dir, start: start, end: end}, nil
}

// Allocate claims the first free id in [start, end] via O_EXCL, so
// concurrent callers never race. owner is recorded in the marker file
// for debugging.
func (a *Allocator) Allocate(owner string) (int, error) {
	for id := a.start; id <= a.end; id++ {
		f, err := os.OpenFile(a.markerPath(id), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			if os.IsExist(err) {
				continue
			}
			return 0, fmt.Errorf("idalloc: claim %d: %w", id, err)
		}
		_, writeErr := f.WriteString(owner)
		closeErr := f.Close()
		if writeErr != nil || closeErr != nil {
			_ = os.Remove(a.markerPath(id))
			if writeErr != nil {
				return 0, fmt.Errorf("idalloc: write marker for %d: %w", id, writeErr)
			}
			return 0, fmt.Errorf("idalloc: close marker for %d: %w", id, closeErr)
		}
		return id, nil
	}
	return 0, fmt.Errorf("idalloc: no free id in [%d, %d]", a.start, a.end)
}

// Release returns id to the pool. Releasing an id that isn't
// currently allocated is not an error.
func (a *Allocator) Release(id int) error {
	if err := os.Remove(a.markerPath(id)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("idalloc: release %d: %w", id, err)
	}
	return nil
}

func (a *Allocator) markerPath(id int) string {
	return filepath.Join(a.dir, strconv.Itoa(id))
}
