// Command agent is the tunnex-node data-plane agent (S3.1).
//
// On boot it enrolls (join token -> mTLS cert) if it has no cert yet, then runs
// the reconcile loop against the control plane's desired state. The WireGuard
// backend is in-memory in S3.1; the real wgctrl device lands in S3.2.
package main

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"log/slog"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/tunnexio/tunnex/apps/node/internal/control"
	"github.com/tunnexio/tunnex/apps/node/internal/egress"
	"github.com/tunnexio/tunnex/apps/node/internal/flowlog"
	"github.com/tunnexio/tunnex/apps/node/internal/nodepolicy"
	"github.com/tunnexio/tunnex/apps/node/internal/reconcile"
)

const protocolVersion = 1

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	apiURL := getenv("TUNNEX_API_URL", "http://api:8080")
	agentURL := getenv("TUNNEX_AGENT_URL", "https://api:8443")
	serverName := getenv("TUNNEX_AGENT_SERVERNAME", "tunnex-control")
	joinToken := os.Getenv("TUNNEX_JOIN_TOKEN")
	nodeName := getenv("TUNNEX_NODE_NAME", hostname())
	certDir := getenv("TUNNEX_NODE_STATE_DIR", "/var/lib/tunnex-node")
	healthAddr := getenv("TUNNEX_AGENT_HEALTH_ADDR", ":9091")

	var ready, keyReported atomic.Bool
	go serveHealth(healthAddr, &ready, logger)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	certPEM, keyPEM, caPEM, err := loadCreds(certDir)
	if err != nil {
		// Not enrolled yet. Enroll if we have a join token; otherwise idle
		// (liveness up, readiness false) until one is provided.
		if joinToken == "" {
			logger.Warn("agent_not_enrolled", slog.String("reason", "no cert and no TUNNEX_JOIN_TOKEN"))
			<-ctx.Done()
			return
		}
		logger.Info("agent_enrolling", slog.String("node_name", nodeName))
		key, csr, gerr := control.GenerateKeyAndCSR(nodeName)
		if gerr != nil {
			logger.Error("agent_csr_failed", slog.String("error", gerr.Error()))
			os.Exit(1)
		}
		res, eerr := control.Enroll(ctx, apiURL, joinToken, csr, nodeName, version(), protocolVersion)
		if eerr != nil {
			logger.Error("agent_enroll_failed", slog.String("error", eerr.Error()))
			os.Exit(1)
		}
		certPEM, keyPEM, caPEM = []byte(res.CertPEM), key, []byte(res.CAPEM)
		if serr := saveCreds(certDir, certPEM, keyPEM, caPEM); serr != nil {
			logger.Error("agent_save_creds_failed", slog.String("error", serr.Error()))
			os.Exit(1)
		}
		logger.Info("agent_enrolled", slog.String("node_id", res.NodeID))
	}

	client, err := control.NewClient(agentURL, serverName, nodeName, certPEM, keyPEM, caPEM)
	if err != nil {
		logger.Error("agent_client_failed", slog.String("error", err.Error()))
		os.Exit(1)
	}
	// WireGuard key: generated locally and persisted; the private key never
	// leaves the node. Re-key = delete the file -> a new key is generated and its
	// pubkey re-reported.
	wgPriv, wgPub, err := loadOrCreateWGKey(filepath.Join(certDir, "wg.key"))
	if err != nil {
		logger.Error("agent_wg_key_failed", slog.String("error", err.Error()))
		os.Exit(1)
	}
	// Report the WG public key + public endpoint to the control plane, retrying
	// until it lands. A one-shot best-effort call could leave the control plane
	// without our key (transient boot-time error) while the agent still went
	// ready — a silent data-plane hole. Readiness is gated on keyReported below,
	// so we never advertise ready until the control plane actually holds our key.
	// The endpoint (host:port peer configs dial) is operator-provided; it cannot
	// be discovered from inside the container.
	wgEndpoint := os.Getenv("TUNNEX_NODE_ENDPOINT")
	// egressNAT holds whether this gateway can source-NAT full-tunnel egress (S3.7),
	// probed by the egress loop and reported to the control plane so it can refuse
	// full-tunnel devices against a no-egress gateway (gateway_no_egress).
	var egressNAT atomic.Bool

	// Backend selection: "wgctrl" drives a real WireGuard device (Linux + NET_ADMIN,
	// used in compose/prod); anything else uses the in-memory backend (dev/CI).
	wgBackend := getenv("TUNNEX_WG_BACKEND", "mem")
	wgIface := getenv("TUNNEX_WG_INTERFACE", "wg0")

	// Egress NAT + forwarding (S3.7): probe+arm ONCE synchronously so the very first
	// capability report is accurate (not a spurious egress_nat=false for one interval —
	// review #6), then reconcile on an interval (heals a flushed table). Torn down on
	// shutdown (full-sweep). No-op / not-capable off Linux.
	egressMgr := egress.New(wgIface)
	defer egressMgr.Teardown(context.Background())
	// S7.5.1 flow logging is OPT-IN per gateway: TUNNEX_FLOWLOG_GROUP>0 arms the forward-chain
	// nflog rules (set BEFORE the first Reconcile so the log clauses render from the start) and
	// the reader+drain below. 0 = OFF (the forward chain is byte-identical to pre-S7.5.1).
	flowGroup := getint("TUNNEX_FLOWLOG_GROUP", 0)
	if flowGroup > 0 {
		egressMgr.SetFlowLogGroup(flowGroup)
	}
	if ok, err := egressMgr.Reconcile(ctx); err != nil {
		logger.Warn("egress_initial_degraded", slog.String("error", err.Error()))
		egressNAT.Store(ok)
	} else {
		egressNAT.Store(ok)
	}
	// policyKick wakes the egress loop IMMEDIATELY when a desired-state fetch carries a
	// changed policy — enforcement rides the push path (<5s revocation spec), not the
	// egress interval. Buffered(1) + non-blocking send: coalesces bursts, never stalls
	// the reconcile loop.
	policyKick := make(chan struct{}, 1)
	go egressLoop(ctx, egressMgr, &egressNAT, getdur("TUNNEX_AGENT_EGRESS_INTERVAL", 30*time.Second), policyKick, logger)

	reportEvery := getdur("TUNNEX_AGENT_REPORT_INTERVAL", 30*time.Second)
	go reportKeyLoop(ctx, client, wgPub, wgEndpoint, &egressNAT, egressMgr, &keyReported, reportEvery, logger)

	backend, err := reconcile.SelectBackend(wgBackend, wgIface, logger)
	if err != nil {
		logger.Error("agent_backend_failed", slog.String("backend", wgBackend), slog.String("error", err.Error()))
		os.Exit(1)
	}
	logger.Info("agent_backend_selected", slog.String("backend", wgBackend), slog.String("interface", wgIface))
	r := reconcile.New(backend, wgPriv, wgPub, logger)
	// Every desired-state fetch hands the compiled Zero Trust policy (nil = legacy
	// mesh) to the egress manager and kicks an immediate forward-chain re-apply.
	r.OnPolicy(func(p *nodepolicy.Compiled) {
		egressMgr.SetPolicy(p)
		select {
		case policyKick <- struct{}{}:
		default: // a kick is already pending — the apply reads the latest policy anyway
		}
	})

	// S7.5.1 flow-log drive: read the nflog group the forward chain logs to, buffer the
	// flow-start records, and drain them to the CP on an interval (best-effort observability;
	// NEVER on the enforcement path). Enabled only when TUNNEX_FLOWLOG_GROUP>0.
	if flowGroup > 0 {
		startFlowLog(ctx, flowGroup, client, egressMgr, logger)
	}

	// Renew the cert at half-life (default 24h; the cert lives 48h) and hot-swap
	// it. Persist the rotated cert so a restart uses the current one. If renewal
	// fails until expiry, mTLS breaks and re-enrollment requires a fresh join
	// token (no silent re-admission).
	renewEvery := getdur("TUNNEX_AGENT_RENEW_INTERVAL", 24*time.Hour)
	go renewLoop(ctx, client, certDir, renewEvery, logger)

	// Report per-peer live telemetry (handshake/bytes/endpoint) on an interval.
	go statusLoop(ctx, client, backend, getdur("TUNNEX_AGENT_STATUS_INTERVAL", 30*time.Second), logger)

	// Readiness mirrors the reconciler's health (enrolled + control session +
	// backend converged). It flips false if the backend later fails (e.g. device
	// lost) so orchestrators see the true state, not a stale first success.
	go func() {
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		var announced bool
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				h := r.Healthy() && keyReported.Load()
				ready.Store(h)
				if h && !announced {
					announced = true
					logger.Info("agent_ready")
				}
			}
		}
	}()

	logger.Info("agent_reconciling", slog.String("node_name", nodeName))
	// Interval is env-overridable so the data-plane e2e can sample device stability
	// across ≥2 reconcile cycles quickly (default 60s in production).
	r.Run(ctx, client, getdur("TUNNEX_AGENT_RECONCILE_INTERVAL", 60*time.Second), 5*time.Second)
	logger.Info("agent_stopped")
}

