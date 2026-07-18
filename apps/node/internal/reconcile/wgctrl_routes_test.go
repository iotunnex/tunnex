//go:build linux

package reconcile

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"
)

// TestKeepaliveSyncConfRoundTrip — S8.3 CK: buildSyncConf EMITS PersistentKeepalive for a site-link peer,
// and parseWGDump READS it back off the last dump field (so the actual side carries it → peersEqual
// converges instead of churning). An "off"/absent keepalive parses to 0.
func TestKeepaliveSyncConfRoundTrip(t *testing.T) {
	p := Peer{PublicKey: "hub", AllowedIPs: []string{"10.2.0.0/24"}, Endpoint: "h:51820", SiteLink: true, PersistentKeepalive: 25}
	conf := buildSyncConf("priv", 51820, []Peer{p})
	if !strings.Contains(conf, "PersistentKeepalive = 25") {
		t.Fatalf("buildSyncConf must emit the keepalive, got:\n%s", conf)
	}
	got := parseWGDump("if\tpub\n" + "hub\t(none)\th:51820\t10.2.0.0/24\t0\t0\t0\t25\n")
	if len(got) != 1 || got[0].PersistentKeepalive != 25 {
		t.Fatalf("parseWGDump must read keepalive from the last field, got %+v", got)
	}
	off := parseWGDump("if\tpub\n" + "dev\t(none)\t1.2.3.4:5\t10.99.0.5/32\t0\t0\t0\toff\n")
	if len(off) != 1 || off[0].PersistentKeepalive != 0 {
		t.Fatalf("an 'off' keepalive must parse to 0, got %+v", off)
	}
}

// TestApplyRoutesV4EnumErrorSurfaces — S8.2 F3 (terminal): a -4 route-enumeration error ALWAYS surfaces
// (full-sweep), INCLUDING when there are no desired routes — the just-UNBOUND gateway, where the prune is
// owed. A -6 error is tolerated (v6-disabled host).
func TestApplyRoutesV4EnumErrorSurfaces(t *testing.T) {
	ctx := context.Background()
	fail := func(family string) func(context.Context, string, ...string) (string, error) {
		return func(_ context.Context, _ string, args ...string) (string, error) {
			if len(args) >= 2 && args[0] == family && args[1] == "route" { // the `ip <fam> route show` call
				return "", errors.New("route show failed")
			}
			return "", nil
		}
	}
	// Unbound gateway (cidrs empty) + a -4 show failure → MUST surface (the sweep is owed).
	noAddrs := func() []netip.Addr { return nil }
	b4 := &wgctrlBackend{iface: "wg0", runFn: fail("-4"), addrsFn: noAddrs}
	if err := b4.ApplyRoutes(ctx, nil, nil); err == nil {
		t.Fatal("F3: a -4 enum error must surface even with no desired routes (unbound gateway owes the prune)")
	}
	// A -6 show failure → tolerated.
	b6 := &wgctrlBackend{iface: "wg0", runFn: fail("-6"), addrsFn: noAddrs}
	if err := b6.ApplyRoutes(ctx, nil, nil); err != nil {
		t.Fatalf("a -6 enum error must be tolerated: %v", err)
	}
}

