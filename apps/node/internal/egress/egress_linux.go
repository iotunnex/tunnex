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
	"log/slog"
	"net/netip"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tunnexio/tunnex/apps/node/internal/flowlog"
	"github.com/tunnexio/tunnex/apps/node/internal/nodepolicy"
)

// ifaceRE bounds an interface name to what the kernel allows (Linux IFNAMSIZ-1 = 15,
// alphanumeric + . _ -). wgIface comes from an operator env var and is interpolated into
// the root nft ruleset, so it MUST be validated or a crafted name could inject nft
// statements (review #4).
var ifaceRE = regexp.MustCompile(`^[A-Za-z0-9._-]{1,15}$`)

// ruleIDRE bounds a rule_id (observability metadata) to the canonical UUID shape before it is
// interpolated into the root nft ruleset — the A-1 discipline applied to the one renderer
// field that isn't numeric (review #7). A non-match drops the id rather than widening trust.
var ruleIDRE = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// Manager reconciles the tunnex nft tables for one WG interface. It also holds the
// latest compiled Zero Trust policy (S7.2): the reconcile loop feeds it via SetPolicy
// on every desired-state fetch, and the forward chain is rendered from it — nil or
// Mesh=true keeps the legacy blanket mesh, enforcing renders default-deny + the
// compiled allow rules.
type Manager struct {
	wgIface string
	policy  atomic.Pointer[nodepolicy.Compiled]
	// deviceByIP is the src /32 -> device_id map (S7.5.4 v3), rebuilt atomically on each
	// SetPolicy from the applied Allow set. It is the AUTHORITATIVE /32->device mapping
	// the CP compiled (the same snapshot that assigned the /32) — the flow-log stamper
	// consults it so a flow event carries device identity WITHOUT any src_ip->device DB
	// guess (the forbidden racy IP-map). Observability only; never touches enforcement.
	deviceByIP atomic.Pointer[map[string]string]
	// policyReceived distinguishes "no policy fetched YET" (cold start, before the first
	// desired-state delivery) from "policy fetched, value nil = legacy mesh". THREE states
	// (finding #2): (a) received + mesh/nil -> blanket; (b) received + enforcing -> grants;
	// (c) NEVER received -> DENY-ALL regardless of mode. The initial synchronous Reconcile
	// runs before OnPolicy is wired, so without this the forward chain would render the
	// blanket mesh (fail-OPEN) on every restart of an enforcing gateway until the first
	// fetch lands. Deny-until-first-policy is fail-CLOSED; an off-mode org's brief
	// restart blip (denied until the first fetch) is the correct trade for a security
	// boundary. This SPLITS the chunk-1 absent=Mesh decision: nil-WITHIN-a-received-policy
	// = mesh (unchanged); NEVER-received != mesh = deny.
	policyReceived atomic.Bool
	// refusedVersion is the compiled-artifact Version the agent last REFUSED because it
	// exceeds nodepolicy.MaxSupportedVersion (S8.1 D1 fail-closed gate). 0 = none refused.
	// A refusal forces the forward chain to DENY-ALL (never a best-effort apply of a shape
	// the agent can't interpret, never a fall-through to legacy mesh) and is reported so the
	// control plane surfaces `unsupported_policy_version`. Cleared when a supported version
	// arrives.
	refusedVersion atomic.Int64
	// maxPolicyVersion is the highest compiled-artifact Version this agent applies (defaults to
	// nodepolicy.MaxSupportedVersion). A field, not the const directly, so the interlock red can pin
	// an OLD-max agent (S8.1 Slice 3) and feed it the current-version artifact.
	maxPolicyVersion int
	// apply performs the atomic nft transaction; injectable so the fail-closed +
	// staleness behavior is unit-testable without a real nft/kernel.
	apply func(context.Context, string) error
	// nftRun runs an arbitrary `nft <args...>` (list/insert/delete) and returns stdout;
	// injectable for the DOCKER-USER foreign-chain reconcile tests (WF-4). Distinct from
	// `apply` (the atomic `-f -` full-table replace) — the Docker-owned chain can't be
	// flushed, so its rules are managed one at a time by handle.
	nftRun func(context.Context, ...string) (string, error)
	// forwardBlocked (WF-4 / D-WF4-d): true when this is a Docker host (DOCKER-USER exists),
	// FORWARD is policy-drop, there ARE remote routes to carry, yet the agent could NOT place
	// its DOCKER-USER accept — so forwarded site traffic is silently dropped by Docker's chain.
	// Surfaced as site_subnet_unreachable (the advertised subnet is not reachable via this
	// gateway) so the health surface shows it LOUD rather than blackholing green.
	forwardBlocked atomic.Bool

	mu sync.Mutex
	// applied* is the status of the LAST SUCCESSFUL apply — what is actually in force
	// on the wire. On an apply FAILURE these are left unchanged (the kernel keeps the
	// previous ruleset), so applied != desired signals STALE policy to the control
	// plane (decision 4b / staleness-visible, chunk-1 status field).
	appliedVersion int
	appliedHash    string
	// appliedEnforcing is whether the policy CURRENTLY IN FORCE (last successful apply) is
	// an ENFORCING one. It distinguishes the two non-enforcing apply-failure cases
	// (finding #B): a gateway that was enforcing and FAILS to apply the new mesh/off
	// ruleset is STUCK enforcing a disabled policy (surface it — silent stale policy is a
	// violation in slow motion), whereas a gateway that was never enforcing (open build /
	// off) whose egress-NAT arm fails is not a policy concern (#6 — stays quiet).
	appliedEnforcing bool
	applyErr         error
	// failingSince is the instant apply FIRST started failing (the mismatch onset),
	// cleared the moment an apply SUCCEEDS. The control plane's stale alarm measures
	// (now - failingSince), NOT the applied-hash age — so a NORMAL push that applies
	// cleanly never registers stale, and the 90s window measures the real mismatch
	// duration (box-proof finding #3). now() is injectable for tests.
	failingSince time.Time
	now          func() time.Time
	// flowLogGroup is the nflog group the forward-chain accept/deny rules log to (S7.5.1
	// flow observation). 0 = flow logging OFF: the forward chain renders EXACTLY as before
	// (no log clauses) — the safety default, so enabling observation is opt-in and its
	// absence is byte-for-byte the pre-S7.5.1 enforcement ruleset.
	flowLogGroup int
}

