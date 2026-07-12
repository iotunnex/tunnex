// Package reconcile converges the local WireGuard interface toward the
// control-plane desired state. The logic is backend-agnostic (WGBackend) so it
// unit-tests against a fake — only a thin adapter touches real wgctrl.
package reconcile

import (
	"context"
	"log/slog"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/tunnexio/tunnex/apps/node/internal/nodepolicy"
)

// Peer mirrors the control plane's peer shape (JSON contract).
type Peer struct {
	PublicKey  string   `json:"public_key"`
	AllowedIPs []string `json:"allowed_ips"`
	Endpoint   string   `json:"endpoint,omitempty"`
}

// DesiredState is the control-plane response the agent reconciles toward.
type DesiredState struct {
	ProtocolVersion  int    `json:"protocol_version"`
	NodeID           string `json:"node_id"`
	InterfaceAddress string `json:"interface_address"`
	MTU              int    `json:"mtu"`
	ListenPort       int    `json:"listen_port"`
	// Version is the control plane's change-version at fetch time; echoed on the
	// next Watch so a change during the fetch/apply gap is not missed.
	Version uint64 `json:"version"`
	Peers   []Peer `json:"peers"`
	// Policy is the compiled Zero Trust policy (S7.2). ABSENT/NULL => nil => the
	// agent keeps the legacy blanket MESH — so an open-build control plane (which
	// never sends the field) or an older control plane mid-upgrade can never make
	// a newer agent accidentally enforce OR open by omission. This decode default
	// is ASSERTED by TestAbsentPolicyDecodesToMesh; don't change it casually.
	Policy *nodepolicy.Compiled `json:"policy,omitempty"`
}

// InterfaceConfig is the device-level configuration. The PrivateKey is supplied
// by the AGENT (generated locally, never from the control plane). PublicKey is
// the key's public half — the adapter compares it against the device's current
// public key to decide if the private key needs (re)setting, which is robust to
// WireGuard clamping the stored private key (the raw private bytes differ after
// clamping, but the public key does not).
type InterfaceConfig struct {
	PrivateKey string
	PublicKey  string
	ListenPort int
	Address    string
	MTU        int
}

// PeerStat is per-peer live telemetry read from the device: last handshake (unix
// seconds, 0 = never), raw byte gauges (reset on interface restart), and current
// source endpoint. Reported to the control plane for the connection-status view.
type PeerStat struct {
	PublicKey     string `json:"public_key"`
	LastHandshake int64  `json:"last_handshake"`
	RxBytes       int64  `json:"rx_bytes"`
	TxBytes       int64  `json:"tx_bytes"`
	Endpoint      string `json:"endpoint,omitempty"`
}

// WGBackend abstracts the WireGuard data plane. The real adapter wraps wgctrl;
// the fake drives unit tests.
type WGBackend interface {
	// Configure idempotently ensures the interface exists with this key/port/
	// address/MTU (converging a dirty device without flapping correct peers).
	Configure(ctx context.Context, cfg InterfaceConfig) error
	Peers(ctx context.Context) ([]Peer, error)
	ApplyPeers(ctx context.Context, peers []Peer) error
	// Stats reports per-peer live telemetry (handshake/bytes/endpoint).
	Stats(ctx context.Context) ([]PeerStat, error)
}

// ControlClient is the agent's view of the control plane.
type ControlClient interface {
	// FetchDesired returns the full desired state (a full resync, not a diff).
	FetchDesired(ctx context.Context) (DesiredState, error)
	// Watch blocks until the control plane signals a change (push) or returns an
	// error/ctx cancellation. It carries the version from the last fetch so a
	// change during the fetch gap makes Watch return immediately (no lost wakeup).
	Watch(ctx context.Context, since uint64) error
}

// Reconciler converges a backend toward desired state. It holds the node's
// locally-generated interface private key (never sourced from the control plane).
type Reconciler struct {
	backend    WGBackend
	privateKey string
	publicKey  string
	logger     *slog.Logger
	healthy    atomic.Bool
	version    atomic.Uint64 // last desired-state version, echoed on Watch
	// onPolicy (optional) receives the compiled Zero Trust policy from EVERY
	// desired-state fetch — including nil (absent => legacy mesh). main wires it
	// to egress.Manager.SetPolicy + an immediate egress kick, so a pushed policy
	// change reaches the forward chain on the push path (<5s), not the next
	// egress tick.
	onPolicy func(*nodepolicy.Compiled)
}

// OnPolicy registers the policy sink. Call before Run (not synchronized).
func (r *Reconciler) OnPolicy(fn func(*nodepolicy.Compiled)) { r.onPolicy = fn }

// New builds a Reconciler with the node's WireGuard key pair (public key is used
// only for the clamp-safe "is the interface key already set" check).
func New(backend WGBackend, privateKey, publicKey string, logger *slog.Logger) *Reconciler {
	return &Reconciler{backend: backend, privateKey: privateKey, publicKey: publicKey, logger: logger}
}

// Healthy reports whether the last reconcile fully succeeded (control plane
// reachable AND the backend converged). Agent readiness reflects this, so a
// backend failure — NET_ADMIN missing, port bound, device collision — surfaces
// as not-ready and diagnosable, never a silent success or crash-loop.
func (r *Reconciler) Healthy() bool { return r.healthy.Load() }

