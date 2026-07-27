//go:build linux

package egress

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/tunnexio/tunnex/apps/node/internal/nodepolicy"
)

// S10.3 Slice 4b — the K8s VIP DNAT (highest-privilege surface). A client reaches an exposed Service at its
// synthetic VIP; this gateway DNATs VIP -> the Service's real ClusterIP, and kube-proxy then DNATs
// ClusterIP -> a pod. The ClusterIP is resolved from <service>.<namespace> via in-cluster CoreDNS (ruling
// (A): no K8s API). FAIL-CLOSED at every branch: the ONLY input that programs a DNAT is exactly ONE
// resolved address INSIDE the registered Service CIDR (a ClusterIP). Resolution is bounded + decoupled from
// the apply path; the render is pure.

// resolveTimeout bounds ONE Service lookup so a slow/hanging cluster DNS never stalls the resolve loop
// (which is itself decoupled from Reconcile/apply — the render reads the last-resolved map).
const resolveTimeout = 2 * time.Second

// ErrDNSUnreachable: the cluster DNS SERVER could not be reached (vs NXDOMAIN = server up, name absent).
// Drives the k8s_cluster_dns_unreachable preflight. Both are fail-closed (no DNAT programmed).
var ErrDNSUnreachable = errors.New("cluster dns unreachable")

// Resolver resolves <service>.<namespace> to its address(es) via in-cluster DNS. Injectable so every
// fail-closed branch of classify is unit-tested without a live CoreDNS. Contract: err==ErrDNSUnreachable =>
// server unreachable; (nil, nil) => NXDOMAIN; ([]addr, nil) => the A records.
type Resolver interface {
	Resolve(ctx context.Context, namespace, service string) ([]netip.Addr, error)
}

// resolvedVIP is a VIP the agent WILL DNAT — produced ONLY by classify's single success branch.
type resolvedVIP struct {
	vip       string
	clusterIP string
}

// refusedVIP is a mapping that did NOT program a DNAT, with the reason (surfaced for the operator).
type refusedVIP struct {
	vip, namespace, service, reason string
}

// classify is THE decision — one function, one success. ok=true is reachable at EXACTLY ONE return: a
// resolution of EXACTLY ONE address that is INSIDE the registered Service CIDR (a ClusterIP). Every other
// outcome fails closed (ok=false, no DNAT): a resolver error, DNS unreachable, NXDOMAIN, more than one
// address (headless/N pods), an address outside the Service CIDR (a pod IP = headless), or a missing/bad
// Service CIDR. Structured so a future edit cannot reorder a guard into a fail-open — the success is the
// last line, gated by everything above it.
func classify(m nodepolicy.VIPMapping, ips []netip.Addr, resolveErr error) (clusterIP string, ok bool, reason string) {
	// Defense-in-depth (review fold): the VIP is a raw string from the CP; NEVER interpolate an
	// unvalidated value into the nft ruleset (the wgIface/ruleID regex-guard convention). A malformed VIP
	// fails CLOSED here, before any render can see it — the classifier is the one place inputs are trusted.
	if _, e := netip.ParseAddr(m.VIP); e != nil {
		return "", false, "invalid_vip"
	}
	switch {
	case errors.Is(resolveErr, ErrDNSUnreachable):
		return "", false, "dns_unreachable"
	case resolveErr != nil:
		return "", false, "resolve_error"
	case len(ips) == 0:
		return "", false, "nxdomain" // Service absent (deleted in-cluster, or DNS not yet propagated)
	case len(ips) > 1:
		return "", false, "headless_multi" // N pod IPs — a headless Service has no stable VIP to map
	}
	svcCIDR, err := netip.ParsePrefix(m.ServiceCIDR)
	if err != nil {
		return "", false, "no_service_cidr" // without the classifier input we cannot tell ClusterIP from pod IP
	}
	if !svcCIDR.Contains(ips[0]) {
		return "", false, "headless_pod_ip" // a single address OUTSIDE the Service CIDR = a pod IP = headless
	}
	// THE ONLY SUCCESS: exactly one address, inside the registered Service CIDR = a ClusterIP. Program it.
	return ips[0].String(), true, ""
}

