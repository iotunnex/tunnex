//go:build linux

// Package egress manages the gateway's NAT + forwarding for full-tunnel egress (S3.7):
// it enables IP forwarding and installs nftables tables that source-NAT tunnel traffic
// out the host's egress interface(s) and forward spoke↔spoke + spoke↔egress. It also
// PROBES whether egress NAT is achievable (a locked-down / route-less host can't) and
// reports that as the node's egress_nat capability — the control plane refuses full-tunnel
// devices against a gateway that lacks it (gateway_no_egress).
//
// IMPLEMENTATION NOTE (deviation from the paper's "Go netlink" preference): we shell to
// `nft` with a declarative ruleset rather than build expression trees via google/nftables.
// The paper explicitly allowed "the nft binary as a fallback"; a declarative ruleset is far
// easier to get correct + review for a root data-plane primitive, at the cost of adding
// nftables to the node image (deploy/docker/node.Dockerfile). The S3.7 decisions doc is
// updated to reflect this.
package egress

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// ifaceRE bounds an interface name to what the kernel allows (Linux IFNAMSIZ-1 = 15,
// alphanumeric + . _ -). wgIface comes from an operator env var and is interpolated into
// the root nft ruleset, so it MUST be validated or a crafted name could inject nft
// statements (review #4).
var ifaceRE = regexp.MustCompile(`^[A-Za-z0-9._-]{1,15}$`)

// Manager reconciles the tunnex nft tables for one WG interface.
type Manager struct{ wgIface string }

// New builds a Manager for the given WireGuard interface (e.g. wg0).
func New(wgIface string) *Manager { return &Manager{wgIface: wgIface} }

// Reconcile is idempotent (safe to call every interval) and DOUBLES as the egress_nat
// capability probe. Ordering matters: it enables ip_forward FIRST and unconditionally, so
// spoke↔spoke forwarding works even on a host that can't egress (review #2), then applies
// the tunnex tables. egress_nat is true ONLY when a default route exists (an egress path)
// AND the IPv4 NAT table applied — so a route-less or NAT-incapable host reports false and
// full-tunnel is refused there rather than silently blackholing.
func (m *Manager) Reconcile(ctx context.Context) (bool, error) {
	if !ifaceRE.MatchString(m.wgIface) {
		return false, fmt.Errorf("invalid wg interface name %q", m.wgIface)
	}
	// Ensure ip_forward FIRST + unconditionally: a later egress failure must not leave
	// forwarding off. In a Docker container /proc/sys is READ-ONLY, so the agent can't
	// write it — the compose `sysctls: net.ipv4.ip_forward=1` sets it at boot and we just
	// VERIFY here; on a bare-metal agent we write it directly.
	if err := ensureIPForward(); err != nil {
		return false, err
	}
	// The masquerade is scoped by SOURCE (the WG pool CIDR), read from the wg interface
	// address — `iifname` is NOT reliable in the nat postrouting hook, whereas `ip saddr`
	// is (and it restores the pool-source scoping the POC had). Until wg0 exists (the WG
	// backend brings it up), there is no pool to scope, so egress isn't ready yet.
	subnet := wgSubnet(ctx, m.wgIface)
	// Apply the tables (add;flush;redefine = atomic replace, so this also self-heals a
	// table a prior crashed agent left, and heals a manual flush). Forward rules use
	// `iifname` (reliable in the forward hook). The masquerade is present only once the
	// pool subnet is known. IPv6 gets a forward-DROP (no NAT66 → v6 egress dropped, not
	// leaked; the client kill-switch also blocks it).
	if err := nftApply(ctx, m.ruleset(subnet)); err != nil {
		return false, err // no nftables / IPv4 NAT support on this host → not egress-capable
	}
	// egress_nat is true only when the pool is known (wg0 up) AND an egress path exists
	// (default route) — otherwise full-tunnel would blackhole, so report NOT capable.
	if subnet == "" || !hasDefaultRoute(ctx) {
		return false, nil
	}
	return true, nil
}

// Teardown removes the tunnex tables (agent shutdown / revocation). Best-effort. NOTE: on
// a crash/SIGKILL the defer doesn't run, but (a) the next agent start's add;flush replaces
// the tables, and (b) in the compose/container deployment the tables live in the container
// netns, which is destroyed when the container stops — so a stopped gateway does not leave
// dangling NAT (review #3).
func (m *Manager) Teardown(ctx context.Context) {
	_ = nftApply(ctx, "delete table ip tunnex\ndelete table ip6 tunnex\n")
}

