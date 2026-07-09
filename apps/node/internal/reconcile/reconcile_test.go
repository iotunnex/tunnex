package reconcile

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

type fakeBackend struct {
	mu        sync.Mutex
	peers     []Peer
	applyN    int
	configErr error
}

func (f *fakeBackend) setConfigErr(e error) { f.mu.Lock(); f.configErr = e; f.mu.Unlock() }
func (f *fakeBackend) Configure(context.Context, InterfaceConfig) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.configErr
}
func (f *fakeBackend) applyCount() int { f.mu.Lock(); defer f.mu.Unlock(); return f.applyN }
func (f *fakeBackend) Stats(context.Context) ([]PeerStat, error) { return nil, nil }
func (f *fakeBackend) Peers(context.Context) ([]Peer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Peer(nil), f.peers...), nil
}
func (f *fakeBackend) ApplyPeers(_ context.Context, p []Peer) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.peers = append([]Peer(nil), p...)
	f.applyN++
	return nil
}

// roam simulates the real device OBSERVING a peer's endpoint change (a client's
// NAT source port rebinding) — what Peers() would then report back.
func (f *fakeBackend) roam(pubKey, endpoint string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.peers {
		if f.peers[i].PublicKey == pubKey {
			f.peers[i].Endpoint = endpoint
		}
	}
}
func (f *fakeBackend) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.peers)
}

type fakeClient struct {
	mu      sync.Mutex
	desired DesiredState
	fetchErr error
	watch   chan struct{}
}

func (c *fakeClient) set(ds DesiredState) { c.mu.Lock(); c.desired = ds; c.mu.Unlock() }
func (c *fakeClient) setErr(e error)      { c.mu.Lock(); c.fetchErr = e; c.mu.Unlock() }
func (c *fakeClient) FetchDesired(context.Context) (DesiredState, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.desired, c.fetchErr
}
func (c *fakeClient) Watch(ctx context.Context, _ uint64) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.watch:
		return nil
	}
}

func discard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

var p1 = Peer{PublicKey: "k1", AllowedIPs: []string{"10.0.0.1/32"}}
var p2 = Peer{PublicKey: "k2", AllowedIPs: []string{"10.0.0.2/32"}}

// TestReconcileIgnoresRoamedEndpoint is the WS1 regression for the POC-surfaced
// bug: a roaming client's observed endpoint changes every reconcile, and the old
// dirty-check (canon included the endpoint) re-fired ApplyPeers each time — whose
// empty-[Interface] syncconf then wiped the interface key + port. After the fix,
// only stable identity (pubkey + allowed-ips) drives convergence, so consecutive
// reconciles over a roaming peer are byte-stable no-ops.
func TestReconcileIgnoresRoamedEndpoint(t *testing.T) {
	b := &fakeBackend{}
	r := New(b, "priv", "pub", discard())
	ctx := context.Background()
	// A control-plane desired peer carries NO endpoint (clients roam).
	desired := []Peer{{PublicKey: "k1", AllowedIPs: []string{"10.99.0.2/32"}}}

	// Cycle 1 applies.
	if changed, _ := r.Reconcile(ctx, desired); !changed || b.applyCount() != 1 {
		t.Fatalf("cycle 1 should apply once, applyN=%d", b.applyCount())
	}
	// The device now REPORTS the peer with a roamed NAT endpoint.
	b.roam("k1", "203.0.113.9:41000")
	// Cycle 2: only the roamed endpoint differs → MUST be a no-op (no re-apply).
	if changed, _ := r.Reconcile(ctx, desired); changed || b.applyCount() != 1 {
		t.Fatalf("roamed endpoint must NOT re-apply (would wipe key+port): changed=%v applyN=%d", changed, b.applyCount())
	}
	// Cycle 3: endpoint roams again → still a stable no-op.
	b.roam("k1", "203.0.113.9:55555")
	if changed, _ := r.Reconcile(ctx, desired); changed || b.applyCount() != 1 {
		t.Fatalf("further roam must stay a no-op: applyN=%d", b.applyCount())
	}
}

func TestReconcileAppliesAndIdempotent(t *testing.T) {
	b := &fakeBackend{}
	r := New(b, "priv", "pub", discard())
	ctx := context.Background()

	if changed, _ := r.Reconcile(ctx, []Peer{p1}); !changed || b.count() != 1 {
		t.Fatal("first reconcile should apply p1")
	}
	if changed, _ := r.Reconcile(ctx, []Peer{p1}); changed {
		t.Fatal("identical desired must be a no-op (idempotent)")
	}
	// Revocation: empty desired removes the peer.
	if changed, _ := r.Reconcile(ctx, []Peer{}); !changed || b.count() != 0 {
		t.Fatal("empty desired should remove peers")
	}
}

