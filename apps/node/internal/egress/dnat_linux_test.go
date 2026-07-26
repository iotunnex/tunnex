//go:build linux

package egress

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"

	"github.com/tunnexio/tunnex/apps/node/internal/nodepolicy"
)

func a(s string) netip.Addr { return netip.MustParseAddr(s) }

type resolverResult struct {
	ips []netip.Addr
	err error
}

type fakeResolver struct{ result map[string]resolverResult }

func (f *fakeResolver) Resolve(_ context.Context, ns, svc string) ([]netip.Addr, error) {
	r := f.result[ns+"/"+svc]
	return r.ips, r.err
}

func mgrWithVIPs(t *testing.T, mappings []nodepolicy.VIPMapping, res Resolver) *Manager {
	t.Helper()
	m := New("wg0")
	m.resolver = res
	m.SetPolicy(&nodepolicy.Compiled{Version: 7, Mode: "enforcing", VIPMappings: mappings})
	m.ResolveK8sVIPs(context.Background())
	return m
}

// classify is FAIL-CLOSED at every branch: the ONLY input that programs a DNAT is exactly one address
// INSIDE the registered Service CIDR. Every other outcome refuses. Both headless detectors covered
// (multi-IP + single-pod-IP-outside-CIDR).
func TestClassifyFailsClosedExceptOneInsideCIDR(t *testing.T) {
	m := nodepolicy.VIPMapping{VIP: "100.64.0.5", Namespace: "prod", Service: "api", ServiceCIDR: "10.96.0.0/12"}
	cases := []struct {
		name       string
		ips        []netip.Addr
		err        error
		wantOK     bool
		wantReason string
	}{
		{"dns unreachable", nil, ErrDNSUnreachable, false, "dns_unreachable"},
		{"resolve error", nil, errors.New("boom"), false, "resolve_error"},
		{"nxdomain", nil, nil, false, "nxdomain"},
		{"headless multi-pod", []netip.Addr{a("10.244.0.1"), a("10.244.0.2")}, nil, false, "headless_multi"},
		{"single pod ip outside service cidr", []netip.Addr{a("10.244.0.7")}, nil, false, "headless_pod_ip"},
		{"clusterip inside service cidr", []netip.Addr{a("10.96.1.5")}, nil, true, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cip, ok, reason := classify(m, c.ips, c.err)
			if ok != c.wantOK || reason != c.wantReason {
				t.Fatalf("classify=%v/%q, want %v/%q", ok, reason, c.wantOK, c.wantReason)
			}
			if ok && cip != "10.96.1.5" {
				t.Fatalf("success must return the ClusterIP, got %q", cip)
			}
		})
	}
	// A missing/bad Service CIDR fails closed (can't tell ClusterIP from pod IP).
	bad := nodepolicy.VIPMapping{VIP: "100.64.0.5", ServiceCIDR: "not-a-cidr"}
	if _, ok, r := classify(bad, []netip.Addr{a("10.96.1.5")}, nil); ok || r != "no_service_cidr" {
		t.Fatalf("a bad Service CIDR must fail closed, got ok=%v reason=%q", ok, r)
	}
	// A malformed VIP from the CP fails closed — never interpolated into nft (review fold), even with a
	// valid ClusterIP resolution.
	badVIP := nodepolicy.VIPMapping{VIP: "100.64.0.5; drop", Namespace: "p", Service: "s", ServiceCIDR: "10.96.0.0/12"}
	if _, ok, r := classify(badVIP, []netip.Addr{a("10.96.1.5")}, nil); ok || r != "invalid_vip" {
		t.Fatalf("a malformed VIP must fail closed, got ok=%v reason=%q", ok, r)
	}
}