// ruleset is the atomic desired state. IPv4 (table ip): masquerade tunnel→egress + a
// forward chain with policy DROP so the ct-state return-path guard is real (review #0) —
// only spoke-initiated (iifname wg0) new flows + established return traffic are accepted,
// so the egress LAN can NEVER initiate into spokes. The masquerade is scoped by SOURCE
// (`ip saddr <pool>` — reliable in the postrouting hook, unlike `iifname`) out ANY
// non-tunnel iface (`oifname != wg0` — multi-homed/ECMP-safe, review #8), so it never
// masquerades spoke↔spoke (which stays wg0→wg0) or off-pool sources (review #5). IPv6
// (table ip6): forward policy DROP with only spoke↔spoke allowed — v6 full-tunnel egress
// is dropped (no NAT66 yet), never leaked (review #1/#7).
func (m *Manager) ruleset(subnet string) string {
	wg := m.wgIface
	// Masquerade line present only when the pool subnet is known (wg0 up). Scoped by
	// SOURCE (ip saddr) — reliable in postrouting, unlike iifname — out ANY non-tunnel
	// iface (ECMP/multi-homed-safe). nft masks e.g. 10.99.0.1/24 to the /24 network.
	masq := ""
	if subnet != "" {
		masq = fmt.Sprintf("    ip saddr %s oifname != \"%s\" masquerade\n", subnet, wg)
	}
	return fmt.Sprintf(`add table ip tunnex
flush table ip tunnex
table ip tunnex {
  chain postrouting {
    type nat hook postrouting priority srcnat; policy accept;
%[2]s  }
  chain forward {
    type filter hook forward priority filter; policy drop;
    ct state established,related accept
    iifname "%[1]s" oifname "%[1]s" accept
    iifname "%[1]s" oifname != "%[1]s" accept
  }
}
add table ip6 tunnex
flush table ip6 tunnex
table ip6 tunnex {
  chain forward {
    type filter hook forward priority filter; policy drop;
    ct state established,related accept
    iifname "%[1]s" oifname "%[1]s" accept
  }
}
`, wg, masq)
}

// nftApply pipes a ruleset to `nft -f -` (atomic transaction).
func nftApply(ctx context.Context, ruleset string) error {
	cmd := exec.CommandContext(ctx, "nft", "-f", "-")
	cmd.Stdin = strings.NewReader(ruleset)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("nft apply: %w: %s", err, bytes.TrimSpace(out))
	}
	return nil
}

// wgSubnet returns the WG interface's IPv4 address+prefix (e.g. "10.99.0.1/24"), used to
// scope the masquerade by SOURCE (nft masks it to the network). Empty if the interface
// isn't up yet (the WG backend brings it up shortly after enrollment).
func wgSubnet(ctx context.Context, iface string) string {
	out, err := exec.CommandContext(ctx, "ip", "-o", "-4", "addr", "show", "dev", iface).Output()
	if err != nil {
		return ""
	}
	fields := strings.Fields(string(out)) // "N: wg0    inet 10.99.0.1/24 scope global wg0 ..."
	for i, f := range fields {
		if f == "inet" && i+1 < len(fields) {
			return fields[i+1]
		}
	}
	return ""
}

// hasDefaultRoute reports whether the host has an IPv4 default route (an egress path).
func hasDefaultRoute(ctx context.Context) bool {
	out, err := exec.CommandContext(ctx, "ip", "route", "show", "default").Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "default")
}

// ensureIPForward enables IPv4 forwarding. It tries to WRITE the sysctl (bare-metal
// agent); if /proc/sys is read-only (Docker default — the container can't write it), it
// falls back to VERIFYING it's already 1 (set by the compose sysctl at boot). Only a
// not-writable-AND-not-already-enabled state is a real failure.
func ensureIPForward() error {
	if err := writeSysctl("net/ipv4/ip_forward", "1"); err == nil {
		return nil
	}
	v, rerr := os.ReadFile("/proc/sys/net/ipv4/ip_forward")
	if rerr == nil && strings.TrimSpace(string(v)) == "1" {
		return nil // already enabled (compose/host set it) — read-only fs is expected in a container
	}
	return fmt.Errorf("ip_forward not enabled and not writable (set sysctls net.ipv4.ip_forward=1 on the node-agent)")
}

func writeSysctl(key, val string) error {
	return os.WriteFile("/proc/sys/"+key, []byte(val), 0o644)
}
