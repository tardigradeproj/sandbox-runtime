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

// Package network provisions per-sandbox networking (namespace, tap
// device, veth-to-bridge egress, IP) for one node. It has no
// knowledge of Firecracker, jailer, or rootfs -- it only ever deals
// in sandbox IDs and plain strings. See net-design.md.
package network

import (
	"bytes"
	"context"
	"fmt"
	"hash/fnv"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tardigradeproj/sandbox-runtime/pkg/sandboxnet"
)

const (
	// TapDeviceName is the tap device created inside every sandbox's
	// own netns for the guest NIC (see Attach), e.g. for Firecracker's
	// NetworkInterface.host_dev_name. Hardcoded, not per-sandbox:
	// every sandbox has its own isolated namespace, so this name never
	// collides across sandboxes.
	TapDeviceName = "tap0"
	// IfaceID identifies the sandbox's guest NIC in Firecracker's own
	// API (PUT /network-interfaces/{iface_id}). Hardcoded for the same
	// reason as TapDeviceName.
	IfaceID = "net0"
)

// NetnsPath returns the network namespace path Attach creates for
// sandboxID (see sandboxnet.Create). By convention, not by lookup:
// callers that only need to point jailer/Firecracker at a sandbox's
// namespace (e.g. CreateSandbox) can compute this directly instead of
// calling Attach themselves -- provisioning that namespace is
// NetworkService's job, done independently.
func NetnsPath(sandboxID string) string {
	return filepath.Join(sandboxnet.Dir, sandboxID)
}

// Attachment is what AttachNetwork hands back: everything the caller
// needs to actually run a sandbox, in generic terms.
type Attachment struct {
	// NetnsPath is the network namespace to run the sandbox in, e.g.
	// for jailer's --netns.
	NetnsPath string
	// IPAddress is the sandbox's address, in CIDR form.
	IPAddress string
	// GatewayIP is the address the sandbox routes through.
	GatewayIP string
}

// Network is one node's view of sandbox networking: one bridge, one
// cidr, one NAT rule, shared by every sandbox this node attaches.
type Network struct {
	cidr       *net.IPNet
	gatewayIP  net.IP
	prefixLen  int
	bridgeName string
	ips        *ipAllocator
}

// Open sets up this node's bridge and NAT rule (both idempotent --
// safe to call on every daemon start) and returns a Network ready to
// Attach/Detach sandboxes. cidr must already be validated (see
// cfg.validateNetwork); stateDir is this node's own state directory
// for allocator bookkeeping, e.g. "<cfg.State>/network".
func Open(ctx context.Context, cidr, bridgeName, stateDir string) (*Network, error) {
	_, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, fmt.Errorf("network: cidr %q: %w", cidr, err)
	}
	ones, _ := ipNet.Mask.Size()
	gateway := uint32ToIP(ipToUint32(ipNet.IP) + 1)

	ips, err := newIPAllocator(ipNet, gateway, stateDir)
	if err != nil {
		return nil, err
	}

	n := &Network{
		cidr:       ipNet,
		gatewayIP:  gateway,
		prefixLen:  ones,
		bridgeName: bridgeName,
		ips:        ips,
	}

	if err := n.ensureBridge(ctx); err != nil {
		return nil, fmt.Errorf("network: ensure bridge: %w", err)
	}
	if err := n.ensureNAT(ctx); err != nil {
		return nil, fmt.Errorf("network: ensure nat: %w", err)
	}
	return n, nil
}

func (n *Network) ensureBridge(ctx context.Context) error {
	if err := run(ctx, "ip", "link", "show", n.bridgeName); err != nil {
		if err := run(ctx, "ip", "link", "add", n.bridgeName, "type", "bridge"); err != nil {
			return err
		}
	}

	gatewayCIDR := fmt.Sprintf("%s/%d", n.gatewayIP, n.prefixLen)
	out, err := output(ctx, "ip", "addr", "show", "dev", n.bridgeName)
	if err != nil {
		return err
	}
	if !strings.Contains(out, n.gatewayIP.String()+"/") {
		if err := run(ctx, "ip", "addr", "add", gatewayCIDR, "dev", n.bridgeName); err != nil {
			return err
		}
	}

	return run(ctx, "ip", "link", "set", n.bridgeName, "up")
}

