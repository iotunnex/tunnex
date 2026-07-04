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
	mu     sync.Mutex
	peers  []Peer
	applyN int
}

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
func (c *fakeClient) Watch(ctx context.Context) error {
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

func TestReconcileAppliesAndIdempotent(t *testing.T) {
	b := &fakeBackend{}
	r := New(b, discard())
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
	r := New(b, discard())
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
	r := New(b, discard())
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
	r := New(b, discard())
	// watch never fires -> only the interval (safety net) can converge.
	c := &fakeClient{watch: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go r.Run(ctx, c, 20*time.Millisecond, 10*time.Millisecond)

	c.set(DesiredState{Peers: []Peer{p1}})
	waitFor(t, 3*time.Second, func() bool { return b.count() == 1 })
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