// Reconcile converges the backend to the desired peer set. It applies the FULL
// set (a resync), so a long-disconnected agent recovers correctly. Returns
// whether anything changed.
func (r *Reconciler) Reconcile(ctx context.Context, desired []Peer) (bool, error) {
	actual, err := r.backend.Peers(ctx)
	if err != nil {
		return false, err
	}
	if peersEqual(actual, desired) {
		return false, nil
	}
	if err := r.backend.ApplyPeers(ctx, desired); err != nil {
		return false, err
	}
	return true, nil
}

// runOnce fetches desired state and reconciles. A fetch error is returned
// WITHOUT touching the backend — data-plane independence: a control-plane outage
// never flushes live peers.
func (r *Reconciler) runOnce(ctx context.Context, client ControlClient) (bool, error) {
	ds, err := client.FetchDesired(ctx)
	if err != nil {
		r.healthy.Store(false)
		return false, err
	}
	r.version.Store(ds.Version) // echoed on the next Watch to close the fetch-gap
	// Deliver the compiled policy BEFORE the WG converge (and regardless of its
	// outcome): enforcement is orthogonal to interface config, and a policy pushed
	// for revocation must not wait on an unrelated backend failure. nil (absent
	// field) is delivered too — it means "legacy mesh" and must be able to unset a
	// previous policy (mode enforcing -> off recovery path).
	if r.onPolicy != nil {
		r.onPolicy(ds.Policy)
	}
	// Idempotently ensure the interface config, then converge peers.
	if err := r.backend.Configure(ctx, InterfaceConfig{
		PrivateKey: r.privateKey, PublicKey: r.publicKey,
		ListenPort: ds.ListenPort, Address: ds.InterfaceAddress, MTU: ds.MTU,
	}); err != nil {
		r.healthy.Store(false)
		return false, err
	}
	changed, err := r.Reconcile(ctx, ds.Peers)
	if err != nil {
		r.healthy.Store(false)
		return false, err
	}
	r.healthy.Store(true)
	return changed, nil
}

// Run drives reconciliation from two independent triggers: Watch (push, low
// latency) and a ticker (safety net that converges even if push is broken). On
// any control-plane error it backs off and leaves the data plane untouched.
func (r *Reconciler) Run(ctx context.Context, client ControlClient, interval, backoff time.Duration) {
	// Initial resync.
	if _, err := r.runOnce(ctx, client); err != nil {
		r.logger.Warn("reconcile_initial_failed", slog.String("error", err.Error()))
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		// Push path: block until the control plane signals a change. Echo the last
		// fetched version so a change during the previous fetch/apply returns now.
		watchCh := make(chan error, 1)
		go func() { watchCh <- client.Watch(ctx, r.version.Load()) }()

		select {
		case <-ctx.Done():
			return
		case err := <-watchCh:
			if err != nil {
				r.logger.Warn("watch_failed_backing_off", slog.String("error", err.Error()))
				if !sleep(ctx, backoff) {
					return
				}
				continue // the ticker keeps converging regardless (safety net)
			}
			if _, err := r.runOnce(ctx, client); err != nil {
				r.logger.Warn("reconcile_after_push_failed", slog.String("error", err.Error()))
			}
		case <-ticker.C:
			if _, err := r.runOnce(ctx, client); err != nil {
				r.logger.Warn("reconcile_interval_failed", slog.String("error", err.Error()))
			}
		}
	}
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func peersEqual(a, b []Peer) bool {
	if len(a) != len(b) {
		return false
	}
	ka, kb := make([]string, len(a)), make([]string, len(b))
	for i := range a {
		ka[i] = canon(a[i])
	}
	for i := range b {
		kb[i] = canon(b[i])
	}
	sort.Strings(ka)
	sort.Strings(kb)
	for i := range ka {
		if ka[i] != kb[i] {
			return false
		}
	}
	return true
}

// canon keys a peer by its STABLE IDENTITY (public key + allowed-ips) for the
// dirty-check. It deliberately EXCLUDES the endpoint: a roaming client's observed
// endpoint (NAT source port) changes constantly, so including it made peersEqual
// perpetually false, firing ApplyPeers on every reconcile (and, with the old
// empty-[Interface] syncconf, wiping the interface each time). WireGuard tracks
// roaming endpoints itself, so the desired->actual convergence must not treat the
// observed endpoint as a diff.
//
// BOUNDARY / EPIC 8: this means a legitimate desired ENDPOINT change is not caught
// by the dirty-check. Today no desired peer carries a meaningful static endpoint
// (clients roam). When EPIC 8 (site-to-site) adds gateway-DIALED peers whose
// static endpoint IS control-plane-managed, the dirty-check must distinguish a
// gateway peer's static endpoint (compare it) from a client's roaming one (ignore
// it) — e.g. a per-peer "static endpoint" flag. Ledger marker: EPIC 8 site-to-site.
func canon(p Peer) string {
	ips := append([]string(nil), p.AllowedIPs...)
	sort.Strings(ips)
	return p.PublicKey + "|" + strings.Join(ips, ",")
}
