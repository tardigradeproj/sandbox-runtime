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
	"os/exec"
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

func TestOpenCreatesBridgeAndNAT(t *testing.T) {
	requireSupport(t)
	ctx := context.Background()

	bridge := "testbr0"
	n, err := New(ctx, "10.250.1.0/28", bridge, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = exec.Command("ip", "link", "delete", bridge).Run() })

	assert.NoError(t, exec.Command("ip", "link", "show", bridge).Run(), "expected bridge to exist")
	assert.Equal(t, "10.250.1.1", n.gatewayIP.String())

	// Calling Open again (same bridge, same cidr) must be idempotent.
	_, err = New(ctx, "10.250.1.0/28", bridge, t.TempDir())
	require.NoError(t, err, "re-opening an already-set-up bridge must not error")
}

func TestAttachDetach(t *testing.T) {
	requireSupport(t)
	ctx := context.Background()

	bridge := "testbr1"
	n, err := New(ctx, "10.250.2.0/28", bridge, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = exec.Command("ip", "link", "delete", bridge).Run() })

	const sandboxID = "net-test-sbx"
	t.Cleanup(func() { _ = n.Detach(ctx, sandboxID) })

	a, err := n.Attach(ctx, sandboxID)
	require.NoError(t, err)
	assert.Equal(t, "10.250.2.1", a.GatewayIP)
	assert.Contains(t, a.NetnsPath, sandboxID)

	// The tap device must actually exist inside the sandbox's own
	// namespace.
	assert.NoError(t,
		exec.CommandContext(ctx, "ip", "netns", "exec", sandboxID, "ip", "link", "show", "tap0").Run(),
		"expected tap0 to exist inside the sandbox's namespace")

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

func TestAttachAllocatesDistinctIPsAcrossSandboxes(t *testing.T) {
	requireSupport(t)
	ctx := context.Background()

	bridge := "testbr2"
	n, err := New(ctx, "10.250.3.0/28", bridge, t.TempDir())
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
