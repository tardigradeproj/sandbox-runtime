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
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// requireSupport skips the test if the environment can't create
// namespaces/bridges (needs CAP_SYS_ADMIN).
func requireSupport(t *testing.T) {
	t.Helper()
	if err := exec.Command("ip", "link", "add", "net-test-probe", "type", "dummy").Run(); err != nil {
		t.Skipf("network setup unavailable: %v", err)
	}
	_ = exec.Command("ip", "link", "delete", "net-test-probe").Run()
}

// testGuest is the GuestNetwork fixture used throughout -- same
// values cfg.Network.GuestNetwork defaults to.
var testGuest = GuestNetwork{TapIP: "169.254.100.1", GuestIP: "169.254.100.2", PrefixLen: 30}

func TestGuestIPKernelParam(t *testing.T) {
	assert.Equal(t, "ip=169.254.100.2::169.254.100.1:255.255.255.252::eth0:off", GuestIPKernelParam(testGuest))
}

func TestOpenCreatesBridgeAndNAT(t *testing.T) {
	requireSupport(t)
	ctx := context.Background()

	bridge := "testbr0"
	n, err := New(ctx, "10.250.1.0/28", bridge, t.TempDir(), testGuest)
	require.NoError(t, err)
	t.Cleanup(func() { _ = exec.Command("ip", "link", "delete", bridge).Run() })

	assert.NoError(t, exec.Command("ip", "link", "show", bridge).Run(), "expected bridge to exist")
	assert.Equal(t, "10.250.1.1", n.gatewayIP.String())

	// Calling Open again (same bridge, same cidr) must be idempotent.
	_, err = New(ctx, "10.250.1.0/28", bridge, t.TempDir(), testGuest)
	require.NoError(t, err, "re-opening an already-set-up bridge must not error")
}

func TestAttachDetach(t *testing.T) {
	requireSupport(t)
	ctx := context.Background()

	bridge := "testbr1"
	n, err := New(ctx, "10.250.2.0/28", bridge, t.TempDir(), testGuest)
	require.NoError(t, err)
	t.Cleanup(func() { _ = exec.Command("ip", "link", "delete", bridge).Run() })

	const sandboxID = "net-test-sbx"
	t.Cleanup(func() { _ = n.Detach(ctx, sandboxID) })

	a, err := n.Attach(ctx, sandboxID)
	require.NoError(t, err)
	assert.Equal(t, "10.250.2.1", a.GatewayIP)
	assert.Contains(t, a.NetnsPath, sandboxID)

	// The tap device must actually exist inside the sandbox's own
	// namespace, with the fixed gateway address the guest expects.
	tapOut, err := exec.CommandContext(ctx, "ip", "netns", "exec", sandboxID, "ip", "addr", "show", "tap0").CombinedOutput()
	require.NoError(t, err, "expected tap0 to exist inside the sandbox's namespace")
	assert.Contains(t, string(tapOut), testGuest.TapIP+"/")

	// The guest's fixed address must actually be reachable at the IP
	// layer within the namespace (tap0 answers for its own address
	// even with nothing attached to the tap fd yet).
	assert.NoError(t,
		exec.CommandContext(ctx, "ip", "netns", "exec", sandboxID, "ping", "-c1", "-W1", testGuest.TapIP).Run(),
		"expected the tap device's own address to answer ping")

	// The namespace must forward and NAT the guest's fixed address,
	// not just the root namespace's own cidr-wide rule (ensureNAT).
	assert.NoError(t,
		exec.CommandContext(ctx, "ip", "netns", "exec", sandboxID, "sh", "-c", "test \"$(cat /proc/sys/net/ipv4/ip_forward)\" = 1").Run(),
		"expected ip_forward enabled inside the sandbox's own namespace")
	natOut, err := exec.CommandContext(ctx, "ip", "netns", "exec", sandboxID, "iptables", "-t", "nat", "-S", "POSTROUTING").CombinedOutput()
	require.NoError(t, err)
	assert.Contains(t, string(natOut), testGuest.GuestIP)

	// The sandbox's real address must be DNATed to the guest, on any
	// port/protocol, so it's reachable from the host without needing
	// ip netns exec.
	dnatOut, err := exec.CommandContext(ctx, "ip", "netns", "exec", sandboxID, "iptables", "-t", "nat", "-S", "PREROUTING").CombinedOutput()
	require.NoError(t, err)
	assert.Contains(t, string(dnatOut), "-d "+a.IPAddress[:strings.Index(a.IPAddress, "/")]+"/32")
	assert.Contains(t, string(dnatOut), "--to-destination "+testGuest.GuestIP)
	fwdOut, err := exec.CommandContext(ctx, "ip", "netns", "exec", sandboxID, "iptables", "-S", "FORWARD").CombinedOutput()
	require.NoError(t, err)
	assert.Contains(t, string(fwdOut), "-d "+testGuest.GuestIP+"/32 -j ACCEPT")

	// The host-side veth end must be enslaved to the bridge.
	out, err := exec.CommandContext(ctx, "ip", "-d", "link", "show", vethHostName(sandboxID)).CombinedOutput()
	require.NoError(t, err)
	assert.Contains(t, string(out), "master "+bridge)

	require.NoError(t, n.Detach(ctx, sandboxID))

	assert.Error(t, exec.Command("ip", "link", "show", vethHostName(sandboxID)).Run(),
		"expected the host-side veth to be gone after Detach")
	assert.Error(t, exec.Command("ip", "netns", "exec", sandboxID, "true").Run(),
		"expected the namespace to be gone after Detach")
}

