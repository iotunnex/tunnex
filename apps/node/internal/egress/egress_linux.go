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
	"net/netip"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/tunnexio/tunnex/apps/node/internal/nodepolicy"
)

// ifaceRE bounds an interface name to what the kernel allows (Linux IFNAMSIZ-1 = 15,
// alphanumeric + . _ -). wgIface comes from an operator env var and is interpolated into
// the root nft ruleset, so it MUST be validated or a crafted name could inject nft
// statements (review #4).
var ifaceRE = regexp.MustCompile(`^[A-Za-z0-9._-]{1,15}$`)

// Manager reconciles the tunnex nft tables for one WG interface. It also holds the
// latest compiled Zero Trust policy (S7.2): the reconcile loop feeds it via SetPolicy
// on every desired-state fetch, and the forward chain is rendered from it — nil or
// Mesh=true keeps the legacy blanket mesh, enforcing renders default-deny + the
// compiled allow rules.
type Manager struct {
	wgIface string
	policy  atomic.Pointer[nodepolicy.Compiled]
	// apply performs the atomic nft transaction; injectable so the fail-closed +
	// staleness behavior is unit-testable without a real nft/kernel.
	apply func(context.Context, string) error

	mu sync.Mutex
	// applied* is the status of the LAST SUCCESSFUL apply — what is actually in force
	// on the wire. On an apply FAILURE these are left unchanged (the kernel keeps the
	// previous ruleset), so applied != desired signals STALE policy to the control
	// plane (decision 4b / staleness-visible, chunk-1 status field).
	appliedVersion int
	appliedHash    string
	applyErr       error
}

// New builds a Manager for the given WireGuard interface (e.g. wg0).
func New(wgIface string) *Manager { return &Manager{wgIface: wgIface, apply: nftApply} }

// SetPolicy stores the latest compiled policy for the next Reconcile to render. A nil
// policy (open build / no Zero Trust) keeps the legacy blanket mesh.
func (m *Manager) SetPolicy(p *nodepolicy.Compiled) { m.policy.Store(p) }

// AppliedStatus reports the version + short content hash of the policy CURRENTLY IN
// FORCE (last successful apply), and the last apply error if any. The reconcile loop
// puts this on the status channel so the control plane can compare pushed-vs-applied
// and surface a gateway running STALE policy (a policy violation in slow motion).
func (m *Manager) AppliedStatus() (version int, hash string, applyErr error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.appliedVersion, m.appliedHash, m.applyErr
}

// desiredVersion returns the version of the policy the loop last handed us (0 = mesh/
// none). The control plane compares this to AppliedStatus to detect staleness.
func (m *Manager) desiredVersion() int {
	if p := m.policy.Load(); p != nil {
		return p.Version
	}
	return 0
}

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
	// Apply the tables. The whole ruleset is ONE `nft -f -` transaction (add;flush;
	// redefine per family) — an atomic full-chain replace, so there is no empty-chain
	// window (flush + repopulate commit together), it self-heals a table a prior crashed
	// agent left or a manual flush, and a FAILED apply is rejected wholesale by the
	// kernel → the PREVIOUS ruleset stays in force (decision 4a/4b). On failure we DO NOT
	// update applied* (staleness stays visible); on success we record what is in force.
	pol := m.policy.Load() // load ONCE: the ruleset rendered and the status recorded are the same policy
	if err := m.applyAndTrack(ctx, m.rulesetWith(subnet, pol), pol); err != nil {
		return false, err // no nftables / IPv4 NAT support, or a bad ruleset → not egress-capable
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
	return m.rulesetWith(subnet, m.policy.Load())
}

// rulesetWith renders the ruleset for an EXPLICIT policy — Reconcile loads the policy
// once and passes it here AND to applyAndTrack, so the rendered rules and the recorded
// status can never be two different policies (no torn read across a SetPolicy).
func (m *Manager) rulesetWith(subnet string, pol *nodepolicy.Compiled) string {
	wg := m.wgIface
	// Masquerade line present only when the pool subnet is known (wg0 up). Scoped by
	// SOURCE (ip saddr) — reliable in postrouting, unlike iifname — out ANY non-tunnel
	// iface (ECMP/multi-homed-safe). nft masks e.g. 10.99.0.1/24 to the /24 network.
	masq := ""
	if subnet != "" {
		masq = fmt.Sprintf("    ip saddr %s oifname != \"%s\" masquerade\n", subnet, wg)
	}
	v4fwd, v6fwd := m.forwardRules(pol)
	return fmt.Sprintf(`add table ip tunnex
flush table ip tunnex
table ip tunnex {
  chain postrouting {
    type nat hook postrouting priority srcnat; policy accept;
%[2]s  }
  chain forward {
    type filter hook forward priority filter; policy drop;
    ct state established,related accept
%[3]s  }
}
add table ip6 tunnex
flush table ip6 tunnex
table ip6 tunnex {
  chain forward {
    type filter hook forward priority filter; policy drop;
    ct state established,related accept
%[4]s  }
}
`, wg, masq, v4fwd, v6fwd)
}