func (n *Network) ensureNAT(ctx context.Context) error {
	if err := runFile(ctx, "/proc/sys/net/ipv4/ip_forward", "1"); err != nil {
		return err
	}

	uplink, err := defaultRouteInterface(ctx)
	if err != nil {
		return fmt.Errorf("resolve uplink interface: %w", err)
	}

	checkArgs := []string{"-t", "nat", "-C", "POSTROUTING", "-s", n.cidr.String(), "-o", uplink, "-j", "MASQUERADE"}
	if run(ctx, "iptables", checkArgs...) == nil {
		return nil // rule already present
	}
	addArgs := append([]string{"-t", "nat", "-A"}, checkArgs[3:]...)
	return run(ctx, "iptables", addArgs...)
}

// Attach provisions networking for sandboxID: a namespace, a tap
// device inside it for the guest NIC, and a veth pair out to this
// node's bridge for egress. Idempotent only in the sense that
// retrying after a partial failure is safe to call again once any
// leftover state is cleaned up by Detach; it does not itself detect
// an already-attached sandbox.
func (n *Network) Attach(ctx context.Context, sandboxID string) (Attachment, error) {
	ip, err := n.ips.Allocate(sandboxID)
	if err != nil {
		return Attachment{}, fmt.Errorf("network: allocate ip: %w", err)
	}

	netnsPath, err := sandboxnet.Create(sandboxID)
	if err != nil {
		return Attachment{}, fmt.Errorf("network: create namespace: %w", err)
	}

	vethHost := vethHostName(sandboxID)
	const vethNS = "veth-ns"

	steps := [][]string{
		{"ip", "netns", "exec", sandboxID, "ip", "tuntap", "add", "dev", TapDeviceName, "mode", "tap"},
		{"ip", "netns", "exec", sandboxID, "ip", "link", "set", TapDeviceName, "up"},
		{"ip", "link", "add", vethHost, "type", "veth", "peer", "name", vethNS},
		{"ip", "link", "set", vethNS, "netns", sandboxID},
		{"ip", "link", "set", vethHost, "master", n.bridgeName},
		{"ip", "link", "set", vethHost, "up"},
		{"ip", "netns", "exec", sandboxID, "ip", "addr", "add", fmt.Sprintf("%s/%d", ip, n.prefixLen), "dev", vethNS},
		{"ip", "netns", "exec", sandboxID, "ip", "link", "set", vethNS, "up"},
		{"ip", "netns", "exec", sandboxID, "ip", "link", "set", "lo", "up"},
		{"ip", "netns", "exec", sandboxID, "ip", "route", "add", "default", "via", n.gatewayIP.String()},
	}
	for _, args := range steps {
		if err := run(ctx, args[0], args[1:]...); err != nil {
			return Attachment{}, fmt.Errorf("network: %s: %w", strings.Join(args, " "), err)
		}
	}

	return Attachment{
		NetnsPath: netnsPath,
		IPAddress: fmt.Sprintf("%s/%d", ip, n.prefixLen),
		GatewayIP: n.gatewayIP.String(),
	}, nil
}

// Detach reverses Attach and releases sandboxID's IP. Safe to call
// even if Attach only partially completed or was never called --
// each step tolerates "already gone."
func (n *Network) Detach(ctx context.Context, sandboxID string) error {
	_ = run(ctx, "ip", "link", "delete", vethHostName(sandboxID)) // cascades into the netns
	_ = sandboxnet.Delete(sandboxID)                              // takes tap0 down with it
	return n.ips.Release(sandboxID)
}

// vethHostName derives a short, unique, valid interface name from
// sandboxID (Linux interface names are capped at 15 characters),
// since -- unlike tap0 and veth-ns, which live inside each sandbox's
// own namespace -- the host-side veth end lives on the shared bridge
// and does need to be unique across sandboxes.
func vethHostName(sandboxID string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(sandboxID))
	return "sbx" + strconv.FormatUint(uint64(h.Sum32()), 16)
}

func defaultRouteInterface(ctx context.Context) (string, error) {
	out, err := output(ctx, "ip", "route", "show", "default")
	if err != nil {
		return "", err
	}
	fields := strings.Fields(out)
	for i, f := range fields {
		if f == "dev" && i+1 < len(fields) {
			return fields[i+1], nil
		}
	}
	return "", fmt.Errorf("no default route found")
}

func runFile(_ context.Context, path, content string) error {
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func run(ctx context.Context, name string, args ...string) error {
	_, err := output(ctx, name, args...)
	return err
}

func output(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}