// The VIP->ClusterIP DNAT renders in OUR prerouting chain at priority -101 (before kube-proxy); a removed
// Service's rule is GONE after one re-resolve with NO delete logic (the atomic table replace); and our
// ruleset never references kube-proxy's chains (resync-inert).
func TestVIPDNATRenderAndAtomicSweep(t *testing.T) {
	vm := nodepolicy.VIPMapping{VIP: "100.64.0.5", Namespace: "prod", Service: "api", ServiceCIDR: "10.96.0.0/12"}
	res := &fakeResolver{result: map[string]resolverResult{"prod/api": {ips: []netip.Addr{a("10.96.1.5")}}}}
	m := mgrWithVIPs(t, []nodepolicy.VIPMapping{vm}, res)

	rs := m.ruleset("10.99.0.1/24")
	if !strings.Contains(rs, "chain prerouting") || !strings.Contains(rs, "priority -101") {
		t.Fatalf("prerouting DNAT chain missing / wrong priority:\n%s", rs)
	}
	if !strings.Contains(rs, "ip daddr 100.64.0.5 dnat to 10.96.1.5") {
		t.Fatalf("VIP->ClusterIP DNAT rule missing:\n%s", rs)
	}
	if strings.Contains(rs, "KUBE-") {
		t.Fatal("our ruleset must never reference kube-proxy chains (resync-inert)")
	}

	// Remove the Service -> re-resolve -> the DNAT is GONE, with NO explicit delete logic (atomic replace).
	m.SetPolicy(&nodepolicy.Compiled{Version: 7, Mode: "enforcing"})
	m.ResolveK8sVIPs(context.Background())
	if rs2 := m.ruleset("10.99.0.1/24"); strings.Contains(rs2, "chain prerouting") || strings.Contains(rs2, "tunnex_k8s_vip") {
		t.Fatalf("a removed Service's DNAT must be swept by the atomic replace (no delete logic):\n%s", rs2)
	}
}

// Steady-state re-resolve is idempotent (byte-stable); a ClusterIP change re-programs within one tick.
func TestVIPDNATIdempotentAndReprogramsOnClusterIPChange(t *testing.T) {
	vm := nodepolicy.VIPMapping{VIP: "100.64.0.5", Namespace: "prod", Service: "api", ServiceCIDR: "10.96.0.0/12"}
	res := &fakeResolver{result: map[string]resolverResult{"prod/api": {ips: []netip.Addr{a("10.96.1.5")}}}}
	m := mgrWithVIPs(t, []nodepolicy.VIPMapping{vm}, res)

	first := m.ruleset("10.99.0.1/24")
	m.ResolveK8sVIPs(context.Background())
	if m.ruleset("10.99.0.1/24") != first {
		t.Fatal("a steady-state re-resolve must be idempotent (byte-stable ruleset)")
	}
	// ClusterIP changes (delete+recreate) -> re-resolve -> DNAT re-programmed to the new address.
	res.result["prod/api"] = resolverResult{ips: []netip.Addr{a("10.96.9.9")}}
	m.ResolveK8sVIPs(context.Background())
	rs := m.ruleset("10.99.0.1/24")
	if strings.Contains(rs, "dnat to 10.96.1.5") || !strings.Contains(rs, "dnat to 10.96.9.9") {
		t.Fatalf("a ClusterIP change must re-program the DNAT within one tick:\n%s", rs)
	}
}

// A non-cluster gateway renders NO prerouting chain at all (the zero-config golden).
func TestNonClusterGatewayNoDNATChain(t *testing.T) {
	m := mgrWithVIPs(t, nil, &fakeResolver{})
	if strings.Contains(m.ruleset("10.99.0.1/24"), "chain prerouting") {
		t.Fatal("a non-cluster gateway must render NO prerouting chain")
	}
}

