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
// device, veth-to-bridge egress, IP) for one node. See docs/network-design.md.
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

	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
)

const (
	// TapDeviceName is every sandbox's guest-NIC tap device. Fixed,
	// not per-sandbox: each sandbox has its own namespace.
	TapDeviceName = "tap0"
	// IfaceID identifies the guest NIC in Firecracker's own API.
	IfaceID = "net0"
)

// GuestNetwork is the fixed point-to-point pair every sandbox's tap
// device and guest use, same for every sandbox, since each one
// already has its own namespace. Configured via cfg.Network.GuestNetwork;
// this type has no default of its own -- cfg's struct tags carry it.
type GuestNetwork struct {
	TapIP     string
	GuestIP   string
	PrefixLen int
}

// GuestIPKernelParam is the Linux kernel "ip=" boot parameter that
// configures g via the kernel's own built-in IP autoconfiguration
// (CONFIG_IP_PNP), before any userspace init ever runs -- the same
// mechanism Firecracker's own network-setup.md documents as an
// alternative to a guest-side script.
func GuestIPKernelParam(g GuestNetwork) string {
	mask := net.IP(net.CIDRMask(g.PrefixLen, 32)).String()
	return fmt.Sprintf("ip=%s::%s:%s::eth0:off", g.GuestIP, g.TapIP, mask)
}

// NetnsPath returns the namespace path Attach creates for sandboxID,
// by convention, so a caller (CreateSandbox) can point jailer at it
// without calling Attach itself.
func NetnsPath(sandboxID string) string {
	return filepath.Join(netnsDir, sandboxID)
}

// Attachment is what Attach hands back.
type Attachment struct {
	// NetnsPath is the namespace to run the sandbox in
	NetnsPath string
	IPAddress string
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
	guest      GuestNetwork
}

// New sets up this node's bridge and NAT rule (idempotent -- safe on
// every daemon start) and returns a Network ready to Attach/Detach
// sandboxes. stateDir is this node's own state dir for IP bookkeeping.
func New(ctx context.Context, cidr, bridgeName, stateDir string, guest GuestNetwork) (*Network, error) {
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
		guest:      guest,
	}

	if err := n.ensureBridge(ctx); err != nil {
		return nil, fmt.Errorf("network: ensure bridge: %w", err)
	}
	if err := n.ensureNAT(ctx); err != nil {
		return nil, fmt.Errorf("network: ensure nat: %w", err)
	}
	return n, nil
}

func (n *Network) ensureBridge(context.Context) error {
	// ip link show <bridge> / ip link add <bridge> type bridge
	link, err := netlink.LinkByName(n.bridgeName)
	if err != nil {
		if _, ok := err.(netlink.LinkNotFoundError); !ok {
			return fmt.Errorf("find bridge: %w", err)
		}
		br := &netlink.Bridge{LinkAttrs: netlink.LinkAttrs{Name: n.bridgeName}}
		if err := netlink.LinkAdd(br); err != nil {
			return fmt.Errorf("add bridge: %w", err)
		}
		if link, err = netlink.LinkByName(n.bridgeName); err != nil {
			return fmt.Errorf("find bridge after add: %w", err)
		}
	}

	// ip addr add <gatewayIP>/<prefixLen> dev <bridge> (if not already present)
	addrs, err := netlink.AddrList(link, netlink.FAMILY_V4)
	if err != nil {
		return fmt.Errorf("list bridge addresses: %w", err)
	}
	hasGatewayAddr := false
	for _, a := range addrs {
		if a.IP.Equal(n.gatewayIP) {
			hasGatewayAddr = true
			break
		}
	}
	if !hasGatewayAddr {
		addr := &netlink.Addr{IPNet: &net.IPNet{IP: n.gatewayIP, Mask: net.CIDRMask(n.prefixLen, 32)}}
		if err := netlink.AddrAdd(link, addr); err != nil {
			return fmt.Errorf("assign bridge address: %w", err)
		}
	}

	// ip link set <bridge> up
	return netlink.LinkSetUp(link)
}

