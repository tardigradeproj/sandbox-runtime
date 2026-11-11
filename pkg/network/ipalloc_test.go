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
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestAllocator(t *testing.T, cidr string) *ipAllocator {
	t.Helper()
	_, ipNet, err := net.ParseCIDR(cidr)
	require.NoError(t, err)
	gateway := uint32ToIP(ipToUint32(ipNet.IP) + 1)
	a, err := newIPAllocator(ipNet, gateway, t.TempDir())
	require.NoError(t, err)
	return a
}

func TestAllocateSkipsNetworkBroadcastAndGateway(t *testing.T) {
	// A /29 has 8 addresses: .0 (network), .1 (gateway), .2-.6
	// (allocatable), .7 (broadcast).
	a := newTestAllocator(t, "10.200.5.0/29")

	seen := map[string]bool{}
	for i := 0; i < 5; i++ {
		ip, err := a.Allocate("sandbox")
		require.NoError(t, err)
		seen[ip.String()] = true
	}

	for _, forbidden := range []string{"10.200.5.0", "10.200.5.1", "10.200.5.7"} {
		assert.False(t, seen[forbidden], "allocated the reserved address %s", forbidden)
	}
	assert.Len(t, seen, 5, "expected all 5 allocatable addresses to be distinct")

	_, err := a.Allocate("one-too-many")
	assert.Error(t, err, "range should be exhausted")
}

func TestReleaseAllowsReuse(t *testing.T) {
	a := newTestAllocator(t, "10.200.5.0/29")

	ip1, err := a.Allocate("sandbox-1")
	require.NoError(t, err)

	require.NoError(t, a.Release("sandbox-1"))

	// A /29 has 5 allocatable offsets (.2-.6, gateway .1 excluded).
	// Claim every *other* one (4 of them) so ip1's offset is the
	// only address left -- forcing the next Allocate to wrap all
	// the way around and reclaim it, rather than happening to land
	// on some other never-yet-claimed offset first.
	for i := 0; i < 4; i++ {
		_, err := a.Allocate("filler")
		require.NoError(t, err)
	}
	ipN, err := a.Allocate("sandbox-2")
	require.NoError(t, err)
	assert.Equal(t, ip1, ipN, "expected the released address to be reclaimed on wraparound")
}

func TestMarkerFileIsNamedByAddressNotOffset(t *testing.T) {
	a := newTestAllocator(t, "10.200.5.0/29")

	ip, err := a.Allocate("sandbox-1")
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(a.dir, ip.String()))
	require.NoError(t, err, "expected the marker file to be named by the address itself (e.g. %q), not a bare offset", ip.String())
	assert.Equal(t, "sandbox-1", string(data))
}

func TestReleaseUnknownSandboxIsNotAnError(t *testing.T) {
	a := newTestAllocator(t, "10.200.5.0/29")
	assert.NoError(t, a.Release("never-allocated"))
}

func TestAllocateResumesFromLastOffsetNotFromTheBottom(t *testing.T) {
	a := newTestAllocator(t, "10.200.5.0/28") // .1-.14 allocatable, gateway .1

	first, err := a.Allocate("a")
	require.NoError(t, err)
	second, err := a.Allocate("b")
	require.NoError(t, err)
	assert.NotEqual(t, first, second)

	// Releasing the first address must NOT make the next allocation
	// reuse it immediately -- that's the whole point of resuming
	// from last_ip instead of scanning from the bottom.
	require.NoError(t, a.Release("a"))
	third, err := a.Allocate("c")
	require.NoError(t, err)
	assert.NotEqual(t, first, third, "released address was reused immediately instead of on wraparound")
}

func TestAllocationSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	_, ipNet, err := net.ParseCIDR("10.200.5.0/29")
	require.NoError(t, err)
	gateway := uint32ToIP(ipToUint32(ipNet.IP) + 1)

	a, err := newIPAllocator(ipNet, gateway, dir)
	require.NoError(t, err)
	ip1, err := a.Allocate("sandbox-1")
	require.NoError(t, err)

	// Simulate a restart: a fresh allocator over the same dir must
	// still see ip1 as held and never hand it out again.
	reopened, err := newIPAllocator(ipNet, gateway, dir)
	require.NoError(t, err)
	for i := 0; i < 4; i++ {
		ip, err := reopened.Allocate("other")
		require.NoError(t, err)
		assert.NotEqual(t, ip1, ip)
	}
}