// serveHealth exposes liveness (process up) and readiness (enrolled + control
// session + backend healthy) — the split S8 multi-gateway views consume.
func serveHealth(addr string, ready *atomic.Bool, logger *slog.Logger) {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "service": "tunnex-node"})
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if !ready.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "not_ready"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ready"})
	})
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("agent_health_failed", slog.String("error", err.Error()))
	}
}

func loadCreds(dir string) (cert, key, ca []byte, err error) {
	cert, err = os.ReadFile(filepath.Join(dir, "cert.pem"))
	if err != nil {
		return nil, nil, nil, err
	}
	key, err = os.ReadFile(filepath.Join(dir, "key.pem"))
	if err != nil {
		return nil, nil, nil, err
	}
	ca, err = os.ReadFile(filepath.Join(dir, "ca.pem"))
	if err != nil {
		return nil, nil, nil, err
	}
	return cert, key, ca, nil
}

func saveCreds(dir string, cert, key, ca []byte) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	for name, data := range map[string][]byte{"cert.pem": cert, "key.pem": key, "ca.pem": ca} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			return err
		}
	}
	return nil
}

func renewLoop(ctx context.Context, client *control.Client, certDir string, every time.Duration, logger *slog.Logger) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			certPEM, keyPEM, err := client.Renew(ctx, version())
			if err != nil {
				logger.Warn("agent_renew_failed", slog.String("error", err.Error()))
				continue
			}
			ca, _ := os.ReadFile(filepath.Join(certDir, "ca.pem"))
			if err := saveCreds(certDir, certPEM, keyPEM, ca); err != nil {
				logger.Warn("agent_renew_persist_failed", slog.String("error", err.Error()))
				continue
			}
			logger.Info("agent_cert_renewed")
		}
	}
}