// New builds a Manager for the given WireGuard interface (e.g. wg0).
func New(wgIface string) *Manager {
	return &Manager{wgIface: wgIface, apply: nftApply, nftRun: nftRun, now: time.Now, maxPolicyVersion: nodepolicy.MaxSupportedVersion}
}

// ForwardBlocked reports the WF-4 / D-WF4-d condition: a Docker host whose FORWARD DROP is
// swallowing forwarded site traffic the agent couldn't clear. The reconcile loop feeds this
// into the site_subnet_unreachable health signal so it never blackholes green.
func (m *Manager) ForwardBlocked() bool { return m.forwardBlocked.Load() }

// SetFlowLogGroup enables flow logging by pointing the forward-chain log clauses at an
// nflog group (>0). 0 disables it. Non-terminal + best-effort: the log clause NEVER changes
// a packet's accept/drop fate (kernel semantics), so this cannot affect enforcement.
func (m *Manager) SetFlowLogGroup(group int) { m.flowLogGroup = group }

// SetPolicy stores the latest compiled policy (nil = legacy mesh) and marks that a
// policy has now been received — flipping the forward chain out of the cold-start
// deny-all state. Called on EVERY desired-state delivery (including nil for off orgs).
func (m *Manager) SetPolicy(p *nodepolicy.Compiled) {
	// S8.1 D1 fail-closed gate: an artifact whose Version exceeds what this agent can apply
	// is REFUSED — the agent does NOT store it as the active policy (rendering its fields
	// would be a best-effort apply of a shape it can't interpret) and does NOT fall through
	// to legacy mesh (fail-OPEN). It records the refused version (forcing DENY-ALL in
	// forwardRules) and reports it. The last-good policy is left in place but overridden by
	// the deny-all refusal; a supported version clears the refusal.
	if p != nil && p.Version > m.maxPolicyVersion {
		m.refusedVersion.Store(int64(p.Version))
		m.policyReceived.Store(true) // past cold-start: the refusal, not the cold deny, is the reason
		return
	}
	m.refusedVersion.Store(0)
	m.policy.Store(p)
	m.policyReceived.Store(true)
	// Rebuild the src /32 -> device_id map (v3 flow-log attribution). Every device with
	// any grant appears as a source /32 in Allow, so this is the full authoritative map.
	byIP := map[string]string{}
	if p != nil {
		for _, e := range p.Allow {
			if e.SrcIP != "" && e.SrcDeviceID != "" {
				byIP[e.SrcIP] = e.SrcDeviceID
			}
		}
	}
	m.deviceByIP.Store(&byIP)
}

// DeviceForIP returns the source device's uuid for a flow's src /32, from the APPLIED
// artifact map (S7.5.4 v3). "" when the src has no grant (default-deny source) or no
// policy has been received — reported as unresolved, never guessed. Cheap map read.
func (m *Manager) DeviceForIP(srcIP string) string {
	if mp := m.deviceByIP.Load(); mp != nil {
		return (*mp)[srcIP]
	}
	return ""
}

