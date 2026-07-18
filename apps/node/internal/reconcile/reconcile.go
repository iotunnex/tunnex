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
	// SiteLink (S8.2) marks a gateway-DIALED site-link peer whose Endpoint is control-plane-managed
	// (static). The dirty-check compares Endpoint for these (B4) so a hub endpoint change re-dials; device
	// peers roam → SiteLink=false → endpoint-blind (TestReconcileIgnoresRoamedEndpoint stays armed).
	SiteLink bool `json:"site_link,omitempty"`
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
	// ApplyRoutes reconciles the kernel routes to remote SITE subnets (S8.2): install each desired
	// route via the tunnel iface (idempotent — heals a flushed route next tick) and PRUNE our routes
	// no longer desired (the full-sweep contract: a site unbind/subnet removal drops the route). Only
	// agent-owned routes are touched; the interface's own on-link route is never pruned.
	ApplyRoutes(ctx context.Context, cidrs []string) error
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
	// siteLinkStale (S8.2 H5) is an optional sink: each reconcile, the agent checks its SITE-LINK peers'
	// WG handshakes and stores whether any is stale/absent, so the report loop can surface site_link_down.
	// nil when not wired (e.g. tests) → the check is skipped.
	siteLinkStale *atomic.Bool
	lastStatsOK   time.Time // F2: last successful backend.Stats read — the one timestamp for three-state staleness
}

// SetSiteLinkStaleSink wires the H5 site-link-staleness sink (read by the report loop). Call before Run.
func (r *Reconciler) SetSiteLinkStaleSink(b *atomic.Bool) { r.siteLinkStale = b }

// siteLinkStaleWindow: a site-link peer with no handshake within this window (or never) is stale. Errs
// toward over-reporting (a false site_link_down is an annoyance; a false-healthy dead bridge is the
// blackhole class). Comfortably above WireGuard's ~2-min rehandshake/keepalive cadence.
const siteLinkStaleWindow = 180 * time.Second

// updateSiteLinkStale checks the desired SITE-LINK peers' handshakes and stores staleness in the sink.
func (r *Reconciler) updateSiteLinkStale(ctx context.Context, desired []Peer) {
	var sitePubs []string
	for _, p := range desired {
		if p.SiteLink {
			sitePubs = append(sitePubs, p.PublicKey)
		}
	}
	if len(sitePubs) == 0 {
		r.siteLinkStale.Store(false) // no site links on this gateway → nothing to be stale
		return
	}
	stats, err := r.backend.Stats(ctx)
	if err != nil {
		// THREE-STATE on a Stats error (F2/R4), one timestamp, no debounce machinery: (1) cold start —
		// never a good reading — report STALE (over-report once; a maybe-dead link reads dead). (2) a
		// TRANSIENT error within the staleness window of the last good read — KEEP the last value (kills
		// the flap: a genuinely-up link doesn't oscillate on an intermittent wg-dump hiccup). (3) an error
		// PERSISTING past the window — report STALE (can no longer vouch the link is up).
		if r.lastStatsOK.IsZero() || time.Since(r.lastStatsOK) > siteLinkStaleWindow {
			r.siteLinkStale.Store(true)
		}
		return // else: keep last value
	}
	r.lastStatsOK = time.Now()
	hs := make(map[string]int64, len(stats))
	for _, s := range stats {
		hs[s.PublicKey] = s.LastHandshake
	}
	now := time.Now().Unix()
	stale := false
	for _, pub := range sitePubs {
		h, ok := hs[pub]
		if !ok || h == 0 || now-h > int64(siteLinkStaleWindow.Seconds()) {
			stale = true
			break
		}
	}
	r.siteLinkStale.Store(stale)
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
	// S8.2: converge the site-to-site kernel routes (from Policy.Routes — explicit intent, never
	// inferred from a peer's AllowedIPs). After peers so the interface + crypto-routing exist.
	var routes []string
	if ds.Policy != nil {
		for _, rt := range ds.Policy.Routes {
			routes = append(routes, rt.DstCIDR)
		}
	}
	if err := r.backend.ApplyRoutes(ctx, routes); err != nil {
		r.healthy.Store(false)
		return false, err
	}
	// H5: refresh the site-link-staleness signal (best-effort; never fails the reconcile).
	if r.siteLinkStale != nil {
		r.updateSiteLinkStale(ctx, ds.Peers)
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

// peersEqual compares the ACTUAL peer set (from the kernel/wg dump) against the DESIRED set (from the
// control plane). OUTER structure is the sorted-MULTISET compare (F4/R2 restore) — canon both, sort,
// pairwise — so a duplicate-pubkey desired set can't hide an unpruned actual peer (a map-keyed compare
// dropped that). The per-peer Endpoint conditionality lives INSIDE the comparator: after sorting on
// canon (pubkey + sorted allowed-ips, endpoint-BLIND), aligned pairs share pubkey+ips, so the DESIRED
// peer's SiteLink flag decides whether the static Endpoint must match (B4). Only the desired side ever
// carries SiteLink (CP intent); the kernel read path never does — keying canon on SiteLink was the R2
// perpetual-dirty bug.
func peersEqual(actual, desired []Peer) bool {
	if len(actual) != len(desired) {
		return false
	}
	a := append([]Peer(nil), actual...)
	d := append([]Peer(nil), desired...)
	sort.Slice(a, func(i, j int) bool { return canon(a[i]) < canon(a[j]) })
	sort.Slice(d, func(i, j int) bool { return canon(d[i]) < canon(d[j]) })
	for i := range a {
		if canon(a[i]) != canon(d[i]) { // pubkey + allowed-ips (multiset-exact)
			return false
		}
		if d[i].SiteLink && a[i].Endpoint != d[i].Endpoint { // site-link static endpoint must match (B4)
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
	return p.PublicKey + "|" + strings.Join(ips, ",") // endpoint-blind; peersEqual compares endpoints per SiteLink (B4/R2)
}