// forwardRules renders the forward-chain accept lines (after the base policy-drop +
// ct-accept) for the ip and ip6 tables, from the compiled policy:
//
//   - nil policy or Mesh=true (Zero Trust off / open build): the LEGACY blanket mesh —
//     wg0<->wg0 (device↔device) + wg0→egress in v4, wg0<->wg0 in v6. No behavior change.
//   - enforcing: DEFAULT-DENY. Only the compiled allows are emitted, in the compiler's
//     already-sorted order (byte-stable → steady-state reconcile is a no-op). There is
//     NO wg0<->wg0 blanket — device↔device is permitted only by an explicit rule (the
//     S7.1 structural guard, now on the wire). Egress is likewise gated: a device reaches
//     off-pool/internet only via an allow whose dst covers it (e.g. a 0.0.0.0/0 resource),
//     which the masquerade then NATs. v6 is left as pure default-deny (drop + ct only):
//     spokes are v4 (the pool is v4), so there is no v6 device traffic to permit, and
//     dropping it is strictly safer than the blanket mesh.
//
// Every forward rule carries a `counter` (S7.2): per-rule packet/byte counts, near-free
// (a native nft primitive). REPORTING is deferred (the flow-log candidate); emitting now
// reserves the seam and gives the box proof its positive (allow-count) + negative
// (dropCounter) observations for free. counter is in the rendered RULESET only — it is
// NOT part of the canonical Compiled JSON, so the pushed/applied CanonicalHash is
// untouched (no version bump, twin goldens unchanged).
const dropCounter = "    counter comment \"tunnex_default_drop\"\n" // counts unmatched -> policy drop

func (m *Manager) forwardRules(pol *nodepolicy.Compiled) (v4, v6 string) {
	wg := m.wgIface
	if pol == nil || pol.Mesh {
		v4 = fmt.Sprintf("    iifname \"%[1]s\" oifname \"%[1]s\" counter accept\n    iifname \"%[1]s\" oifname != \"%[1]s\" counter accept\n", wg)
		v6 = fmt.Sprintf("    iifname \"%[1]s\" oifname \"%[1]s\" counter accept\n", wg)
		return v4, v6
	}
	var b strings.Builder
	for _, e := range pol.Allow {
		if line, ok := renderAllow(e); ok {
			b.WriteString(line)
		}
	}
	b.WriteString(dropCounter) // count the default-deny drops (box-proof drop observation)
	return b.String(), dropCounter // enforcing v6 = default-deny: no allows, just the drop counter
}

// renderAllow turns one compiled allow into an nft accept line for the ip (v4) forward
// chain, or reports ok=false to SKIP it. Every field is re-emitted through netip as a
// canonical NUMERIC string (never the raw control-plane string) so nothing can inject
// nft statements into this root ruleset — the same hardening as ifaceRE. Ports are
// integers. A v6 destination is skipped (v4 spokes have no route to it; v6 stays
// default-deny).
func renderAllow(e nodepolicy.AllowEntry) (string, bool) {
	src, err := netip.ParseAddr(e.SrcIP)
	if err != nil || !src.Is4() {
		return "", false
	}
	dst, err := netip.ParsePrefix(e.DstCIDR)
	if err != nil || !dst.Addr().Is4() {
		return "", false
	}
	clause := ""
	switch e.Protocol {
	case "tcp", "udp":
		switch {
		case e.PortLow <= 0:
			clause = fmt.Sprintf(" ip protocol %s", e.Protocol)
		case e.PortHigh > e.PortLow:
			clause = fmt.Sprintf(" %s dport %d-%d", e.Protocol, e.PortLow, e.PortHigh)
		default:
			clause = fmt.Sprintf(" %s dport %d", e.Protocol, e.PortLow)
		}
	}
	return fmt.Sprintf("    ip saddr %s ip daddr %s%s counter accept\n", src.String(), dst.Masked().String(), clause), true
}

// applyAndTrack performs the atomic apply and records the fail-closed status: on
// SUCCESS it records the applied policy's version + CANONICAL content hash (what is in
// force); on FAILURE it records only the error and leaves applied version/hash
// UNCHANGED — so the kernel's preserved previous ruleset is reflected as
// applied != desired (STALE), never as a silent success. Extracted from Reconcile so
// the fail-closed behavior is unit-testable with an injected applier (the kernel-level
// rollback itself is a box proof).
//
// HASH DISCIPLINE: the hash is nodepolicy.CanonicalHash(pol) — SHA-256 over the
// canonical Compiled JSON, the SAME bytes the control plane hashes on its side
// (policyspec.CanonicalHash, twin-golden-pinned). NEVER hash the rendered ruleset
// text: it contains node-local state (the masquerade subnet line) the control plane
// cannot reproduce, which would false-positive the staleness alarm permanently.
func (m *Manager) applyAndTrack(ctx context.Context, ruleset string, pol *nodepolicy.Compiled) error {
	if err := m.apply(ctx, ruleset); err != nil {
		m.mu.Lock()
		m.applyErr = err
		m.mu.Unlock()
		return err
	}
	version := 0
	if pol != nil {
		version = pol.Version
	}
	m.mu.Lock()
	m.appliedVersion = version
	m.appliedHash = nodepolicy.CanonicalHash(pol)
	m.applyErr = nil
	m.mu.Unlock()
	return nil
}

// nftApply pipes a ruleset to `nft -f -` (a single atomic netlink transaction: every
// command in the input commits together or the whole batch is rejected).
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