// The preflight: DNS SERVER unreachable (every lookup) -> k8s_cluster_dns_unreachable + no DNAT (fail-closed);
// a reachable server returning NXDOMAIN is NOT unreachable.
func TestDNSUnreachablePreflight(t *testing.T) {
	vm := nodepolicy.VIPMapping{VIP: "100.64.0.5", Namespace: "prod", Service: "api", ServiceCIDR: "10.96.0.0/12"}

	down := &fakeResolver{result: map[string]resolverResult{"prod/api": {err: ErrDNSUnreachable}}}
	m := mgrWithVIPs(t, []nodepolicy.VIPMapping{vm}, down)
	if !m.DNSUnreachable() {
		t.Fatal("all-unreachable lookups must set the k8s_cluster_dns_unreachable preflight")
	}
	if strings.Contains(m.ruleset("10.99.0.1/24"), "chain prerouting") {
		t.Fatal("DNS unreachable must program NO DNAT (fail-closed)")
	}

	up := &fakeResolver{result: map[string]resolverResult{"prod/api": {ips: nil, err: nil}}} // NXDOMAIN
	if mgrWithVIPs(t, []nodepolicy.VIPMapping{vm}, up).DNSUnreachable() {
		t.Fatal("NXDOMAIN means the DNS server IS reachable -> preflight must be false")
	}
}

// TestReconcileDNSVIPsAssignsAndSweeps — S10.3 A1: each cluster's reserved DNS VIP is assigned as a /32 on
// wg0 (so the client's query is delivered locally and the forwarder binds :53 on it), and a VIP that leaves
// the policy is removed. Fail-closed: an assign that errors is NOT recorded (retried next tick), and an
// invalid CP-supplied VIP never reaches `ip`.
func TestReconcileDNSVIPsAssignsAndSweeps(t *testing.T) {
	m := New("wg0")
	var cmds []string
	fail := map[string]bool{}
	m.runIP = func(_ context.Context, args ...string) error {
		key := strings.Join(args, " ")
		cmds = append(cmds, key)
		if fail[key] {
			return errors.New("RTNETLINK: operation not permitted")
		}
		return nil
	}

	// Two clusters, one with a bogus VIP that must never reach `ip`.
	m.SetPolicy(&nodepolicy.Compiled{Version: 7, K8sDNSZones: []nodepolicy.K8sDNSZone{
		{ListenVIP: "100.64.0.2", Zone: "prod.k8s.acme.com"},
		{ListenVIP: "not-an-ip", Zone: "bad.k8s.acme.com"},
	}})
	if err := m.ReconcileDNSVIPs(context.Background()); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	joined := strings.Join(cmds, "\n")
	if !strings.Contains(joined, "addr replace 100.64.0.2/32 dev wg0") {
		t.Fatalf("the valid DNS VIP must be assigned, got:\n%s", joined)
	}
	if strings.Contains(joined, "not-an-ip") {
		t.Fatalf("an invalid CP-supplied VIP must NEVER reach ip, got:\n%s", joined)
	}

	// The cluster leaves the policy → its DNS VIP is removed (no stale local address / :53 bind).
	cmds = nil
	m.SetPolicy(&nodepolicy.Compiled{Version: 7})
	if err := m.ReconcileDNSVIPs(context.Background()); err != nil {
		t.Fatalf("sweep reconcile: %v", err)
	}
	if !strings.Contains(strings.Join(cmds, "\n"), "addr del 100.64.0.2/32 dev wg0") {
		t.Fatalf("a departed DNS VIP must be unassigned, got:\n%s", strings.Join(cmds, "\n"))
	}

	// Fail-closed: an assign that errors is not recorded as applied → the NEXT reconcile retries it.
	cmds = nil
	fail["addr replace 100.64.0.2/32 dev wg0"] = true
	m.SetPolicy(&nodepolicy.Compiled{Version: 7, K8sDNSZones: []nodepolicy.K8sDNSZone{{ListenVIP: "100.64.0.2", Zone: "prod.k8s.acme.com"}}})
	if err := m.ReconcileDNSVIPs(context.Background()); err == nil {
		t.Fatal("an assign failure must surface an error")
	}
	cmds = nil
	fail["addr replace 100.64.0.2/32 dev wg0"] = false
	_ = m.ReconcileDNSVIPs(context.Background())
	if !strings.Contains(strings.Join(cmds, "\n"), "addr replace 100.64.0.2/32 dev wg0") {
		t.Fatalf("a previously-FAILED assign must be retried (never recorded as applied), got:\n%s", strings.Join(cmds, "\n"))
	}
}