func TestAttachTwiceReturnsErrSandboxExistsWithoutLeakingAnIP(t *testing.T) {
	requireSupport(t)
	ctx := context.Background()

	bridge := "testbr4"
	n, err := New(ctx, "10.250.5.0/28", bridge, t.TempDir(), testGuest)
	require.NoError(t, err)
	t.Cleanup(func() { _ = exec.Command("ip", "link", "delete", bridge).Run() })

	const sandboxID = "net-test-dup"
	t.Cleanup(func() { _ = n.Detach(ctx, sandboxID) })

	_, err = n.Attach(ctx, sandboxID)
	require.NoError(t, err)

	_, err = n.Attach(ctx, sandboxID)
	require.ErrorIs(t, err, ErrSandboxExists)

	// The failed second Attach must not have claimed a second address
	// for the same sandboxID on top of the one the first call holds.
	entries, err := os.ReadDir(n.ips.dir)
	require.NoError(t, err)
	assert.Len(t, entries, 1, "expected exactly one allocated address for %s, not a leaked second one", sandboxID)
}

func TestSetTapOwner(t *testing.T) {
	requireSupport(t)
	ctx := context.Background()

	bridge := "testbr3"
	n, err := New(ctx, "10.250.4.0/28", bridge, t.TempDir(), testGuest)
	require.NoError(t, err)
	t.Cleanup(func() { _ = exec.Command("ip", "link", "delete", bridge).Run() })

	const sandboxID = "net-test-owner"
	t.Cleanup(func() { _ = n.Detach(ctx, sandboxID) })

	_, err = n.Attach(ctx, sandboxID)
	require.NoError(t, err)

	require.NoError(t, SetTapOwner(sandboxID, 1234, 5678))

	owner, err := exec.CommandContext(ctx, "ip", "netns", "exec", sandboxID, "cat", "/sys/class/net/tap0/owner").CombinedOutput()
	require.NoError(t, err)
	assert.Equal(t, "1234", strings.TrimSpace(string(owner)))

	group, err := exec.CommandContext(ctx, "ip", "netns", "exec", sandboxID, "cat", "/sys/class/net/tap0/group").CombinedOutput()
	require.NoError(t, err)
	assert.Equal(t, "5678", strings.TrimSpace(string(group)))
}

func TestAttachAllocatesDistinctIPsAcrossSandboxes(t *testing.T) {
	requireSupport(t)
	ctx := context.Background()

	bridge := "testbr2"
	n, err := New(ctx, "10.250.3.0/28", bridge, t.TempDir(), testGuest)
	require.NoError(t, err)
	t.Cleanup(func() { _ = exec.Command("ip", "link", "delete", bridge).Run() })

	const sbx1, sbx2 = "net-test-a", "net-test-b"
	t.Cleanup(func() { _ = n.Detach(ctx, sbx1) })
	t.Cleanup(func() { _ = n.Detach(ctx, sbx2) })

	a1, err := n.Attach(ctx, sbx1)
	require.NoError(t, err)
	a2, err := n.Attach(ctx, sbx2)
	require.NoError(t, err)

	assert.NotEqual(t, a1.IPAddress, a2.IPAddress)
}
