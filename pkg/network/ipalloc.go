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

package network

import (
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ipAllocator hands out addresses from a CIDR, minus its network,
// broadcast, and gateway addresses, backed by marker files on disk --
// one per allocated address, filename the address's host offset,
// content the owning sandbox ID. See net-design.md.
type ipAllocator struct {
	dir            string // one marker file per allocated offset
	lastOffsetFile string // round-robin resume point
	base           net.IP // cidr's network address
	start, end     uint32 // allocatable host offsets, inclusive
	gatewayOffset  uint32 // excluded from the allocatable range
}

func newIPAllocator(cidr *net.IPNet, gateway net.IP, stateDir string) (*ipAllocator, error) {
	dir := filepath.Join(stateDir, "ips")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("ipalloc: create %s: %w", dir, err)
	}

	base := cidr.IP.Mask(cidr.Mask)
	ones, bits := cidr.Mask.Size()
	size := uint32(1) << uint(bits-ones)

	return &ipAllocator{
		dir:            dir,
		lastOffsetFile: filepath.Join(stateDir, "last_ip"),
		base:           base,
		start:          1,        // skip the network address (offset 0)
		end:            size - 2, // skip the broadcast address (last offset)
		gatewayOffset:  ipToUint32(gateway) - ipToUint32(base),
	}, nil
}

// Allocate claims the next free address for sandboxID, resuming from
// the last successfully claimed offset (wrapping around) rather than
// always scanning from the bottom of the range. A released address
// isn't reused by the very next call -- only once the scan wraps back
// around to it -- matching CNI host-local's own round-robin allocator.
func (a *ipAllocator) Allocate(sandboxID string) (net.IP, error) {
	resumeFrom := a.start
	if data, err := os.ReadFile(a.lastOffsetFile); err == nil {
		if n, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 32); err == nil {
			resumeFrom = uint32(n) + 1
		}
	}

	span := a.end - a.start + 1
	for i := uint32(0); i < span; i++ {
		offset := a.start + (resumeFrom-a.start+i)%span
		if offset == a.gatewayOffset {
			continue
		}

		f, err := os.OpenFile(a.markerPath(offset), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			if os.IsExist(err) {
				continue
			}
			return nil, fmt.Errorf("ipalloc: claim offset %d: %w", offset, err)
		}
		_, writeErr := f.WriteString(sandboxID)
		closeErr := f.Close()
		if writeErr != nil || closeErr != nil {
			_ = os.Remove(a.markerPath(offset))
			if writeErr != nil {
				return nil, fmt.Errorf("ipalloc: write marker for offset %d: %w", offset, writeErr)
			}
			return nil, fmt.Errorf("ipalloc: close marker for offset %d: %w", offset, closeErr)
		}

		// Best effort: losing a race to update this just costs the
		// next Allocate a few extra re-scanned offsets, not a
		// correctness problem.
		_ = os.WriteFile(a.lastOffsetFile, []byte(strconv.FormatUint(uint64(offset), 10)), 0o600)

		return uint32ToIP(ipToUint32(a.base) + offset), nil
	}
	return nil, fmt.Errorf("ipalloc: no free address in range")
}

// Release returns sandboxID's address to the pool. Releasing a
// sandbox that holds nothing is not an error.
func (a *ipAllocator) Release(sandboxID string) error {
	entries, err := os.ReadDir(a.dir)
	if err != nil {
		return fmt.Errorf("ipalloc: read %s: %w", a.dir, err)
	}
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(a.dir, e.Name()))
		if err != nil {
			continue
		}
		if strings.TrimSpace(string(data)) != sandboxID {
			continue
		}
		if err := os.Remove(filepath.Join(a.dir, e.Name())); err != nil {
			return fmt.Errorf("ipalloc: release %s: %w", sandboxID, err)
		}
		return nil
	}
	return nil
}

func (a *ipAllocator) markerPath(offset uint32) string {
	return filepath.Join(a.dir, strconv.FormatUint(uint64(offset), 10))
}

func ipToUint32(ip net.IP) uint32 {
	ip4 := ip.To4()
	return binary.BigEndian.Uint32(ip4)
}

func uint32ToIP(v uint32) net.IP {
	ip := make(net.IP, 4)
	binary.BigEndian.PutUint32(ip, v)
	return ip
}