// AppliedStatus reports the version + canonical hash of the policy CURRENTLY IN FORCE
// (last successful apply), the last apply error, and failingSince — the mismatch
// onset (zero when apply is healthy). The reconcile loop puts these on the status
// channel so the control plane can surface a gateway running STALE policy.
func (m *Manager) AppliedStatus() (version int, hash string, failingSince time.Time, applyErr error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.appliedVersion, m.appliedHash, m.failingSince, m.applyErr
}

// RefusedVersion returns the compiled-artifact Version the agent last REFUSED as
// unsupported (S8.1 D1), or 0 when none. The reconcile loop reports this so the control
// plane surfaces `unsupported_policy_version` (remedy: upgrade the agent).
func (m *Manager) RefusedVersion() int { return int(m.refusedVersion.Load()) }

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
	// WF-4: on a Docker host, clear Docker's `filter FORWARD` DROP for the approved site routes
	// (a Routes-scoped DOCKER-USER accept) so site-to-site forwarding works with zero gateway touch.
	// Best-effort + idempotent; a Docker-blocked forward it can't clear is surfaced via ForwardBlocked().
	var routeCIDRs, localSubnets []string
	var poolCIDR string
	if pol != nil {
		for _, rt := range pol.Routes {
			routeCIDRs = append(routeCIDRs, rt.DstCIDR)
		}
		// WF-4-local: this gateway's OWN advertised subnets (LocalSubnets) also need a DOCKER-USER accept —
		// a split-tunnel device reaching the LAN BEHIND this gateway is forwarded wg0→eth0 and Docker's
		// FORWARD DROP swallows it exactly as it did remote routes. Same union, mirrored orientation.
		localSubnets = append(localSubnets, pol.LocalSubnets...)
		// A3b (v6): the org device pool — the pool-class accepts (relaxed, wg0↔wg0 included) so Docker
		// never structurally drops device transit or device↔device; the ip tunnex chain adjudicates.
		poolCIDR = pol.PoolCIDR
	}
	m.reconcileDockerForward(ctx, routeCIDRs, localSubnets, poolCIDR)
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
	v4fwd, v6fwd := m.forwardRules(pol, m.policyReceived.Load())
	// S8.2 D9 MSS clamp: on the INTRA-TUNNEL forward path (wg0→wg0 — device-to-device and site-to-site,
	// where a client-WG session can ride a site-WG link and PMTUD fails silently inside the tunnels),
	// clamp each TCP SYN's MSS down to the path MTU. This is the classic "ping works, large transfer
	// freezes" fix. HONEST SCOPE (reassuring-comment law): it clamps TCP ONLY (UDP/ICMP-dependent PMTUD
	// is unaffected — those rely on the link MTU / fragmentation) and only NEW connections (the SYN);
	// it does not otherwise change forwarding. Node-local rendered rule, OUTSIDE CanonicalHash (the
	// masquerade class, D2) — no version bump, twin goldens untouched. Non-terminal: it modifies then
	// continues to the grant/drop below.
	return fmt.Sprintf(`add table ip tunnex
flush table ip tunnex
table ip tunnex {
  chain postrouting {
    type nat hook postrouting priority srcnat; policy accept;
%[2]s  }
  chain forward {
    type filter hook forward priority filter; policy drop;
    ct state established,related accept
    iifname "%[1]s" oifname "%[1]s" tcp flags syn tcp option maxseg size set rt mtu
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

func (m *Manager) forwardRules(pol *nodepolicy.Compiled, received bool) (v4, v6 string) {
	wg := m.wgIface
	if !received {
		// COLD START, no policy fetched yet -> DENY-ALL (drop + ct only, no accepts).
		// Fail-closed until the first desired-state delivery, so an enforcing gateway is
		// never briefly wide-open on restart (finding #2). NOT the same as nil-in-received.
		return dropCounter, dropCounter
	}
	if m.refusedVersion.Load() > 0 {
		// S8.1 D1: an unsupported-version artifact was refused -> DENY-ALL (the same
		// fail-closed shape as cold start). NEVER fall through to the mesh/enforcing render
		// below, which would apply a shape the agent can't interpret or open the mesh.
		return dropCounter, dropCounter
	}
	if pol == nil || pol.Mesh {
		v4 = fmt.Sprintf("    iifname \"%[1]s\" oifname \"%[1]s\" counter accept\n    iifname \"%[1]s\" oifname != \"%[1]s\" counter accept\n", wg)
		// S8.2c D1: SYMMETRIC site forwarding in mesh. Mesh means "no doors" — a behind-gateway host must
		// be able to INITIATE to a remote site (LAN→tunnel), not just receive. The wg0-ingress accepts
		// above cover tunnel→LAN + spoke↔spoke but NOT LAN→tunnel (the S3.7 "egress LAN can never initiate
		// into spokes" stance). We open LAN→tunnel SCOPED TO THE REMOTE SITE SUBNETS (pol.Routes) only —
		// so the S3.7 spoke-isolation HOLDS (the device pool 10.99.x is never a Route, so the egress LAN
		// still can't reach device spokes; only approved site-to-site subnets). Canonically re-emitted
		// (netip) so nothing injects nft statements. Enforcing keeps its grant-gated forward (allowMatch).
		if pol != nil {
			for _, rt := range pol.Routes {
				if p, err := netip.ParsePrefix(rt.DstCIDR); err == nil && p.Addr().Is4() {
					v4 += fmt.Sprintf("    iifname != \"%[1]s\" oifname \"%[1]s\" ip daddr %[2]s counter accept\n", wg, p.Masked().String())
				}
			}
		}
		v6 = fmt.Sprintf("    iifname \"%[1]s\" oifname \"%[1]s\" counter accept\n", wg)
		return v4, v6
	}
	var b strings.Builder
	g := m.flowLogGroup
	for _, e := range pol.Allow {
		// Compute ONE form (review #9): the logged variant when logging is on, else the plain
		// one — never both (allowMatch re-parses src/dst via netip, so the throwaway call
		// doubled the per-rule work every reconcile).
		var line string
		var ok bool
		if g > 0 {
			line, ok = renderAllowLogged(e, g)
		} else {
			line, ok = renderAllow(e)
		}
		if ok {
			b.WriteString(line)
		}
	}
	b.WriteString(denyDrop(g))     // count (+ log when on) the default-deny drops
	return b.String(), denyDrop(g) // enforcing v6 = default-deny: no allows, just the deny tail
}

// denyDrop is the default-deny tail. g==0: the original counter (relies on the chain's
// policy drop) — byte-identical to pre-S7.5.1. g>0: additionally LOG the unmatched NEW
// flow (flow-start deny, D1) with the deny sentinel, then count + drop. The verdict is
// drop either way; the log is the sole addition (non-terminal). The deny-log is the
// port-scan amplification point — aggregated CP-side (4/n), nflog socket sized for it.
func denyDrop(group int) string {
	if group <= 0 {
		return dropCounter
	}
	return fmt.Sprintf("    ct state new%s counter drop comment \"tunnex_default_drop\"\n", logClause(flowlog.EncodePrefix(""), group))
}

// allowMatch turns one compiled allow into the ENFORCEMENT clause (match + counter, NO
// verdict) for the ip (v4) forward chain, or reports ok=false to SKIP it. This is the
// rule_id-INDEPENDENT part that decides packet fate — renderAllow / renderAllowLogged
// append the verdict (and, for the logged form, an observation clause) to it. Every field
// is re-emitted through netip as a canonical NUMERIC string (never the raw control-plane
// string) so nothing can inject nft statements into this root ruleset — the same hardening
// as ifaceRE. Ports are integers. A v6 destination is skipped (v4 spokes have no route to
// it; v6 stays default-deny).
func allowMatch(e nodepolicy.AllowEntry) (string, bool) {
	// SOURCE match: a DEVICE source is a bare host ("10.99.0.7"); a SITE source (v5, S8.2) is a LAN CIDR
	// ("10.1.0.0/24"). Accept BOTH, fail closed on anything else. Re-emit canonically (never the raw CP
	// string) so nothing can inject nft statements. The v4 renderer used ParseAddr only — a CIDR source
	// was SKIPPED (silent under-enforcement), which is exactly why a CIDR source triggers the v5 gate.
	var srcMatch string
	if strings.Contains(e.SrcIP, "/") {
		p, err := netip.ParsePrefix(e.SrcIP)
		if err != nil || !p.Addr().Is4() {
			return "", false
		}
		srcMatch = p.Masked().String()
	} else {
		a, err := netip.ParseAddr(e.SrcIP)
		if err != nil || !a.Is4() {
			return "", false
		}
		srcMatch = a.String()
	}
	dst, err := netip.ParsePrefix(e.DstCIDR)
	if err != nil || !dst.Addr().Is4() {
		return "", false
	}
	// CONVENTION (fail-closed rendering): this renderer REFUSES any unknown or half-
	// specified field — it skips the rule (-> no match -> default-deny) and NEVER widens
	// on it. validateResource is the first gate, but a compromised or future control
	// plane could still emit a malformed artifact, so the renderer never trusts it. This
	// has bitten twice (port range #1, protocol #6); it is a checklist line for every new
	// field added to AllowEntry. ALSO (A-1, S7.5.1): classify every new field
	// enforcement-vs-observability — enforcement fields go into CanonicalHash's projection
	// (nodepolicy/policyspec hash.go); observability fields (e.g. rule_id) stay OUT of it
	// AND out of this renderer, so the hash and the packet fate ignore them alike.
	clause := ""
	switch e.Protocol {
	case "any":
		// All protocols for this src/dst — the intended wide grant; clause stays empty.
	case "tcp", "udp":
		lowSet, highSet := e.PortLow > 0, e.PortHigh > 0
		switch {
		case !lowSet && !highSet:
			// Both unset = any port of this protocol (the "no port range" case).
			clause = fmt.Sprintf(" ip protocol %s", e.Protocol)
		case lowSet && highSet && e.PortHigh >= e.PortLow:
			if e.PortHigh > e.PortLow {
				clause = fmt.Sprintf(" %s dport %d-%d", e.Protocol, e.PortLow, e.PortHigh)
			} else {
				clause = fmt.Sprintf(" %s dport %d", e.Protocol, e.PortLow)
			}
		default:
			// A HALF-SET or inverted range (only low, only high, or high<low) is
			// malformed. FAIL CLOSED: skip the rule -> default-deny, NEVER widen to
			// all-ports (finding #1).
			return "", false
		}
	default:
		// Unknown/empty protocol. The compiler only emits any/tcp/udp, but the renderer
		// does not rely on that: an unrecognized value FAILS CLOSED (skip -> default-deny),
		// symmetric with the half-set-port refusal — never a silent all-protocol widen
		// (finding #6).
		return "", false
	}
	return fmt.Sprintf("    ip saddr %s ip daddr %s%s counter", srcMatch, dst.Masked().String(), clause), true
}

// renderAllow is the ENFORCEMENT-ONLY accept line (no observation). rule_id-INDEPENDENT.
func renderAllow(e nodepolicy.AllowEntry) (string, bool) {
	m, ok := allowMatch(e)
	if !ok {
		return "", false
	}
	return m + " accept\n", true
}

// renderAllowLogged is renderAllow PLUS an nflog observation clause carrying the grant's
// rule_id (S7.5.1, decision (a)). The log clause is the SOLE delta vs renderAllow and is
// NON-TERMINAL — `log` cannot change the accept verdict (kernel semantics). Scoping: the
// established-accept line above short-circuits established flows, so this per-rule accept
// only sees a flow's FIRST packet → one log per flow-start (D1). group is the nflog group
// the flowlog reader listens on.
func renderAllowLogged(e nodepolicy.AllowEntry, group int) (string, bool) {
	// rule_id is the ONE renderer field that isn't a number — validate it to the canonical UUID
	// shape before it enters the root nft ruleset (the A-1 fail-closed discipline, review #7).
	// A non-conforming rule_id renders the accept WITHOUT a log clause: NOT an empty prefix
	// (EncodePrefix("") is the DENY sentinel, which would misclassify this ACCEPTED flow as a
	// deny) and NOT a raw interpolation. Fail-closed on OBSERVABILITY only — the packet is still
	// correctly accepted. In practice the compiler always stamps a DB uuid; this defends a
	// future/compromised artifact, matching allowMatch's netip re-emission of src/dst/port.
	if !ruleIDRE.MatchString(e.RuleID) {
		return renderAllow(e)
	}
	m, ok := allowMatch(e)
	if !ok {
		return "", false
	}
	return m + logClause(flowlog.EncodePrefix(e.RuleID), group) + " accept\n", true
}

// logClause renders a non-terminal nflog statement. prefix names the grant (or the deny
// sentinel); group is the nflog group. Placed BEFORE the verdict so the kernel logs the
// matched packet then proceeds to accept/drop unchanged.
func logClause(prefix string, group int) string {
	return fmt.Sprintf(" log prefix %q group %d", prefix, group)
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
	// POLICY staleness applies ONLY to an ENFORCING policy. A failure while rendering the
	// mesh/off/open ruleset is an S3.7 EGRESS-NAT arm problem (surfaced via egress_nat=false
	// + logs), NOT Zero Trust policy staleness — so it must NOT set policy_error/failingSince
	// (finding #6: a nftless open-build gateway must not report itself policy-stale).
	isPolicy := pol != nil && pol.Mode == nodepolicy.ModeEnforcing
	err := m.apply(ctx, ruleset)
	m.mu.Lock()
	defer m.mu.Unlock()
	if !isPolicy {
		// Desired state is NON-enforcing (mesh / off / open build).
		if err == nil {
			// Applied cleanly: mesh/off is now in force. Clear all policy status.
			m.appliedVersion = 0
			m.appliedHash = nodepolicy.CanonicalHash(pol) // "" for nil, mesh hash otherwise
			m.appliedEnforcing = false
			m.applyErr = nil
			m.failingSince = time.Time{}
			return nil
		}
		// The apply FAILED, so the kernel keeps the PREVIOUS ruleset in force.
		if m.appliedEnforcing {
			// STUCK ENFORCING: the org disabled Zero Trust (or reverted to mesh) but the
			// gateway could not swap out the enforcing chain — it is still enforcing a
			// DISABLED policy, invisibly denying traffic. Surface it via applyErr (an
			// immediate policy_error), the "silent stale policy = violation in slow motion"
			// DoD (finding #B). appliedHash/appliedEnforcing stay (enforcing is what's in
			// force). failingSince stays enforcing-scoped — applyErr is the signal here.
			m.applyErr = err
			return err
		}
		// Never enforcing (open build / off egress-arm failure): NOT a policy concern —
		// the egress-capability path (egress_nat=false + logs) carries this. Stay quiet so
		// a nftless open-build gateway never reports itself policy-stale (finding #6).
		m.applyErr = nil
		m.failingSince = time.Time{}
		return err
	}
	if err != nil {
		m.applyErr = err
		if m.failingSince.IsZero() { // stamp the mismatch ONSET, once
			m.failingSince = m.now()
		}
		return err
	}
	m.appliedVersion = pol.Version
	m.appliedHash = nodepolicy.CanonicalHash(pol)
	m.appliedEnforcing = true
	m.applyErr = nil
	m.failingSince = time.Time{} // apply succeeded -> no mismatch -> not stale
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

// nftRun runs `nft <args...>` and returns stdout. Used by the DOCKER-USER foreign-chain
// reconcile (WF-4) for list/insert/delete, which — unlike the atomic tunnex-table replace —
// must edit a Docker-owned chain one rule at a time (never flush it).
func nftRun(ctx context.Context, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, "nft", args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("nft %s: %w: %s", strings.Join(args, " "), err, bytes.TrimSpace(out))
	}
	return string(out), nil
}

const dockerUserComment = "tunnex-site-fwd" // marks the agent's own DOCKER-USER rules for idempotent find + full-sweep

// captures direction (s|d addr) + the address + the handle, for a comment-marked rule. BOTH directions
// are Route-scoped (forward: daddr=route; return: saddr=route) — the return path is why a single-direction
// accept passed the forward ping but dropped the reply on the re-walk.
// captures: (1) the iif/oif orientation prefix (for drift-detection, S8.6b), (2) direction s|d, (3) the addr,
// (4) the handle. The orientation prefix distinguishes an old iif!=wg0-predicated rule from the relaxed form
// under the same daddr/saddr key.
var dockerUserRuleRE = regexp.MustCompile(`((?:iifname|oifname)[^\n]*?)?ip ([sd])addr (\S+).*comment "` + dockerUserComment + `".*# handle (\d+)`)

// orientSig canonicalizes a rule's iif/oif match predicates (spaces + quotes stripped) so nft's printed form
// and our insert-args form normalize identically — the drift-detection comparator (S8.6b D-transit-2).
func orientSig(s string) string { return strings.NewReplacer(" ", "", `"`, "").Replace(s) }

// argOrientSig derives the orientation signature from an insert-args vector: the tokens BEFORE "ip" (the
// iifname/oifname clause), normalized the same way as orientSig.
func argOrientSig(args []string) string {
	var pre []string
	for _, t := range args {
		if t == "ip" {
			break
		}
		pre = append(pre, t)
	}
	return orientSig(strings.Join(pre, " "))
}

// reconcileDockerForward makes forwarding work on a DOCKER host with ZERO gateway touch (WF-4).
// Docker sets `filter FORWARD` policy DROP + a DOCKER-USER hook; the agent's `ip tunnex` forward
// accept is a SEPARATE base chain, so Docker's drop terminally kills the forwarded packet even
// after the ZT chain accepted it. This inserts SCOPED accepts into DOCKER-USER (jumped FIRST from
// FORWARD; an accept there clears the hook's drop) — mirroring the tunnex rule, NEVER a blanket
// ACCEPT. TWO scoped sets: remote Routes (site-to-site, S8.2c) AND this gateway's own LocalSubnets
// (WF-4-local, S8.5 — a split-tunnel device reaching the LAN behind its gateway is forwarded
// wg0→eth0 and Docker's drop swallowed it; the ZT chain accepted it, proven on the wire). The `ip
// tunnex` chain still ENFORCES the grant (enforcing with no grant stays 100% loss even with the
// DOCKER-USER accept), so this only lifts Docker's structural isolation, never the policy.
//
// Idempotent (list → insert only what's missing) + full-sweep (delete comment-marked rules whose
// addr left Routes∪LocalSubnets) → re-run every reconcile tick, so a dockerd reload that recreates
// DOCKER-USER self-heals within one interval (D-WF4-a). Docker-CONDITIONAL: no DOCKER-USER chain
// (bare metal / the D4 bare-metal path) → no-op, forwarding rides the host's own FORWARD (D-WF4-c).
// Returns forwardBlocked when we have subnets to carry, FORWARD is policy-drop, yet we could not
// place the accept — the D-WF4-d loud signal.
func (m *Manager) reconcileDockerForward(ctx context.Context, routes, localSubnets []string, poolCIDR string) (forwardBlocked bool) {
	wg := m.wgIface
	// Probe DOCKER-USER. Absent → not a Docker-managed FORWARD host; nothing to satisfy.
	if _, err := m.nftRun(ctx, "list", "chain", "ip", "filter", "DOCKER-USER"); err != nil {
		m.forwardBlocked.Store(false)
		return false
	}
	// Desired = TWO accepts per v4 CIDR — forward (daddr) AND return (saddr) — keyed "d:"/"s:" + the CANONICAL
	// address nft PRINTS (host route bare, else masked). Both directions are needed: a forward-only accept
	// passed the ping's echo-request but Docker's FORWARD DROP killed the reply (re-walk). #1: nft drops the
	// /32 from a host addr, so keying on Masked() "x/32" would never match the listed bare "x" and thrash —
	// canonDaddr keys both sides the same way. args are built from canonical prefixes (no operator/CP string
	// reaches nft raw). Routes and LocalSubnets are DISJOINT by construction (remote vs this-gateway), so the
	// "d:"/"s:" address keying never collides across the two orientations.
	desired := map[string]bool{}
	insertArgs := map[string][]string{}
	desiredSig := map[string]string{} // key -> iif/oif orientation signature (drift-detection, S8.6b D-transit-2)
	comment := `"` + dockerUserComment + `"`
	set := func(k string, args []string) {
		desired[k] = true
		insertArgs[k] = args
		desiredSig[k] = argOrientSig(args)
	}
	// REMOTE routes (S8.6b D-transit-1, RELAXED): Docker must not structurally drop traffic whose daddr/saddr
	// is a Route — the ZT chain adjudicates. The old iif!=wg0/oif!=wg0 predicates were "the direction the walk
	// proved" (eth0→wg0), never a security predicate (see docs/S8.6-decisions.md — narrowing-was-incidental).
	// Relaxed, ONE rule covers eth0→wg0 (route) AND wg0→wg0 (device→remote-site hub transit). Forward =
	// oif=wg0, daddr=route; return = iif=wg0, saddr=route. A future PR must NOT re-add the iif/oif predicates.
	for _, c := range routes {
		if p, err := netip.ParsePrefix(c); err == nil && p.Addr().Is4() {
			a := canonDaddr(p)
			set("d:"+a, []string{"oifname", wg, "ip", "daddr", a, "counter", "accept", "comment", comment})
			set("s:"+a, []string{"iifname", wg, "ip", "saddr", a, "counter", "accept", "comment", comment})
		}
	}
	// WF-4-local (S8.5): this gateway's OWN advertised subnets. A DEVICE (wg0) initiates IN to the local LAN
	// (eth0) — the MIRROR of the route orientation. Forward = iif=wg0 → oif!=wg0, daddr=localsubnet; return =
	// iif!=wg0 → oif=wg0, saddr=localsubnet. Without this, Docker's FORWARD DROP swallows the device→own-LAN
	// forward even though the ZT chain accepted it (wire-proven). Same marked/swept discipline as routes.
	for _, c := range localSubnets {
		if p, err := netip.ParsePrefix(c); err == nil && p.Addr().Is4() {
			a := canonDaddr(p)
			if desired["d:"+a] || desired["s:"+a] {
				continue // a route already claimed this addr (disjoint-by-construction guard); do not overwrite
			}
			set("d:"+a, []string{"iifname", wg, "oifname", "!=", wg, "ip", "daddr", a, "counter", "accept", "comment", comment})
			set("s:"+a, []string{"iifname", "!=", wg, "oifname", wg, "ip", "saddr", a, "counter", "accept", "comment", comment})
		}
	}
	// POOL class (A3b v6, fork-ruled (ii) RELAXED): the org device pool. Forward = oif=wg0, daddr=pool
	// (LAN→device replies AND wg0→wg0 device↔device forward); return = iif=wg0, saddr=pool (device-sourced
	// any direction, incl. wg0→wg0 transit at a hub). NO iif/oif exclusions — Docker's match tier never
	// structurally drops what the ip tunnex chain is entitled to adjudicate (the D-transit-3 boundary,
	// applied uniformly to the pool class; the amended D-A3b-1 condition). Under enforcing, device↔device
	// without a grant still drops AT THE CHAIN with counter evidence (the re-targeted spoke-isolation red).
	// Same ONE engine: same comment marker, same "d:"/"s:" key space, same drift-detection transition.
	// Existing-key guard mirrors localSubnets (a pool colliding with a route/local addr never overwrites).
	if poolCIDR != "" {
		if p, err := netip.ParsePrefix(poolCIDR); err == nil && p.Addr().Is4() {
			a := canonDaddr(p)
			if !desired["d:"+a] && !desired["s:"+a] {
				set("d:"+a, []string{"oifname", wg, "ip", "daddr", a, "counter", "accept", "comment", comment})
				set("s:"+a, []string{"iifname", wg, "ip", "saddr", a, "counter", "accept", "comment", comment})
			}
		}
	}
	// Current tunnex-marked rules: "dir:addr" -> handle. A LIST ERROR (not just empty) means we can't know
	// what's in force → SKIP add/sweep this tick and keep the prior signal (#2: blindly inserting on an
	// unread list duplicates accepts the sweep can't reap, since they ARE in desired). Next tick retries.
	listing, err := m.nftRun(ctx, "-a", "list", "chain", "ip", "filter", "DOCKER-USER")
	if err != nil {
		return m.forwardBlocked.Load()
	}
	// current: key -> {handle, orientation signature}. The SIGNATURE (drift-detection, S8.6b D-transit-2) lets
	// a reconcile REPLACE an old orientation-predicated rule with the relaxed form under the SAME daddr/saddr
	// key — key-only idempotence would skip it (key present) and strand the old rule, breaking transit forever.
	type curRule struct {
		handle string
		sig    string
	}
	current := map[string]curRule{}
	for _, mt := range dockerUserRuleRE.FindAllStringSubmatch(listing, -1) {
		orient, dir, addr, handle := mt[1], mt[2], mt[3], mt[4]
		key := ""
		if p, e := netip.ParsePrefix(addr); e == nil {
			key = dir + ":" + canonDaddr(p)
		} else if a, e := netip.ParseAddr(addr); e == nil {
			key = dir + ":" + a.String() // nft prints a host route as a bare address
		}
		if key != "" {
			current[key] = curRule{handle: handle, sig: orientSig(orient)}
		}
	}
	placeErr := false
	// Add missing OR REPLACE drifted — INSERT (prepend) so it precedes DOCKER-USER's trailing RETURN. A key
	// present with a MATCHING signature is idempotent (skip); present with a DIFFERENT signature (an old
	// orientation-predicated rule vs the relaxed desired) is deleted first, then re-inserted — one pass, no
	// orphan window (D-transit-2 sweep-hygiene).
	for key, args := range insertArgs {
		if cur, have := current[key]; have {
			if cur.sig == desiredSig[key] {
				continue // idempotent — same rule already in force
			}
			// drifted: remove the stale-orientation rule before placing the relaxed one (same key)
			if _, err := m.nftRun(ctx, "delete", "rule", "ip", "filter", "DOCKER-USER", "handle", cur.handle); err != nil {
				placeErr = true
				continue // couldn't remove the old rule → don't stack a second; retry next tick
			}
			delete(current, key) // it's gone; the sweep must not try to delete it again by the old handle
		}
		if _, err := m.nftRun(ctx, append([]string{"insert", "rule", "ip", "filter", "DOCKER-USER"}, args...)...); err != nil {
			placeErr = true
		}
	}
	// Full-sweep: delete comment-marked rules whose daddr left Routes. #5: surface a failed delete (a
	// lingering foreign-chain accept is retried next tick, but a persistent failure must not be silent).
	for key, cur := range current {
		if desired[key] {
			continue
		}
		if _, err := m.nftRun(ctx, "delete", "rule", "ip", "filter", "DOCKER-USER", "handle", cur.handle); err != nil {
			slog.Warn("docker_user_sweep_failed", "handle", cur.handle, "daddr", key, "error", err.Error())
		}
	}
	// D-WF4-d: routes to carry + FORWARD policy-drop + our accept couldn't be placed → blocked (loud).
	blocked := len(desired) > 0 && placeErr && forwardPolicyIsDrop(ctx, m.nftRun)
	m.forwardBlocked.Store(blocked)
	return blocked
}

// canonDaddr returns the daddr string nft PRINTS for a prefix: a host route (/32 v4, /128 v6) as the BARE
// address, any other prefix as the masked CIDR. Keying idempotence on this form makes the /32 case match
// (nft drops the /32) instead of thrashing insert+delete every tick.
func canonDaddr(p netip.Prefix) string {
	if p.Bits() == p.Addr().BitLen() {
		return p.Addr().String()
	}
	return p.Masked().String()
}

// forwardPolicyIsDrop reports whether the `ip filter FORWARD` base chain is policy drop (the
// Docker default that swallows forwarded traffic). Best-effort: an unreadable chain → false
// (don't manufacture a blocked signal we can't substantiate).
func forwardPolicyIsDrop(ctx context.Context, run func(context.Context, ...string) (string, error)) bool {
	out, err := run(ctx, "list", "chain", "ip", "filter", "FORWARD")
	if err != nil {
		return false
	}
	return strings.Contains(out, "policy drop")
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
