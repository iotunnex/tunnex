package dnsforward

import (
	"errors"
	"net/netip"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

func mkQuery(name string) []byte {
	n, err := dnsmessage.NewName(name)
	if err != nil {
		panic(err)
	}
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: 0x1234, RecursionDesired: true})
	_ = b.StartQuestions()
	_ = b.Question(dnsmessage.Question{Name: n, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET})
	out, err := b.Finish()
	if err != nil {
		panic(err)
	}
	return out
}

func rcodeOf(t *testing.T, resp []byte) dnsmessage.RCode {
	t.Helper()
	var p dnsmessage.Parser
	h, err := p.Start(resp)
	if err != nil {
		t.Fatalf("unparseable response: %v", err)
	}
	return h.RCode
}

// TestMatchLongestSuffix — a QNAME resolves to the LONGEST covering zone; unrelated names don't match.
func TestMatchLongestSuffix(t *testing.T) {
	tbl := buildTable([]Entry{
		{Domain: "corp.local", ResolverIP: "10.0.0.53"},
		{Domain: "a.corp.local", ResolverIP: "10.1.0.53"},
	}, nil)
	if r, ok := tbl.match("nas.corp.local"); !ok || r != netip.MustParseAddr("10.0.0.53") {
		t.Fatalf("nas.corp.local → corp.local resolver; got %v ok=%v", r, ok)
	}
	if r, ok := tbl.match("db.a.corp.local"); !ok || r != netip.MustParseAddr("10.1.0.53") {
		t.Fatalf("db.a.corp.local → the more specific a.corp.local resolver; got %v ok=%v", r, ok)
	}
	if _, ok := tbl.match("example.com"); ok {
		t.Fatal("an out-of-zone name must NOT match (split-horizon)")
	}
	// A near-miss must not false-match by bare suffix ("evilcorp.local" is not within "corp.local").
	if _, ok := tbl.match("evilcorp.local"); ok {
		t.Fatal("a label-boundary near-miss must NOT match")
	}
}

// TestBuildTableSkipDegraded — a malformed entry (bad IP / empty / empty-label domain) is SKIPPED; the
// valid ones survive (D2: one typo never blanks every zone).
func TestBuildTableSkipDegraded(t *testing.T) {
	tbl := buildTable([]Entry{
		{Domain: "corp.local", ResolverIP: "10.0.0.53"}, // good
		{Domain: "bad.local", ResolverIP: "not-an-ip"},  // bad IP → skip
		{Domain: "a..b", ResolverIP: "10.0.0.9"},        // empty label → skip
		{Domain: "", ResolverIP: "10.0.0.9"},            // empty domain → skip
	}, nil)
	if len(tbl.rules) != 1 {
		t.Fatalf("only the one valid entry must survive, got %d: %+v", len(tbl.rules), tbl.rules)
	}
	if _, ok := tbl.match("nas.corp.local"); !ok {
		t.Fatal("the surviving good entry must still resolve")
	}
}

// TestHandleServfailFailStatic — a matched domain whose resolver is unreachable → SERVFAIL (never a
// timeout, never a tunnel effect); the last-good table stays in force.
func TestHandleServfailFailStatic(t *testing.T) {
	f := New(nil, func(netip.Addr, []byte) ([]byte, error) { return nil, errors.New("i/o timeout") })
	f.SetTable([]Entry{{Domain: "corp.local", ResolverIP: "10.0.0.53"}})
	resp := f.handle(mkQuery("nas.corp.local."), netip.MustParseAddr("10.99.0.5"))
	if resp == nil || rcodeOf(t, resp) != dnsmessage.RCodeServerFailure {
		t.Fatalf("unreachable resolver → SERVFAIL; got %v", resp)
	}
}

// TestHandleRefusedOutOfScope — an unmatched domain is REFUSED (scoped forwarder; the client's own resolver
// handles everything else — split-horizon).
func TestHandleRefusedOutOfScope(t *testing.T) {
	f := New(nil, func(netip.Addr, []byte) ([]byte, error) { t.Fatal("must NOT forward an out-of-scope query"); return nil, nil })
	f.SetTable([]Entry{{Domain: "corp.local", ResolverIP: "10.0.0.53"}})
	resp := f.handle(mkQuery("www.example.com."), netip.MustParseAddr("10.99.0.5"))
	if resp == nil || rcodeOf(t, resp) != dnsmessage.RCodeRefused {
		t.Fatalf("out-of-scope → REFUSED; got %v", resp)
	}
}

// TestHandleForwardsMatched — a matched query is relayed and the upstream response returned verbatim.
func TestHandleForwardsMatched(t *testing.T) {
	want := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	var got netip.Addr
	f := New(nil, func(r netip.Addr, _ []byte) ([]byte, error) { got = r; return want, nil })
	f.SetTable([]Entry{{Domain: "corp.local", ResolverIP: "10.0.0.53"}})
	resp := f.handle(mkQuery("nas.corp.local."), netip.MustParseAddr("10.99.0.5"))
	if string(resp) != string(want) || got != netip.MustParseAddr("10.0.0.53") {
		t.Fatalf("matched query must relay to the declared resolver + return its bytes; got resp=%v resolver=%v", resp, got)
	}
}

// TestRateLimit — a single source over its burst is dropped (nil, no reply). D2 hygiene.
func TestRateLimit(t *testing.T) {
	now := time.Unix(0, 0)
	f := New(nil, func(netip.Addr, []byte) ([]byte, error) { return []byte{0}, nil })
	f.now = func() time.Time { return now } // frozen clock → no refill
	f.SetTable([]Entry{{Domain: "corp.local", ResolverIP: "10.0.0.53"}})
	src := netip.MustParseAddr("10.99.0.5")
	q := mkQuery("nas.corp.local.")
	served := 0
	for i := 0; i < dnsRateBurst+10; i++ {
		if f.handle(q, src) != nil {
			served++
		}
	}
	if served != dnsRateBurst {
		t.Fatalf("a frozen-clock source may spend exactly its burst (%d), then drop; served %d", dnsRateBurst, served)
	}
}

// TestWgBindScopeNeverWildcard — the bind set is derived from a NAMED interface's addresses ONLY, never a
// wildcard/public bind (D2). Using loopback (a real iface everywhere): its addrs are 127.0.0.1 / ::1, and
// 0.0.0.0 can never appear — proving the forwarder can't become an open resolver.
func TestWgBindScopeNeverWildcard(t *testing.T) {
	addrs, err := wgBindAddrs("lo")
	if err != nil {
		t.Skipf("no loopback iface named 'lo' on this host: %v", err)
	}
	if len(addrs) == 0 {
		t.Fatal("loopback must yield at least one bind address")
	}
	for _, a := range addrs {
		if a.IsUnspecified() {
			t.Fatalf("bind set must NEVER contain a wildcard/unspecified address (open-resolver risk); got %v", a)
		}
		if !a.IsLoopback() {
			t.Fatalf("binds must come from the named iface only; got non-loopback %v from 'lo'", a)
		}
	}
}
