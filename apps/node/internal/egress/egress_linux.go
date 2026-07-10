//go:build linux

// Package egress manages the gateway's NAT + forwarding for full-tunnel egress (S3.7):
// it enables IP forwarding and installs an nftables `tunnex` table that source-NATs
// tunnel traffic out the host's egress interface and forwards spoke↔spoke + spoke↔egress.
// It also PROBES whether egress NAT is achievable on this host (a locked-down kernel
// can't) and reports that as the node's egress_nat capability — the control plane refuses
// full-tunnel devices against a gateway that lacks it (gateway_no_egress).
//
// IMPLEMENTATION NOTE (deviation from the paper's "Go netlink" preference): we shell to
// `nft` with a declarative ruleset rather than build expression trees via google/nftables.
// The paper explicitly allowed "the nft binary as a fallback"; a declarative ruleset is far
// easier to get correct + review for a root data-plane primitive, at the cost of adding
// nftables to the node image (deploy/docker/node.Dockerfile).
package egress

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Manager reconciles the tunnex nft table for one WG interface.
type Manager struct{ wgIface string }

// New builds a Manager for the given WireGuard interface (e.g. wg0).
func New(wgIface string) *Manager { return &Manager{wgIface: wgIface} }

// Reconcile is idempotent (safe to call every interval): it enables ip_forward, detects
// the egress interface (the default route), and atomically (re)applies the tunnex table.
// It DOUBLES as the capability probe — returns whether egress NAT is achievable. A
// failure returns (false, err) and does NOT crash the agent (a locked-down host just
// reports egress_nat=false → full-tunnel refused there).
func (m *Manager) Reconcile(ctx context.Context) (bool, error) {
	egressIf, err := defaultRouteIface(ctx)
	if err != nil || egressIf == "" {
		return false, fmt.Errorf("egress iface: %w", err)
	}
	// ip_forward is the agent's to own now (removed from the compose sysctl). Best-effort
	// for IPv6 too (NAT66 is best-effort; failure there doesn't gate egress_nat).
	if err := writeSysctl("net/ipv4/ip_forward", "1"); err != nil {
		return false, fmt.Errorf("ip_forward: %w", err)
	}
	_ = writeSysctl("net/ipv6/conf/all/forwarding", "1")
	if err := nftApply(ctx, m.ruleset(egressIf)); err != nil {
		return false, err // no nftables/nat support on this host → not egress-capable
	}
	return true, nil
}

// Teardown removes the tunnex table (agent shutdown / revocation full-sweep). Best-effort.
func (m *Manager) Teardown(ctx context.Context) {
	_ = nftApply(ctx, "delete table inet tunnex\n")
}

// ruleset is the atomic desired state: masquerade tunnel→egress, forward spoke↔spoke
// (device-to-device) + spoke↔egress (full-tunnel). Masquerade is scoped by iifname (the
// WG interface) — inherently only tunnel-sourced traffic, never a blanket rule. NAT66 for
// IPv6 is included best-effort; if the host lacks it the whole apply fails and egress_nat
// is false (the client kill-switch then blocks IPv6, no leak).
func (m *Manager) ruleset(egressIf string) string {
	wg := m.wgIface
	return fmt.Sprintf(`add table inet tunnex
flush table inet tunnex
table inet tunnex {
  chain postrouting {
    type nat hook postrouting priority srcnat; policy accept;
    iifname "%[1]s" oifname "%[2]s" masquerade
  }
  chain forward {
    type filter hook forward priority filter; policy accept;
    iifname "%[1]s" oifname "%[1]s" accept
    iifname "%[1]s" oifname "%[2]s" accept
    iifname "%[2]s" oifname "%[1]s" ct state established,related accept
  }
}
`, wg, egressIf)
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

// defaultRouteIface returns the interface of the IPv4 default route (the egress iface).
func defaultRouteIface(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, "ip", "route", "show", "default").Output()
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(out)) // "default via 172.18.0.1 dev eth0 ..."
	for i, f := range fields {
		if f == "dev" && i+1 < len(fields) {
			return fields[i+1], nil
		}
	}
	return "", fmt.Errorf("no default route")
}

func writeSysctl(key, val string) error {
	return os.WriteFile("/proc/sys/"+key, []byte(val), 0o644)
}