// ResolveK8sVIPs resolves every VIP mapping in the current policy and stores the resolved map + refusals +
// the DNS-preflight flag. DECOUPLED from Reconcile/apply (its own loop, main.go) so a slow resolver never
// stalls an nft apply; the render (preroutingDNAT) reads whatever this last stored.
func (m *Manager) ResolveK8sVIPs(ctx context.Context) {
	p := m.policy.Load()
	nVIP, nDNS := 0, 0
	if p != nil {
		nVIP, nDNS = len(p.VIPMappings), len(p.K8sDNSZones)
	}
	// WF-K-OBS-1: log what the agent received + resolved. A silent refusal (no DNAT, no log) is un-debuggable;
	// the DNS-unreachable health kind only fires when ALL VIPs fail to REACH the server, so per-VIP outcomes
	// (headless / outside-CIDR / NXDOMAIN) were previously invisible.
	if m.log != nil {
		m.log.Info("k8s_resolve_begin", "vip_mappings", nVIP, "dns_zones", nDNS)
	}
	if p == nil || len(p.VIPMappings) == 0 {
		m.resolvedVIPs.Store(&[]resolvedVIP{})
		m.refusedK8sVIPs.Store(&[]refusedVIP{})
		m.dnsUnreachable.Store(false)
		return
	}
	var resolved []resolvedVIP
	var refused []refusedVIP
	reachable, unreachable := 0, 0
	for _, vm := range p.VIPMappings {
		cctx, cancel := context.WithTimeout(ctx, resolveTimeout)
		ips, err := m.resolver.Resolve(cctx, vm.Namespace, vm.Service)
		cancel()
		cip, ok, reason := classify(vm, ips, err)
		if reason == "dns_unreachable" {
			unreachable++
		} else {
			reachable++
		}
		if ok {
			resolved = append(resolved, resolvedVIP{vip: vm.VIP, clusterIP: cip})
			if m.log != nil {
				m.log.Info("k8s_vip_resolved", "vip", vm.VIP, "service", vm.Namespace+"/"+vm.Service, "cluster_ip", cip)
			}
		} else {
			refused = append(refused, refusedVIP{vip: vm.VIP, namespace: vm.Namespace, service: vm.Service, reason: reason})
			if m.log != nil {
				m.log.Warn("k8s_vip_refused", "vip", vm.VIP, "service", vm.Namespace+"/"+vm.Service, "service_cidr", vm.ServiceCIDR, "reason", reason)
			}
		}
	}
	// Byte-stable order → a steady-state reconcile is a no-op (no thrash).
	sort.Slice(resolved, func(i, j int) bool { return resolved[i].vip < resolved[j].vip })
	// Preflight: the DNS SERVER is unreachable only if EVERY lookup failed to reach it (a single NXDOMAIN
	// means the server is up). Fail-closed + surfaced loud (k8s_cluster_dns_unreachable), never a silent no-map.
	m.dnsUnreachable.Store(unreachable > 0 && reachable == 0)
	m.resolvedVIPs.Store(&resolved)
	m.refusedK8sVIPs.Store(&refused)
}

// runIP is the real `ip` runner (a single command, discarded output — errors carry the exit status).
func runIP(ctx context.Context, args ...string) error {
	return exec.CommandContext(ctx, "ip", args...).Run()
}