// reportKeyLoop reports the node's WG public key to the control plane, retrying
// with backoff until it succeeds (then sets reported and returns). The report is
// idempotent server-side, so retrying is safe. Until it succeeds the agent stays
// not-ready, so no orchestrator routes to a node the control plane can't peer.
func reportKeyLoop(ctx context.Context, client *control.Client, pubKey, endpoint string, egressNAT *atomic.Bool, egressMgr *egress.Manager, reported *atomic.Bool, every time.Duration, logger *slog.Logger) {
	const maxBackoff = 30 * time.Second
	report := func() bool {
		// Applied-policy status rides the capability report (S7.2 staleness): version +
		// canonical hash of what is IN FORCE, plus the last apply error. The control
		// plane compares against what it pushed — a stale gateway must be visible.
		v, h, failingSince, applyErr := egressMgr.AppliedStatus()
		ps := control.PolicyStatus{Version: v, Hash: h}
		if applyErr != nil {
			ps.Error = applyErr.Error()
			if len(ps.Error) > 300 { // bound so a verbose nft error can't overflow the report body (finding #4)
				ps.Error = ps.Error[:300]
			}
		}
		if !failingSince.IsZero() {
			ps.FailingSince = failingSince.UTC().Format(time.RFC3339)
		}
		if err := client.ReportInfo(ctx, pubKey, endpoint, egressNAT.Load(), ps); err != nil {
			logger.Warn("agent_report_key_failed", slog.String("error", err.Error()))
			return false
		}
		if reported.CompareAndSwap(false, true) {
			logger.Info("agent_wg_key_reported", slog.String("public_key", pubKey))
		}
		return true
	}
	// Retry fast until the FIRST success (readiness is gated on it).
	backoff := time.Second
	for !report() {
		if !sleepCtx(ctx, backoff) {
			return
		}
		if backoff *= 2; backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
	// Then re-report on an interval so a CHANGED egress_nat capability (host state can
	// shift) propagates to the control plane — the decision was "report every reconcile".
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			report()
		}
	}
}

// egressLoop reconciles the gateway egress NAT + the Zero Trust forward chain (the
// tunnex nft tables) every interval — idempotent, so it heals a flushed table — and
// updates the egress_nat capability that reportKeyLoop advertises. It ALSO applies
// immediately on a policy kick (a pushed policy change must land within the <5s
// revocation spec, not wait out the interval). A degraded reconcile (locked-down
// host) sets egress_nat=false and logs, never crashing the agent.
func egressLoop(ctx context.Context, mgr *egress.Manager, egressNAT *atomic.Bool, every time.Duration, kick <-chan struct{}, logger *slog.Logger) {
	apply := func() {
		ok, err := mgr.Reconcile(ctx)
		egressNAT.Store(ok)
		if err != nil {
			logger.Warn("egress_reconcile_degraded", slog.String("error", err.Error()))
		}
	}
	// The initial probe/arm ran synchronously in main() before the first report — this
	// loop only re-reconciles on the interval (heals a flushed table, tracks capability)
	// and on policy kicks (push-path enforcement).
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-kick:
			apply()
		case <-t.C:
			apply()
		}
	}
}