// TestSiteRouteSrcHint — S8.2c D2: ApplyRoutes sources its site routes from the host address inside an
// approved LOCAL subnet (the CP's authoritative answer), so gateway-host-originated site traffic sources
// from the site LAN (not the overlay). Reds: (1) the src is applied to the route + SURVIVES RECONCILE
// (re-derived+re-applied every call — the manual-strip clobber can't win); (2) the no-site edge (empty
// localSubnets) programs the route WITHOUT a src; (3) the no-matching-address edge (advertised a subnet
// the host isn't on) programs without a src (D3 territory) — never a wrong/guessed src.
func TestSiteRouteSrcHint(t *testing.T) {
	ctx := context.Background()
	var gotArgs [][]string
	rec := func(_ context.Context, _ string, args ...string) (string, error) {
		if len(args) >= 2 && args[0] == "route" && args[1] == "replace" {
			gotArgs = append(gotArgs, append([]string(nil), args...))
		}
		return "", nil // route show returns empty (no prune)
	}
	siteHost := netip.MustParseAddr("172.31.24.206")   // inside the local site subnet
	overlay := netip.MustParseAddr("10.99.0.1")        // wg0 overlay — must NOT be chosen
	addrs := func() []netip.Addr { return []netip.Addr{overlay, siteHost} }

	// (1) match → src applied.
	gotArgs = nil
	b := &wgctrlBackend{iface: "wg0", runFn: rec, addrsFn: addrs}
	if err := b.ApplyRoutes(ctx, []string{"10.0.0.0/24"}, []string{"172.31.0.0/16"}); err != nil {
		t.Fatal(err)
	}
	if len(gotArgs) != 1 || !hasPair(gotArgs[0], "src", "172.31.24.206") {
		t.Fatalf("D2: the site route must carry src=172.31.24.206 (the local-subnet host addr); got %v", gotArgs)
	}
	// survives reconcile: a second call re-applies the same src (there is no persisted state to clobber).
	gotArgs = nil
	_ = b.ApplyRoutes(ctx, []string{"10.0.0.0/24"}, []string{"172.31.0.0/16"})
	if len(gotArgs) != 1 || !hasPair(gotArgs[0], "src", "172.31.24.206") {
		t.Fatalf("D2: the src-hint must SURVIVE reconcile (re-applied every tick); got %v", gotArgs)
	}
	// (2) no-site edge: empty localSubnets → no src.
	gotArgs = nil
	_ = b.ApplyRoutes(ctx, []string{"10.0.0.0/24"}, nil)
	if len(gotArgs) != 1 || hasArg(gotArgs[0], "src") {
		t.Fatalf("no localSubnets → route programs WITHOUT a src; got %v", gotArgs)
	}
	// (3) no-matching-address edge: advertised a subnet the host isn't on → no src (never a guess).
	gotArgs = nil
	_ = b.ApplyRoutes(ctx, []string{"10.0.0.0/24"}, []string{"10.55.0.0/24"})
	if len(gotArgs) != 1 || hasArg(gotArgs[0], "src") {
		t.Fatalf("no host addr in the advertised subnet → no src (D3 territory), never a guess; got %v", gotArgs)
	}
}

func hasArg(args []string, a string) bool {
	for _, x := range args {
		if x == a {
			return true
		}
	}
	return false
}
func hasPair(args []string, k, v string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == k && args[i+1] == v {
			return true
		}
	}
	return false
}

// TestParseRouteDstNormalizesHost — S8.2 review #3: `ip route show` prints a host route as a BARE address
// (no /32), so a desired "10.1.0.5/32" and the enumerated "10.1.0.5" MUST canonicalize equal — otherwise
// a /32 site route churns install→delete every reconcile tick and blackholes.
func TestParseRouteDstNormalizesHost(t *testing.T) {
	want, ok1 := parseRouteDst("10.1.0.5/32")
	got, ok2 := parseRouteDst("10.1.0.5") // the bare form `ip route show` prints
	if !ok1 || !ok2 || got != want {
		t.Fatalf("a bare host must canonicalize to its /32 (no churn): %v vs %v", got, want)
	}
	// A v6 host normalizes to /128 too (the dual-family prune, review #4).
	w6, _ := parseRouteDst("2001:db8::1/128")
	g6, ok := parseRouteDst("2001:db8::1")
	if !ok || g6 != w6 {
		t.Fatalf("a bare v6 host must canonicalize to /128: %v vs %v", g6, w6)
	}
}

// TestRoutesToPruneCanonicalCompare — the pure prune decision compares canonical prefixes, so a desired
// /32 (enumerated bare) is NOT pruned while a genuinely stale route IS. Stability is the proof (#3).
func TestRoutesToPruneCanonicalCompare(t *testing.T) {
	desired := map[netip.Prefix]bool{}
	p, _ := parseRouteDst("10.1.0.5/32")
	desired[p] = true
	q, _ := parseRouteDst("10.2.0.0/24")
	desired[q] = true
	// As `ip route show` prints: the /32 as a bare host, the /24 as-is, plus a stale route we own.
	del := routesToPrune([]string{"10.1.0.5", "10.2.0.0/24", "10.9.0.0/24"}, desired)
	if len(del) != 1 || del[0].String() != "10.9.0.0/24" {
		t.Fatalf("only the stale route must prune (the /32 must NOT churn): %v", del)
	}
}