func (n *Network) ensureNAT(ctx context.Context) error {
	if err := runFile(ctx, "/proc/sys/net/ipv4/ip_forward", "1"); err != nil {
		return err
	}

	uplink, err := defaultRouteInterface()
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
// device inside it for the guest NIC (fixed address, see n.guest),
// and a veth pair out to this node's bridge for egress, with the
// guest's traffic NATed from n.guest.GuestIP to the sandbox's real
// allocated address as it crosses from tap0 to veth-ns. The reverse
// direction is NATed too: any traffic arriving at the sandbox's real
// address, on any port or protocol, is redirected to the guest --
// see the host-reachability DNAT rule below.
func (n *Network) Attach(ctx context.Context, sandboxID string) (Attachment, error) {
	// Created first, before allocating an IP: if sandboxID is already
	// attached, this fails fast (ErrSandboxExists) without leaking a
	// second address on an id that already holds one.
	// ip netns add <sandboxID>
	netnsPath, err := createNetns(sandboxID)
	if err != nil {
		return Attachment{}, fmt.Errorf("network: create namespace: %w", err)
	}

	ip, err := n.ips.Allocate(sandboxID)
	if err != nil {
		return Attachment{}, fmt.Errorf("network: allocate ip: %w", err)
	}

	// ip netns exec <sandboxID> ip tuntap add dev tap0 mode tap
	// ip netns exec <sandboxID> ip addr add 169.254.100.1/30 dev tap0
	// ip netns exec <sandboxID> ip link set tap0 up
	//
	// Tap creation is a raw ioctl, not a netlink message, so it needs
	// withNetns rather than a Handle (see withNetns).
	err = withNetns(sandboxID, func() error {
		tap := &netlink.Tuntap{
			LinkAttrs: netlink.LinkAttrs{Name: TapDeviceName},
			Mode:      netlink.TUNTAP_MODE_TAP,
		}
		if err := netlink.LinkAdd(tap); err != nil {
			return fmt.Errorf("add tap device: %w", err)
		}
		tapAddr := &netlink.Addr{IPNet: &net.IPNet{IP: net.ParseIP(n.guest.TapIP), Mask: net.CIDRMask(n.guest.PrefixLen, 32)}}
		if err := netlink.AddrAdd(tap, tapAddr); err != nil {
			return fmt.Errorf("assign tap address: %w", err)
		}
		return netlink.LinkSetUp(tap)
	})
	if err != nil {
		return Attachment{}, fmt.Errorf("network: %w", err)
	}

	nsHandle, err := netns.GetFromName(sandboxID)
	if err != nil {
		return Attachment{}, fmt.Errorf("network: open namespace: %w", err)
	}
	defer nsHandle.Close()

	// Real netlink messages from here on, so a Handle bound to the
	// sandbox's namespace reaches it without moving this thread.
	nsLink, err := netlink.NewHandleAt(nsHandle)
	if err != nil {
		return Attachment{}, fmt.Errorf("network: open netlink handle: %w", err)
	}
	defer nsLink.Delete()

	vethHost := vethHostName(sandboxID)
	const vethNS = "veth-ns"

	// ip link add <vethHost> type veth peer name veth-ns
	veth := &netlink.Veth{LinkAttrs: netlink.LinkAttrs{Name: vethHost}, PeerName: vethNS}
	if err := netlink.LinkAdd(veth); err != nil {
		return Attachment{}, fmt.Errorf("network: add veth pair: %w", err)
	}

	// ip link set veth-ns netns <sandboxID>
	peer, err := netlink.LinkByName(vethNS)
	if err != nil {
		return Attachment{}, fmt.Errorf("network: find veth peer: %w", err)
	}
	if err := netlink.LinkSetNsFd(peer, int(nsHandle)); err != nil {
		return Attachment{}, fmt.Errorf("network: move veth peer into namespace: %w", err)
	}

	// ip link set <vethHost> master <bridge>
	bridge, err := netlink.LinkByName(n.bridgeName)
	if err != nil {
		return Attachment{}, fmt.Errorf("network: find bridge: %w", err)
	}
	if err := netlink.LinkSetMaster(veth, bridge); err != nil {
		return Attachment{}, fmt.Errorf("network: enslave veth to bridge: %w", err)
	}

	// ip link set <vethHost> up
	if err := netlink.LinkSetUp(veth); err != nil {
		return Attachment{}, fmt.Errorf("network: set veth host end up: %w", err)
	}

	// ip netns exec <sandboxID> ip addr add <ip>/<prefixLen> dev veth-ns
	nsVeth, err := nsLink.LinkByName(vethNS)
	if err != nil {
		return Attachment{}, fmt.Errorf("network: find veth peer in namespace: %w", err)
	}
	addr := &netlink.Addr{IPNet: &net.IPNet{IP: ip, Mask: net.CIDRMask(n.prefixLen, 32)}}
	if err := nsLink.AddrAdd(nsVeth, addr); err != nil {
		return Attachment{}, fmt.Errorf("network: assign address: %w", err)
	}

	// ip netns exec <sandboxID> ip link set veth-ns up
	if err := nsLink.LinkSetUp(nsVeth); err != nil {
		return Attachment{}, fmt.Errorf("network: set veth peer up: %w", err)
	}

	// ip netns exec <sandboxID> ip link set lo up
	lo, err := nsLink.LinkByName("lo")
	if err != nil {
		return Attachment{}, fmt.Errorf("network: find loopback: %w", err)
	}
	if err := nsLink.LinkSetUp(lo); err != nil {
		return Attachment{}, fmt.Errorf("network: set loopback up: %w", err)
	}

	// ip netns exec <sandboxID> ip route add default via <gatewayIP>
	if err := nsLink.RouteAdd(&netlink.Route{Gw: n.gatewayIP}); err != nil {
		return Attachment{}, fmt.Errorf("network: add default route: %w", err)
	}

	// Second NAT boundary, nested inside the one ensureNAT already set
	// up: the guest's fixed GuestIP is masqueraded to veth-ns's real,
	// per-sandbox address as it leaves this namespace, which the outer
	// bridge+NAT then carries the rest of the way out. Scoped to this
	// namespace, so it's torn down for free when Detach deletes it.
	err = withNetns(sandboxID, func() error {
		// ip netns exec <sandboxID> sh -c 'echo 1 > /proc/sys/net/ipv4/ip_forward'
		if err := os.WriteFile("/proc/sys/net/ipv4/ip_forward", []byte("1"), 0o644); err != nil {
			return fmt.Errorf("enable ip_forward: %w", err)
		}
		// ip netns exec <sandboxID> iptables -t nat -A POSTROUTING -s 169.254.100.2 -o veth-ns -j MASQUERADE
		if err := run(ctx, "iptables", "-t", "nat", "-A", "POSTROUTING", "-s", n.guest.GuestIP, "-o", vethNS, "-j", "MASQUERADE"); err != nil {
			return fmt.Errorf("add guest masquerade rule: %w", err)
		}
		// ip netns exec <sandboxID> iptables -A FORWARD -i tap0 -o veth-ns -j ACCEPT
		if err := run(ctx, "iptables", "-A", "FORWARD", "-i", TapDeviceName, "-o", vethNS, "-j", "ACCEPT"); err != nil {
			return fmt.Errorf("add forward accept rule: %w", err)
		}
		// ip netns exec <sandboxID> iptables -A FORWARD -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT
		if err := run(ctx, "iptables", "-A", "FORWARD", "-m", "conntrack", "--ctstate", "RELATED,ESTABLISHED", "-j", "ACCEPT"); err != nil {
			return fmt.Errorf("add forward established rule: %w", err)
		}

		// Host reachability: redirect anything arriving at this
		// sandbox's real address -- any port, any protocol -- to the
		// guest's fixed address instead.
		// ip netns exec <sandboxID> iptables -t nat -A PREROUTING -d <ip> -j DNAT --to-destination 169.254.100.2
		if err := run(ctx, "iptables", "-t", "nat", "-A", "PREROUTING", "-d", ip.String(), "-j", "DNAT", "--to-destination", n.guest.GuestIP); err != nil {
			return fmt.Errorf("add host-reachability dnat rule: %w", err)
		}
		// ip netns exec <sandboxID> iptables -A FORWARD -d 169.254.100.2 -j ACCEPT
		return run(ctx, "iptables", "-A", "FORWARD", "-d", n.guest.GuestIP, "-j", "ACCEPT")
	})
	if err != nil {
		return Attachment{}, fmt.Errorf("network: %w", err)
	}

	return Attachment{
		NetnsPath: netnsPath,
		IPAddress: fmt.Sprintf("%s/%d", ip, n.prefixLen),
		GatewayIP: n.gatewayIP.String(),
	}, nil
}

// SetTapOwner chowns sandboxID's tap device to uid/gid (via
// TUNSETOWNER/TUNSETGROUP), so a process running as that uid can open
// it without CAP_NET_ADMIN. The device is created by Attach; this
// runs separately because the caller (jailer's uid, in "on_demand"
// mode) often isn't known until after Attach already ran.
func SetTapOwner(sandboxID string, uid, gid int) error {
	return withNetns(sandboxID, func() error {
		h, err := netlink.NewHandle()
		if err != nil {
			return fmt.Errorf("open netlink handle: %w", err)
		}
		defer h.Delete()

		tap := &netlink.Tuntap{
			LinkAttrs: netlink.LinkAttrs{Name: TapDeviceName},
			Mode:      netlink.TUNTAP_MODE_TAP,
			// Attach's default Flags (TUNTAP_DEFAULTS) includes
			// IFF_TUN_EXCL, which fails with EBUSY if the device
			// already exists -- which it does, that's the point.
			Flags: netlink.TUNTAP_ONE_QUEUE,
			Owner: uint32(uid),
			Group: uint32(gid),
		}
		return h.LinkModify(tap)
	})
}

// Detach reverses Attach and releases sandboxID's IP. Safe to call
// even if Attach only partially completed or was never called --
// each step tolerates "already gone."
func (n *Network) Detach(_ context.Context, sandboxID string) error {
	// ip link delete <vethHost> (cascades: removes the veth-ns peer too)
	if link, err := netlink.LinkByName(vethHostName(sandboxID)); err == nil {
		_ = netlink.LinkDel(link)
	}
	// ip netns delete <sandboxID> (takes tap0 down with it)
	_ = deleteNetns(sandboxID)
	return n.ips.Release(sandboxID)
}

// vethHostName derives a short interface name from sandboxID (Linux
// names are capped at 15 characters) -- unique across sandboxes,
// since this end lives on the shared bridge.
func vethHostName(sandboxID string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(sandboxID))
	return "sbx" + strconv.FormatUint(uint64(h.Sum32()), 16)
}

// ip route show default
func defaultRouteInterface() (string, error) {
	routes, err := netlink.RouteList(nil, netlink.FAMILY_V4)
	if err != nil {
		return "", fmt.Errorf("list routes: %w", err)
	}
	for _, r := range routes {
		if r.Dst == nil {
			link, err := netlink.LinkByIndex(r.LinkIndex)
			if err != nil {
				return "", fmt.Errorf("resolve route interface: %w", err)
			}
			return link.Attrs().Name, nil
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
