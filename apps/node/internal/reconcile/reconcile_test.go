package reconcile

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tunnexio/tunnex/apps/node/internal/nodepolicy"
)

type fakeBackend struct {
	mu        sync.Mutex
	peers     []Peer
	routes    []string
	stats     []PeerStat
	statsErr  error
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
func (f *fakeBackend) appliedRoutes() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.routes...)
}
func (f *fakeBackend) ApplyRoutes(_ context.Context, cidrs []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.routes = append([]string(nil), cidrs...)
	return nil
}
func (f *fakeBackend) setStat(s PeerStat)  { f.mu.Lock(); f.stats = []PeerStat{s}; f.statsErr = nil; f.mu.Unlock() }
func (f *fakeBackend) setStatsErr(e error) { f.mu.Lock(); f.statsErr = e; f.mu.Unlock() }
func (f *fakeBackend) Stats(context.Context) ([]PeerStat, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.statsErr != nil {
		return nil, f.statsErr
	}
	return append([]PeerStat(nil), f.stats...), nil
}
func (f *fakeBackend) Peers(context.Context) ([]Peer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	// R2 fixture-fidelity: the kernel/`wg show dump` read path CANNOT know SiteLink, so the fake must not
	// either — strip it on read (a test double must not be more capable than the real substrate).
	out := make([]Peer, len(f.peers))
	for i, p := range f.peers {
		p.SiteLink = false
		out[i] = p
	}
	return out, nil
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

// TestRunOnceProgramsAndPrunesSiteRoutes — S8.2 Slice-2 agent red: the reconcile loop programs the
// kernel routes carried in Policy.Routes (explicit intent, NEVER inferred from peer AllowedIPs), and a
// subsequent fetch carrying fewer routes shrinks the delivered set — the full-sweep contract (a site
// unbind drops its route). A nil Policy (mesh) clears routes.
func TestRunOnceProgramsAndPrunesSiteRoutes(t *testing.T) {
	b := &fakeBackend{}
	r := New(b, "priv", "pub", discard())
	ctx := context.Background()
	c := &fakeClient{watch: make(chan struct{})}

	c.set(DesiredState{Policy: &nodepolicy.Compiled{
		Version: 5, Mode: nodepolicy.ModeEnforcing,
		Routes: []nodepolicy.Route{{DstCIDR: "10.2.0.0/24"}, {DstCIDR: "10.3.0.0/24"}},
	}})
	if _, err := r.runOnce(ctx, c); err != nil {
		t.Fatalf("runOnce: %v", err)
	}
	if got := b.appliedRoutes(); len(got) != 2 {
		t.Fatalf("both site routes must be programmed, got %v", got)
	}
	// A site unbind: the next fetch drops 10.3.0.0/24 → the desired set shrinks → prune.
	c.set(DesiredState{Policy: &nodepolicy.Compiled{
		Version: 5, Mode: nodepolicy.ModeEnforcing, Routes: []nodepolicy.Route{{DstCIDR: "10.2.0.0/24"}},
	}})
	if _, err := r.runOnce(ctx, c); err != nil {
		t.Fatalf("runOnce 2: %v", err)
	}
	if got := b.appliedRoutes(); len(got) != 1 || got[0] != "10.2.0.0/24" {
		t.Fatalf("the dropped route must be pruned (full-sweep), got %v", got)
	}
	// nil Policy (mesh) → routes cleared.
	c.set(DesiredState{})
	if _, err := r.runOnce(ctx, c); err != nil {
		t.Fatalf("runOnce 3: %v", err)
	}
	if got := b.appliedRoutes(); len(got) != 0 {
		t.Fatalf("nil policy must clear routes, got %v", got)
	}
}

// TestSiteLinkEndpointChangeIsDirty — S8.2 B4 + R2: the ACTUAL peer set comes from the kernel (wg dump)
// and NEVER carries SiteLink; only the DESIRED (CP) side does. A converged site-link peer must be a
// steady-state no-op (the R2 perpetual-dirty regression would fail here); a hub endpoint change must be
// dirty (re-dial); a device peer's roamed endpoint stays ignored.
func TestSiteLinkEndpointChangeIsDirty(t *testing.T) {
	actualHub := Peer{PublicKey: "hub", AllowedIPs: []string{"10.2.0.0/24"}, Endpoint: "old:51820"} // kernel shape: SiteLink=false
	desiredSame := Peer{PublicKey: "hub", AllowedIPs: []string{"10.2.0.0/24"}, Endpoint: "old:51820", SiteLink: true}
	if !peersEqual([]Peer{actualHub}, []Peer{desiredSame}) {
		t.Fatal("R2: a converged site-link peer (actual has no SiteLink) must be a steady-state NO-OP, not perpetually dirty")
	}
	desiredNew := desiredSame
	desiredNew.Endpoint = "new:51820"
	if peersEqual([]Peer{actualHub}, []Peer{desiredNew}) {
		t.Fatal("B4: a site-link peer endpoint change MUST be dirty (spoke re-dials the hub)")
	}
	actualDev := Peer{PublicKey: "dev", AllowedIPs: []string{"10.99.0.5/32"}, Endpoint: "1.2.3.4:5"}
	desiredDev := Peer{PublicKey: "dev", AllowedIPs: []string{"10.99.0.5/32"}, Endpoint: "6.7.8.9:10"} // device: SiteLink=false
	if !peersEqual([]Peer{actualDev}, []Peer{desiredDev}) {
		t.Fatal("a device peer's roamed endpoint must be IGNORED (no re-apply every reconcile)")
	}
}

// TestSiteLinkStaleComputation — S8.2 H5: the agent flags site_link_down when a SITE-LINK peer's WG
// handshake is stale/absent (and NOT for a healthy one, nor for device peers). Feeds the CP kind.
func TestSiteLinkStaleComputation(t *testing.T) {
	var sink atomic.Bool
	b := &fakeBackend{}
	r := New(b, "priv", "pub", discard())
	r.SetSiteLinkStaleSink(&sink)
	ctx := context.Background()

	// A site-link peer with NO handshake (fake reports no stats) → stale.
	r.updateSiteLinkStale(ctx, []Peer{{PublicKey: "hub", SiteLink: true}})
	if !sink.Load() {
		t.Fatal("a site-link peer with no handshake must be stale")
	}
	// A fresh handshake → not stale.
	b.setStat(PeerStat{PublicKey: "hub", LastHandshake: time.Now().Unix()})
	r.updateSiteLinkStale(ctx, []Peer{{PublicKey: "hub", SiteLink: true}})
	if sink.Load() {
		t.Fatal("a site-link peer with a fresh handshake must NOT be stale")
	}
	// A non-site (device) peer is never considered → not stale even with no handshake.
	r.updateSiteLinkStale(ctx, []Peer{{PublicKey: "dev", SiteLink: false}})
	if sink.Load() {
		t.Fatal("device peers must not drive site-link staleness")
	}
	// R4: a Stats ERROR with site-link peers present → over-report STALE (maybe-dead reads dead).
	b.setStatsErr(errors.New("wg dump failed"))
	r.updateSiteLinkStale(ctx, []Peer{{PublicKey: "hub", SiteLink: true}})
	if !sink.Load() {
		t.Fatal("R4: a Stats error with site-link peers must over-report stale (never false-green)")
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
