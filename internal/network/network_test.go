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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/tardigradeproj/sandbox-runtime/internal/cfg"
	v1 "github.com/tardigradeproj/sandbox-runtime/pkg/api/runtime/v1"
)

func requireSupport(t *testing.T) {
	t.Helper()
	// Interface names are capped at 15 characters (IFNAMSIZ).
	if err := exec.Command("ip", "link", "add", "svcnetprobe", "type", "dummy").Run(); err != nil {
		t.Skipf("network setup unavailable: %v", err)
	}
	_ = exec.Command("ip", "link", "delete", "svcnetprobe").Run()
}

func newTestNetwork(t *testing.T) *Network {
	t.Helper()
	requireSupport(t)

	bridge := "svctestbr0"
	n, err := New(&cfg.Settings{
		State:   t.TempDir(),
		Network: cfg.Network{Cidr: "10.250.9.0/28", BridgeName: bridge},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = exec.Command("ip", "link", "delete", bridge).Run() })
	return n
}

func TestAttachNetworkRequiresSandboxID(t *testing.T) {
	n := newTestNetwork(t)
	_, err := n.AttachNetwork(context.Background(), &v1.AttachNetworkRequest{})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestDetachNetworkRequiresSandboxID(t *testing.T) {
	n := newTestNetwork(t)
	_, err := n.DetachNetwork(context.Background(), &v1.DetachNetworkRequest{})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestAttachDetachNetworkRoundTrip(t *testing.T) {
	n := newTestNetwork(t)
	const sandboxID = "net-svc-test-sbx"
	t.Cleanup(func() {
		_, _ = n.DetachNetwork(context.Background(), &v1.DetachNetworkRequest{SandboxId: sandboxID})
	})

	resp, err := n.AttachNetwork(context.Background(), &v1.AttachNetworkRequest{SandboxId: sandboxID})
	require.NoError(t, err)
	assert.Equal(t, "tap0", resp.GetTapDevice())
	assert.NotEmpty(t, resp.GetNetnsPath())
	assert.NotEmpty(t, resp.GetIpAddress())
	assert.NotEmpty(t, resp.GetGatewayIp())

	_, err = n.DetachNetwork(context.Background(), &v1.DetachNetworkRequest{SandboxId: sandboxID})
	require.NoError(t, err)

	assert.Error(t, exec.Command("ip", "netns", "exec", sandboxID, "true").Run(),
		"expected the namespace to be gone after DetachNetwork")
}