// sleepCtx sleeps for d, returning false if ctx is cancelled first.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// maxStatusPeers caps a status report so a gateway with thousands of peers can't
// turn a heartbeat into a huge post. Excess is dropped (and logged) — the status
// view is best-effort telemetry, not the source of truth.
const maxStatusPeers = 1000

// statusLoop periodically reads per-peer telemetry from the backend and reports
// it. Best-effort: a failed report only means a momentarily stale status view.
func statusLoop(ctx context.Context, client *control.Client, backend reconcile.WGBackend, every time.Duration, logger *slog.Logger) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			stats, err := backend.Stats(ctx)
			if err != nil {
				logger.Warn("agent_stats_read_failed", slog.String("error", err.Error()))
				continue
			}
			if len(stats) == 0 {
				continue
			}
			if len(stats) > maxStatusPeers {
				logger.Warn("agent_status_truncated", slog.Int("peers", len(stats)), slog.Int("cap", maxStatusPeers))
				stats = stats[:maxStatusPeers]
			}
			if err := client.ReportStatus(ctx, stats); err != nil {
				logger.Warn("agent_status_report_failed", slog.String("error", err.Error()))
			}
		}
	}
}

// loadOrCreateWGKey loads (or generates + persists) the node's WireGuard key,
// returning base64 private and public keys. A missing OR unparseable file (e.g.
// a crash mid-write left it empty/truncated) triggers regeneration rather than a
// hard error — otherwise a corrupt key file would wedge the agent in a permanent
// crash-loop with no way to self-heal.
func loadOrCreateWGKey(path string) (privB64, pubB64 string, err error) {
	curve := ecdh.X25519()
	if data, rerr := os.ReadFile(path); rerr == nil {
		trimmed := strings.TrimSpace(string(data))
		if raw, derr := base64.StdEncoding.DecodeString(trimmed); derr == nil {
			if pk, perr := curve.NewPrivateKey(raw); perr == nil {
				return trimmed, base64.StdEncoding.EncodeToString(pk.PublicKey().Bytes()), nil
			}
		}
		// File exists but is corrupt/empty — fall through and regenerate.
	}
	pk, gerr := curve.GenerateKey(rand.Reader)
	if gerr != nil {
		return "", "", gerr
	}
	priv := base64.StdEncoding.EncodeToString(pk.Bytes())
	if werr := os.WriteFile(path, []byte(priv), 0o600); werr != nil {
		return "", "", werr
	}
	return priv, base64.StdEncoding.EncodeToString(pk.PublicKey().Bytes()), nil
}

func getdur(k string, def time.Duration) time.Duration {
	if v, ok := os.LookupEnv(k); ok && v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

func getenv(k, def string) string {
	if v, ok := os.LookupEnv(k); ok && v != "" {
		return v
	}
	return def
}

func getint(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// startFlowLog wires the nflog reader → pump → periodic drain → ReportFlows (S7.5.1 7/n).
// Best-effort + isolated: a source-open failure logs and returns (enforcement + the rest of
// the agent are unaffected); the pump/drain run until ctx is cancelled. Each event is stamped
// with the APPLIED policy hash so the CP can detect a ruleset-swap-window skew.
func startFlowLog(ctx context.Context, group int, client *control.Client, egressMgr *egress.Manager, logger *slog.Logger) {
	sockBuf := getint("TUNNEX_FLOWLOG_SOCKBUF", flowlog.DefaultNflogSockBuf)
	src, err := flowlog.NewNflogSource(ctx, group, sockBuf)
	if err != nil {
		logger.Error("flowlog_source_failed", slog.Int("group", group), slog.String("error", err.Error()))
		return
	}
	pump := flowlog.NewPump(src, flowlog.NewBuffer(0), func() string {
		_, hash, _, _ := egressMgr.AppliedStatus()
		return hash
	})
	go pump.Run(ctx)
	go flowlog.RunDrain(ctx, pump, client, getdur("TUNNEX_FLOWLOG_INTERVAL", flowlog.DefaultDrainInterval), logger)
	logger.Info("flowlog_started", slog.Int("group", group), slog.Int("sock_buf_bytes", sockBuf))
}

func hostname() string { h, _ := os.Hostname(); return h }
func version() string  { return getenv("TUNNEX_AGENT_VERSION", "0.1.0") }