func TestRunOnceDataPlaneIndependence(t *testing.T) {
	b := &fakeBackend{peers: []Peer{p1}}
	r := New(b, "priv", "pub", discard())
	c := &fakeClient{watch: make(chan struct{})}
	c.setErr(errors.New("control plane down"))

	// Control-plane fetch fails -> backend is NOT flushed.
	if _, err := r.runOnce(context.Background(), c); err == nil {
		t.Fatal("expected fetch error")
	}
	if b.count() != 1 {
		t.Fatal("data-plane independence violated: peers flushed on control-plane outage")
	}
	// Recovery -> full resync applies the current desired state.
	c.setErr(nil)
	c.set(DesiredState{Peers: []Peer{p1, p2}})
	if _, err := r.runOnce(context.Background(), c); err != nil {
		t.Fatalf("recovery reconcile: %v", err)
	}
	if b.count() != 2 {
		t.Fatal("resync did not converge after recovery")
	}
}

func TestRunPushConvergesQuickly(t *testing.T) {
	b := &fakeBackend{}
	r := New(b, "priv", "pub", discard())
	c := &fakeClient{watch: make(chan struct{}, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Long interval so only the PUSH path can converge within the window.
	go r.Run(ctx, c, time.Hour, 10*time.Millisecond)

	c.set(DesiredState{Peers: []Peer{p1}})
	start := time.Now()
	c.watch <- struct{}{} // push signal

	waitFor(t, 5*time.Second, func() bool { return b.count() == 1 })
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("push convergence took %v (> 5s)", elapsed)
	}
}

func TestRunIntervalSafetyNet(t *testing.T) {
	b := &fakeBackend{}
	r := New(b, "priv", "pub", discard())
	// watch never fires -> only the interval (safety net) can converge.
	c := &fakeClient{watch: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go r.Run(ctx, c, 20*time.Millisecond, 10*time.Millisecond)

	c.set(DesiredState{Peers: []Peer{p1}})
	waitFor(t, 3*time.Second, func() bool { return b.count() == 1 })
}

// TestDirtyDeviceConvergence proves watch-item (c) at the reconcile layer: a
// device carrying a STALE peer plus a still-correct peer converges to exactly
// the desired set (stale removed, correct kept, new added) and a repeat run is a
// no-op — no re-apply, so unchanged peers never flap.
func TestDirtyDeviceConvergence(t *testing.T) {
	stale := Peer{PublicKey: "gone", AllowedIPs: []string{"10.0.0.9/32"}}
	// Device is dirty: it has a stale peer and the still-correct p1.
	b := &fakeBackend{peers: []Peer{stale, p1}}
	r := New(b, "priv", "pub", discard())
	c := &fakeClient{watch: make(chan struct{})}
	c.set(DesiredState{Peers: []Peer{p1, p2}}) // p1 kept, p2 added, stale dropped

	if _, err := r.runOnce(context.Background(), c); err != nil {
		t.Fatalf("runOnce: %v", err)
	}
	if !peersEqual(b.peers, []Peer{p1, p2}) {
		t.Fatalf("did not converge to desired set: %+v", b.peers)
	}
	appliesAfterFirst := b.applyCount()

	// Second run against identical desired state must not re-apply (no flap).
	if changed, err := r.runOnce(context.Background(), c); err != nil || changed {
		t.Fatalf("second run should be a no-op: changed=%v err=%v", changed, err)
	}
	if b.applyCount() != appliesAfterFirst {
		t.Fatal("idempotence violated: re-applied an unchanged peer set")
	}
}

// TestHealthyReflectsBackendFailure proves watch-item (d): a backend that cannot
// configure the device (e.g. NET_ADMIN missing, port bound) leaves the agent
// NOT-ready with a diagnosable error — never a silent success. Recovery flips it
// back to ready.
func TestHealthyReflectsBackendFailure(t *testing.T) {
	b := &fakeBackend{}
	r := New(b, "priv", "pub", discard())
	c := &fakeClient{watch: make(chan struct{})}
	c.set(DesiredState{Peers: []Peer{p1}})

	if r.Healthy() {
		t.Fatal("should not be healthy before any successful reconcile")
	}
	b.setConfigErr(errors.New("operation not permitted (NET_ADMIN?)"))
	if _, err := r.runOnce(context.Background(), c); err == nil {
		t.Fatal("expected a backend configure error")
	}
	if r.Healthy() {
		t.Fatal("backend failure must leave the agent NOT healthy (not-ready)")
	}
	// Recovery.
	b.setConfigErr(nil)
	if _, err := r.runOnce(context.Background(), c); err != nil {
		t.Fatalf("recovery: %v", err)
	}
	if !r.Healthy() {
		t.Fatal("healthy should flip true after a fully successful reconcile")
	}
}

func waitFor(t *testing.T, d time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within deadline")
}
