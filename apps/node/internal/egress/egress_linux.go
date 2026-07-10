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
	// ip_forward is the agent's to own now (removed from the compose sysctl). Set it
	// FIRST + unconditionally: a later egress failure must not leave forwarding off.
	if err := writeSysctl("net/ipv4/ip_forward", "1"); err != nil {
		return false, fmt.Errorf("ip_forward: %w", err)
	}
	// Apply the tables (add;flush;redefine = atomic replace, so this also self-heals a
	// table a prior crashed agent left, and heals a manual flush). This installs the
	// IPv4 NAT + forward and the IPv6 forward-DROP (v6 full-tunnel is blocked at the
	// gateway — no NAT66 yet, so no v6 leak; the client kill-switch also blocks it).
	if err := nftApply(ctx, m.ruleset()); err != nil {
		return false, err // no nftables / IPv4 NAT support on this host → not egress-capable
	}
	// Capability = an egress path exists. Without a default route, full-tunnel would
	// blackhole even though nft applied, so report NOT capable.
	if !hasDefaultRoute(ctx) {
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
// so the egress LAN can NEVER initiate into spokes. Egress is scoped `oifname != wg0`
// (masquerade/forward tunnel traffic out ANY non-tunnel iface — handles multi-homed / ECMP
// gateways, review #8; never masquerades spoke↔spoke, which stays wg0→wg0). Source scoping
// to the pool is enforced UPSTREAM by WireGuard cryptokey routing (each spoke's AllowedIPs
// pin its pool /32, so only pool-sourced packets ever reach wg0 — review #5). IPv6 (table
// ip6): forward policy DROP with only spoke↔spoke allowed — v6 full-tunnel egress is
// dropped (no NAT66 yet), never leaked (review #1/#7).
func (m *Manager) ruleset() string {
	wg := m.wgIface
	return fmt.Sprintf(`add table ip tunnex
flush table ip tunnex
table ip tunnex {
  chain postrouting {
    type nat hook postrouting priority srcnat; policy accept;
    iifname "%[1]s" oifname != "%[1]s" masquerade
  }
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
`, wg)
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

// hasDefaultRoute reports whether the host has an IPv4 default route (an egress path).
func hasDefaultRoute(ctx context.Context) bool {
	out, err := exec.CommandContext(ctx, "ip", "route", "show", "default").Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "default")
}

func writeSysctl(key, val string) error {
	return os.WriteFile("/proc/sys/"+key, []byte(val), 0o644)
}