// ReconcileDNSVIPs drives the wg interface's assigned DNS VIPs to match the policy's K8sDNSZones (S10.3 A1).
// Each cluster's reserved DNS VIP must be OWNED locally as a /32 so (a) a client's DNS query to it is
// delivered locally (not forwarded) and (b) the dnsforward bind-reconcile binds :53 on it (it enumerates
// the wg interface's addresses). Idempotent: `ip addr replace` adds/refreshes; a VIP that left the policy is
// `ip addr del`'d. FAIL-CLOSED by construction — if an assign fails (no CAP_NET_ADMIN / netlink fault) the
// address never becomes local, the forwarder never binds :53 on it, and the gateway answers NOTHING there
// (a departed-half-bind is impossible). Decoupled from the nft apply (its own step in the egress loop).
func (m *Manager) ReconcileDNSVIPs(ctx context.Context) error {
	if !ifaceRE.MatchString(m.wgIface) {
		return fmt.Errorf("invalid wg interface name %q", m.wgIface) // never interpolate an unvalidated name into a privileged command
	}
	p := m.policy.Load()
	want := map[string]struct{}{}
	if p != nil {
		for _, z := range p.K8sDNSZones {
			// Validate the CP-supplied VIP before it reaches `ip` — the same never-interpolate-an-unvalidated-
			// CP-string discipline the DNAT classifier applies (Slice 4b fold).
			if a, err := netip.ParseAddr(z.ListenVIP); err == nil && a.Is4() {
				want[a.String()] = struct{}{}
			}
		}
	}
	var prev []string
	if pv := m.dnsVIPs.Load(); pv != nil {
		prev = *pv
	}
	var errs []error
	// Remove VIPs no longer wanted (a deregistered/emptied cluster) — no stale local address, no stale :53 bind.
	for _, v := range prev {
		if _, ok := want[v]; !ok {
			if err := m.runIP(ctx, "addr", "del", v+"/32", "dev", m.wgIface); err != nil {
				errs = append(errs, fmt.Errorf("unassign %s: %w", v, err))
			}
		}
	}
	// Add/refresh wanted VIPs (idempotent replace). FAIL-CLOSED: a VIP whose assign fails is NOT recorded as
	// applied, so a later reconcile retries; the address stays non-local, the forwarder never binds :53 on it,
	// and the gateway answers nothing there — never a half-owned VIP.
	applied := make([]string, 0, len(want))
	for v := range want {
		if err := m.runIP(ctx, "addr", "replace", v+"/32", "dev", m.wgIface); err != nil {
			errs = append(errs, fmt.Errorf("assign %s: %w", v, err))
			continue
		}
		applied = append(applied, v)
	}
	sort.Strings(applied)
	m.dnsVIPs.Store(&applied)
	return errors.Join(errs...)
}

// preroutingDNAT renders the VIP->ClusterIP DNAT chain from the LAST-RESOLVED map (PURE — no I/O). Priority
// -101 (below dstnat's -100) so our chain runs BEFORE kube-proxy's ClusterIP DNAT in the shared node netns
// (hostNetwork): VIP->ClusterIP here, then kube-proxy ClusterIP->pod. It lives in OUR `table ip tunnex`
// (atomic add;flush;table replace) — a removed Service's rule is swept for free, never interleaved into
// kube-proxy's chains. Empty (no chain at all) for a non-cluster gateway — the zero-config golden.
func (m *Manager) preroutingDNAT() string {
	rs := m.resolvedVIPs.Load()
	if rs == nil || len(*rs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("  chain prerouting {\n")
	b.WriteString("    type nat hook prerouting priority -101; policy accept;\n") // -101 < dstnat(-100): before kube-proxy
	for _, r := range *rs {
		fmt.Fprintf(&b, "    ip daddr %s dnat to %s comment \"tunnex_k8s_vip\"\n", r.vip, r.clusterIP)
	}
	b.WriteString("  }\n")
	return b.String()
}

// DNSUnreachable reports the k8s_cluster_dns_unreachable preflight (every exposed-Service lookup failed to
// reach the cluster DNS server). Reported to the CP so an operator sees WHY no Service is reachable.
func (m *Manager) DNSUnreachable() bool { return m.dnsUnreachable.Load() }

// clusterDNSResolver is the real Resolver — an in-cluster CoreDNS lookup of the standard Service FQDN. Uses
// the pod's resolv.conf (the chart sets dnsPolicy: ClusterFirstWithHostNet so a hostNetwork pod still points
// at cluster DNS). Any non-NotFound error is treated as unreachable (fail-closed, conservative).
type clusterDNSResolver struct{}

func (clusterDNSResolver) Resolve(ctx context.Context, namespace, service string) ([]netip.Addr, error) {
	fqdn := service + "." + namespace + ".svc.cluster.local"
	addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip4", fqdn)
	if err != nil {
		var de *net.DNSError
		if errors.As(err, &de) && de.IsNotFound {
			return nil, nil // NXDOMAIN — server up, name absent (Service deleted / not yet created)
		}
		return nil, ErrDNSUnreachable // timeout / connection refused / no server → unreachable (fail-closed)
	}
	return addrs, nil
}
