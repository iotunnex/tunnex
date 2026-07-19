package dnsforward

import (
	"context"
	"errors"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// fakeListener is an injectable udpListener for the F1 bind-reconcile red — no real sockets. ReadFrom
// blocks until Close (so serveConn's reader exits cleanly when its listener is cancelled).
type fakeListener struct {
	closed atomic.Bool
	done   chan struct{}
}

func newFakeListener() *fakeListener { return &fakeListener{done: make(chan struct{})} }
func (l *fakeListener) ReadFromUDPAddrPort([]byte) (int, netip.AddrPort, error) {
	<-l.done
	return 0, netip.AddrPort{}, errors.New("closed")
}
func (l *fakeListener) WriteToUDPAddrPort(b []byte, _ netip.AddrPort) (int, error) {
	return len(b), nil
}
func (l *fakeListener) Close() error {
	if l.closed.CompareAndSwap(false, true) {
		close(l.done)
	}
	return nil
}

// TestServeBindReconcileLifecycle (F1) — the forwarder binds when wg0 APPEARS after start (not at boot,
// where wg0 doesn't exist yet), re-binds after an address flap, and closes listeners when the interface
// goes. Drives reconcileBinds directly across interface states with injected seams.
func TestServeBindReconcileLifecycle(t *testing.T) {
	f := New(nil, func(netip.Addr, []byte) ([]byte, error) { return nil, nil })
	var mu sync.Mutex
	var addrs []netip.Addr
	setAddrs := func(a ...netip.Addr) { mu.Lock(); addrs = a; mu.Unlock() }
	ifaceUp := false // false = wg0 absent (InterfaceByName errors); true = wg0 present (addrs may be empty)
	src := func(string) ([]netip.Addr, error) {
		mu.Lock()
		defer mu.Unlock()
		if !ifaceUp {
			return nil, errors.New("no such iface") // wg0 absent (boot) → the F1 topology
		}
		return append([]netip.Addr(nil), addrs...), nil // present: a SUCCESSFUL read (possibly empty)
	}
	opened := map[netip.Addr]*fakeListener{}
	var olMu sync.Mutex
	lst := func(a netip.Addr) (udpListener, error) {
		l := newFakeListener()
		olMu.Lock()
		opened[a] = l
		olMu.Unlock()
		return l, nil
	}
	waitClosed := func(a netip.Addr) {
		for i := 0; i < 200; i++ {
			olMu.Lock()
			l := opened[a]
			olMu.Unlock()
			if l != nil && l.closed.Load() {
				return
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatalf("listener for %v never closed", a)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	live := map[netip.Addr]context.CancelFunc{}
	a1 := netip.MustParseAddr("10.99.0.2")
	a2 := netip.MustParseAddr("10.99.0.7")

	// 1) wg0 absent at boot → NO bind (the F1 bug was a bind-once here that died forever).
	f.reconcileBinds(ctx, src, lst, "wg0", live)
	if len(live) != 0 {
		t.Fatal("must not bind before wg0 exists")
	}
	// 2) wg0 appears → bind a1.
	mu.Lock()
	ifaceUp = true
	mu.Unlock()
	setAddrs(a1)
	f.reconcileBinds(ctx, src, lst, "wg0", live)
	if _, ok := live[a1]; !ok {
		t.Fatal("must bind once wg0 appears (the F1 fix)")
	}
	// 3) flap a1 → a2: a1 closes, a2 binds.
	setAddrs(a2)
	f.reconcileBinds(ctx, src, lst, "wg0", live)
	if _, ok := live[a1]; ok {
		t.Fatal("stale a1 listener must close on flap")
	}
	if _, ok := live[a2]; !ok {
		t.Fatal("must re-bind to a2 after flap")
	}
	waitClosed(a1)
	// 4) addresses removed (wg0 up but addressless — a SUCCESSFUL empty read) → all listeners close.
	// (A transient InterfaceByName ERROR is the separate R6 case and must NOT close — see that red.)
	setAddrs()
	f.reconcileBinds(ctx, src, lst, "wg0", live)
	if len(live) != 0 {
		t.Fatal("a successful empty address read closes every listener")
	}
	waitClosed(a2)
}

// TestReconcileBindsTransientErrorKeepsListeners (S8.4 fold R6) — a TRANSIENT bindSource error is NOT an
// empty address set: the live listeners must PERSIST across a glitchy tick (error ≠ absence), so a momentary
// interface read failure can't blip cross-site DNS. Only a SUCCESSFUL empty read closes them.
func TestReconcileBindsTransientErrorKeepsListeners(t *testing.T) {
	f := New(nil, func(netip.Addr, []byte) ([]byte, error) { return nil, nil })
	a1 := netip.MustParseAddr("10.99.0.2")
	mode := "ok"
	src := func(string) ([]netip.Addr, error) {
		switch mode {
		case "err":
			return nil, errors.New("transient InterfaceByName glitch")
		case "empty":
			return nil, nil // successful read, no addresses
		default:
			return []netip.Addr{a1}, nil
		}
	}
	lst := func(netip.Addr) (udpListener, error) { return newFakeListener(), nil }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	live := map[netip.Addr]context.CancelFunc{}

	f.reconcileBinds(ctx, src, lst, "wg0", live) // binds a1
	if _, ok := live[a1]; !ok {
		t.Fatal("a1 must bind")
	}
	mode = "err"
	f.reconcileBinds(ctx, src, lst, "wg0", live) // glitch → KEEP a1
	if _, ok := live[a1]; !ok {
		t.Fatal("a transient bindSource error must NOT tear down the listener (error ≠ absence)")
	}
	mode = "empty"
	f.reconcileBinds(ctx, src, lst, "wg0", live) // genuine empty read → close
	if len(live) != 0 {
		t.Fatal("a successful empty address read must close the listeners")
	}
}

// TestBucketEvictionBounded (F7) — idle rate-limit buckets are swept so a source-address flood can't grow
// the map without bound (OOM → tunnel-down). 1000 idle sources collapse to ~1 after a sweep pass.
func TestBucketEvictionBounded(t *testing.T) {
	base := time.Unix(0, 0)
	cur := base
	f := New(nil, func(netip.Addr, []byte) ([]byte, error) { return []byte{0}, nil })
	f.now = func() time.Time { return cur }
	f.SetTable([]Entry{{Domain: "corp.local", ResolverIP: "10.0.0.53"}})
	q := mkQuery("nas.corp.local.")
	for i := 0; i < 1000; i++ {
		f.handle(q, netip.AddrFrom4([4]byte{10, byte(i >> 8), byte(i), 5}))
	}
	f.mu.Lock()
	before := len(f.buckets)
	f.mu.Unlock()
	if before < 900 {
		t.Fatalf("expected ~1000 buckets before eviction, got %d", before)
	}
	// Advance past the idle TTL + a sweep interval; the next query triggers the sweep.
	cur = base.Add(bucketIdleTTL + bucketSweepEvery + time.Second)
	f.handle(q, netip.MustParseAddr("10.200.200.200"))
	f.mu.Lock()
	after := len(f.buckets)
	f.mu.Unlock()
	if after > 1 {
		t.Fatalf("idle buckets must be evicted; map still holds %d", after)
	}
}

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

// TestSingleLabelZoneCompiles (F3) — a single-label zone ("internal") is legitimate and must compile +
// match, matching what the control plane accepts (no normalizer drift between layers).
func TestSingleLabelZoneCompiles(t *testing.T) {
	tbl := buildTable([]Entry{{Domain: "internal", ResolverIP: "10.0.0.53"}}, nil)
	if len(tbl.rules) != 1 {
		t.Fatalf("single-label zone must compile, got %d", len(tbl.rules))
	}
	if _, ok := tbl.match("host.internal"); !ok {
		t.Fatal("host.internal must resolve via the single-label zone")
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
	f := New(nil, func(netip.Addr, []byte) ([]byte, error) {
		t.Fatal("must NOT forward an out-of-scope query")
		return nil, nil
	})
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
